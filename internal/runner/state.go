// internal/runner/state.go
package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"git.disroot.org/jmy/regis/internal/cue"
)

const stateSchema = 1

// State records what regis last deployed to a target: per-cue file inventory
// with remote paths and content fingerprints for drift detection and prune.
// It is metadata only — no file content is stored.
type State struct {
	Schema     int                 `yaml:"schema"`
	ID         string              `yaml:"id"`
	Checksum   string              `yaml:"checksum,omitempty"`
	GitRef     string              `yaml:"git_ref,omitempty"`
	GitClean   bool                `yaml:"git_clean,omitempty"`
	DeployedAt time.Time           `yaml:"deployed_at"`
	DeployedBy string              `yaml:"deployed_by"`
	Target     string              `yaml:"target"`
	Scenarios  []string            `yaml:"scenarios"`
	Cues       map[string]CueState `yaml:"cues,omitempty"`
}

// CueState holds the deployed file inventory for one cue.
type CueState struct {
	Nature string               `yaml:"nature"`
	Files  map[string]FileState `yaml:"files,omitempty"`
}

// FileState records the fingerprint of one deployed file.
// Key in the parent map is the relative path within the cue (e.g. "index.html",
// "js/app.js"). For single-file cues the key is the cue name itself.
// Mtime and Size are omitted for secret cues (avoid leaking metadata).
// Hash is omitted when the file was equal via mtime/size fast-path on first deploy;
// it is populated on the next state check or deploy.
type FileState struct {
	Remote string `yaml:"remote"`          // absolute remote path
	Mtime  int64  `yaml:"mtime,omitempty"` // unix epoch seconds
	Size   int64  `yaml:"size,omitempty"`  // bytes
	Hash   string `yaml:"hash,omitempty"`  // MD5 hex
}

// stateUploader is the SSH subset needed to read/write remote state files.
type stateUploader interface {
	Run(cmd string) (stdout, stderr string, exitCode int, err error)
	UploadBytes(data []byte, remotePath string, mode fs.FileMode, useSudo bool) error
	Download(remotePath string) ([]byte, error)
}

// computeChecksum returns a SHA-256 hex digest of s serialised to YAML,
// with the Checksum field zeroed so the digest is stable across writes.
func computeChecksum(s State) string {
	s.Checksum = ""
	data, _ := yaml.Marshal(s)
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

// NewStateID generates a state ID of the form v20060102-150405.
// When HEAD is on a git tag, appends it: v20060102-150405+v1.2.3.
func NewStateID() string {
	ts := time.Now().UTC().Format("v20060102-150405")
	out, err := exec.Command("git", "describe", "--exact-match", "--tags", "HEAD").Output()
	if err != nil {
		return ts
	}
	if tag := strings.TrimSpace(string(out)); tag != "" {
		return ts + "+" + tag
	}
	return ts
}

// BuildState constructs a State from the results of a deploy run.
// prev, if non-nil, is the previous state — its file hashes are inherited for
// files that did not change (mtime/size fast-path), avoiding re-hashing.
func BuildState(
	id string,
	scenarioNames []string,
	results []cue.Result,
	steps []Step,
	targetDir, targetName string,
	prev *State,
) State {
	hostname, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}

	s := State{
		Schema:     stateSchema,
		ID:         id,
		GitRef:     currentGitRef(),
		GitClean:   isGitClean(),
		DeployedAt: time.Now().UTC(),
		DeployedBy: user + "@" + hostname,
		Target:     targetName,
		Scenarios:  scenarioNames,
		Cues:       make(map[string]CueState),
	}

	// Build a step index for remote path resolution.
	stepByCue := make(map[string]Step, len(steps))
	for _, st := range steps {
		stepByCue[st.CueRef.Name] = st
	}

	// Build a previous-state file hash index: (cueName, relKey) → hash
	type prevKey struct{ cue, rel string }
	prevHashes := map[prevKey]FileState{}
	if prev != nil {
		for cueName, cs := range prev.Cues {
			for rel, fs := range cs.Files {
				prevHashes[prevKey{cueName, rel}] = fs
			}
		}
	}

	for _, r := range results {
		if r.Status == cue.StatusSkipped || r.Status == cue.StatusFailed {
			continue
		}

		cs := CueState{Nature: r.Nature}

		switch r.Nature {
		case "pack":
			// Multi-file only: ArtifactPaths has key → remote path.
			if len(r.ArtifactPaths) == 0 {
				continue
			}
			cs.Files = make(map[string]FileState, len(r.ArtifactPaths))
			for key, remotePath := range r.ArtifactPaths {
				relKey := strings.TrimPrefix(key, r.CueName+"/")
				fs := FileState{Remote: remotePath}
				if h, ok := r.LocalFileHashes[key]; ok && h != "" {
					fs.Hash = h
				} else if pk := (prevKey{r.CueName, relKey}); prevHashes[pk].Hash != "" && r.Status == cue.StatusEqual {
					fs.Hash = prevHashes[pk].Hash
				}
				cs.Files[relKey] = fs
			}

		case "render":
			if len(r.ArtifactPaths) > 0 {
				// Folder render: ArtifactPaths has key → remote path.
				cs.Files = make(map[string]FileState, len(r.ArtifactPaths))
				for key, remotePath := range r.ArtifactPaths {
					relKey := strings.TrimPrefix(key, r.CueName+"/")
					fs := FileState{Remote: remotePath}
					if h, ok := r.LocalFileHashes[key]; ok && h != "" {
						fs.Hash = h
					} else if pk := (prevKey{r.CueName, relKey}); prevHashes[pk].Hash != "" && r.Status == cue.StatusEqual {
						fs.Hash = prevHashes[pk].Hash
					}
					cs.Files[relKey] = fs
				}
			} else {
				// Single-file render.
				st, ok := stepByCue[r.CueName]
				if !ok || st.CueRef.Dest == "" {
					continue
				}
				relKey := r.CueName
				fs := FileState{Remote: resolveRemoteDest(st.CueRef.Dest, targetDir)}
				if r.LocalHash != "" {
					fs.Hash = r.LocalHash
				} else if pk := (prevKey{r.CueName, relKey}); prevHashes[pk].Hash != "" && r.Status == cue.StatusEqual {
					fs.Hash = prevHashes[pk].Hash
				}
				cs.Files = map[string]FileState{relKey: fs}
			}

		case "binary":
			remotePath := r.RemotePath
			if remotePath == "" {
				if st, ok := stepByCue[r.CueName]; ok {
					remotePath = resolveRemoteDest(st.CueRef.Dest, targetDir)
				}
			}
			if remotePath == "" {
				continue
			}
			relKey := r.CueName
			fs := FileState{Remote: remotePath}
			if r.LocalHash != "" {
				fs.Hash = r.LocalHash
			} else if pk := (prevKey{r.CueName, relKey}); prevHashes[pk].Hash != "" && r.Status == cue.StatusEqual {
				fs.Hash = prevHashes[pk].Hash
			}
			if !r.LocalMtime.IsZero() {
				fs.Mtime = r.LocalMtime.Unix()
			}
			cs.Files = map[string]FileState{relKey: fs}

		case "config":
			st, ok := stepByCue[r.CueName]
			if !ok || st.CueRef.Dest == "" {
				continue
			}
			relKey := r.CueName
			fs := FileState{Remote: resolveRemoteDest(st.CueRef.Dest, targetDir)}
			if r.LocalHash != "" {
				fs.Hash = r.LocalHash
			} else if pk := (prevKey{r.CueName, relKey}); prevHashes[pk].Hash != "" && r.Status == cue.StatusEqual {
				fs.Hash = prevHashes[pk].Hash
			}
			cs.Files = map[string]FileState{relKey: fs}

		case "secret":
			st, ok := stepByCue[r.CueName]
			if !ok || st.CueRef.Dest == "" {
				continue
			}
			remotePath := resolveRemoteDest(st.CueRef.Dest, targetDir)
			relKey := r.CueName
			// Secret: hash only, no mtime/size (avoid leaking metadata).
			fs := FileState{Remote: remotePath}
			if r.LocalHash != "" {
				fs.Hash = r.LocalHash
			} else if pk := (prevKey{r.CueName, relKey}); prevHashes[pk].Hash != "" && r.Status == cue.StatusEqual {
				fs.Hash = prevHashes[pk].Hash
			}
			cs.Files = map[string]FileState{relKey: fs}

		default:
			// action, service, generate: no files deployed to a dest
			continue
		}

		if len(cs.Files) > 0 {
			s.Cues[r.CueName] = cs
		}
	}

	if len(s.Cues) == 0 {
		s.Cues = nil
	}
	return s
}

// remoteStatesDir returns the remote directory that holds state archives.
func remoteStatesDir(targetDir string) string {
	return targetDir + "/.regis-states"
}

// SaveState writes the state as a YAML file under localDir/<target>/<id>.yml.
// Returns a non-nil error if the write fails.
func SaveState(s State, localDir string) error {
	s.Checksum = computeChecksum(s)
	dir := filepath.Join(localDir, s.Target)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("state: mkdir %s: %w", dir, err)
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("state: marshal: %w", err)
	}
	p := filepath.Join(dir, s.ID+".yml")
	if err := os.WriteFile(p, data, 0644); err != nil {
		return fmt.Errorf("state: write %s: %w", p, err)
	}
	return nil
}

// WriteStateToRemote uploads the state to <targetDir>/.regis-states/<id>.yml.
func WriteStateToRemote(conn stateUploader, targetDir string, s State, sudo bool) error {
	s.Checksum = computeChecksum(s)
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("state: marshal for remote: %w", err)
	}
	dir := remoteStatesDir(targetDir)
	if _, _, code, runErr := conn.Run(fmt.Sprintf("mkdir -p %s", shellQuotePath(dir))); runErr != nil || code != 0 {
		return fmt.Errorf("state: mkdir %s on remote: %w", dir, runErr)
	}
	p := dir + "/" + s.ID + ".yml"
	if err := conn.UploadBytes(data, p, 0644, sudo); err != nil {
		return fmt.Errorf("state: upload %s: %w", p, err)
	}
	return nil
}

// ListRemoteStates returns state IDs found in <targetDir>/.regis-states/, newest first.
func ListRemoteStates(conn stateUploader, targetDir string) ([]string, error) {
	dir := remoteStatesDir(targetDir)
	stdout, _, _, err := conn.Run(fmt.Sprintf("find %s -maxdepth 1 -name '*.yml' -type f 2>/dev/null", shellQuotePath(dir)))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		base := path.Base(line)
		if strings.HasSuffix(base, ".yml") {
			ids = append(ids, strings.TrimSuffix(base, ".yml"))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids, nil
}

// LoadRemoteState downloads the newest state from <targetDir>/.regis-states/.
func LoadRemoteState(conn stateUploader, targetDir string) (*State, error) {
	ids, err := ListRemoteStates(conn, targetDir)
	if err != nil || len(ids) == 0 {
		return nil, fmt.Errorf("no remote states in %s/.regis-states", targetDir)
	}
	return LoadRemoteStateByID(conn, targetDir, ids[0])
}

// LoadRemoteStateByID downloads a specific state from <targetDir>/.regis-states/<id>.yml.
func LoadRemoteStateByID(conn stateUploader, targetDir, id string) (*State, error) {
	p := remoteStatesDir(targetDir) + "/" + id + ".yml"
	data, err := conn.Download(p)
	if err != nil {
		return nil, err
	}
	var s State
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", id, err)
	}
	return &s, nil
}

// shellQuotePath single-quotes a path for use in remote shell commands.
// Assumes targetDir has already been expanded (no ~).
func shellQuotePath(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// LoadLocalState reads a specific state record from localDir/<target>/<id>.yml.
func LoadLocalState(localDir, target, id string) (*State, error) {
	path := filepath.Join(localDir, target, id+".yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// LatestLocalState returns the most recent state for a target, or nil if none.
func LatestLocalState(localDir, target string) *State {
	dir := filepath.Join(localDir, target)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	// ReadDir is lexicographic; state IDs are date-prefixed so last = newest.
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".yml")
		if s, err := LoadLocalState(localDir, target, id); err == nil {
			return s
		}
	}
	return nil
}

// ListLocalStates returns state IDs for a target, newest first.
func ListLocalStates(localDir, target string) []string {
	dir := filepath.Join(localDir, target)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yml") {
			ids = append(ids, strings.TrimSuffix(e.Name(), ".yml"))
		}
	}
	// Reverse to newest first.
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids
}

// PruneLocalStates removes old state files, keeping the most recent keep.
func PruneLocalStates(localDir, target string, keep int) {
	if keep <= 0 {
		keep = 5
	}
	ids := ListLocalStates(localDir, target) // newest first
	for i := keep; i < len(ids); i++ {
		path := filepath.Join(localDir, target, ids[i]+".yml")
		_ = os.Remove(path)
	}
}

// StatePruneCandidates returns remote paths that were in prev but are not in curr
// for the given cue — files that should be deleted as part of a prune during deploy.
func StatePruneCandidates(prev, curr *State, cueName string) []string {
	if prev == nil {
		return nil
	}
	prevCue, ok := prev.Cues[cueName]
	if !ok {
		return nil
	}
	var currPaths map[string]bool
	if curr != nil {
		if cc, ok := curr.Cues[cueName]; ok {
			currPaths = make(map[string]bool, len(cc.Files))
			for _, fs := range cc.Files {
				currPaths[fs.Remote] = true
			}
		}
	}
	var candidates []string
	for _, fs := range prevCue.Files {
		if !currPaths[fs.Remote] {
			candidates = append(candidates, fs.Remote)
		}
	}
	return candidates
}

// resolveRemoteDest resolves dest relative to targetDir for a remote Linux path.
func resolveRemoteDest(dest, targetDir string) string {
	if path.IsAbs(dest) {
		return dest
	}
	return path.Join(targetDir, dest)
}

// currentGitRef returns the full SHA of HEAD, or "" if not in a git repo.
func currentGitRef() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// isGitClean reports whether the working tree has no uncommitted changes
// (tracked or untracked). A dirty tree means the git_ref does not fully
// represent what was deployed.
func isGitClean() bool {
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return false // can't tell → assume not clean
	}
	return len(strings.TrimSpace(string(out))) == 0
}

// GitDirtyReason builds a human-readable description of what makes the
// working tree dirty: modified tracked files and/or untracked files.
// Returns "" when the tree is clean or not in a git repo.
func GitDirtyReason() string { return gitDirtyReason() }

// gitDirtyReason is the unexported implementation.
func gitDirtyReason() string {
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return ""
	}
	var modified, untracked []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		file := strings.TrimSpace(line[3:])
		if xy == "??" {
			untracked = append(untracked, file)
		} else {
			modified = append(modified, file)
		}
	}
	var parts []string
	if len(modified) > 0 {
		parts = append(parts, "uncommitted changes: "+strings.Join(modified, " "))
	}
	if len(untracked) > 0 {
		parts = append(parts, "untracked files: "+strings.Join(untracked, " "))
	}
	return strings.Join(parts, "\n")
}
