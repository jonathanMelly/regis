// internal/runner/manifest.go
package runner

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// ReleaseManifest records what was deployed and the checksums/paths of deployed files.
// Written to <target.dir>/.regis-release after each successful deploy.
type ReleaseManifest struct {
	Release      string                       `yaml:"release"`
	DeployedAt   time.Time                    `yaml:"deployed_at"`
	DeployedBy   string                       `yaml:"deployed_by"`
	Scenarios    []string                     `yaml:"scenarios"`
	Checksums    map[string]string            `yaml:"checksums,omitempty"`
	Artifacts    map[string]string            `yaml:"artifacts,omitempty"`    // cue name → remote path (for rollback)
	CueArtifacts map[string]map[string]string `yaml:"cue_artifacts,omitempty"` // cue name → {snapshotKey → remote path}
}

// manifestUploader is the subset of cue.SSHConn needed to write the manifest.
type manifestUploader interface {
	UploadBytes(data []byte, remotePath string, mode fs.FileMode, useSudo bool) error
}

// BuildManifest constructs a ReleaseManifest from deploy results and steps.
// Checksums are populated for StatusChanged binary/config/secret cues that have LocalMD5.
// Artifacts maps every binary/config/secret/render cue to its remote path (needed for rollback).
func BuildManifest(releaseID string, scenarios []string, results []cue.Result, steps []Step, targetDir string) ReleaseManifest {
	hostname, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME") // Windows fallback
	}
	deployedBy := user + "@" + hostname

	checksums := make(map[string]string)
	for _, r := range results {
		if r.Status != cue.StatusChanged {
			continue
		}
		switch r.Nature {
		case "binary", "config", "secret":
			if r.LocalMD5 != "" {
				checksums[r.CueName] = r.LocalMD5
			}
		}
	}
	if len(checksums) == 0 {
		checksums = nil
	}

	artifacts := make(map[string]string)
	cueArtifacts := make(map[string]map[string]string)

	// Pack cues: use ArtifactPaths populated by the executor (cueName/relpath → remote).
	for _, r := range results {
		if len(r.ArtifactPaths) > 0 {
			cueArtifacts[r.CueName] = r.ArtifactPaths
			for _, remotePath := range r.ArtifactPaths {
				// Flatten into artifacts for backward compat (last write wins for duplicates).
				artifacts[r.CueName] = remotePath
				break
			}
		}
	}

	for _, step := range steps {
		cr := step.CueRef
		if cr.Dest == "" {
			continue
		}
		switch cr.Nature {
		case "binary", "config", "secret":
			remotePath := resolveRemoteDest(cr.Dest, targetDir)
			artifacts[cr.Name] = remotePath
			cueArtifacts[cr.Name] = map[string]string{cr.Name: remotePath}
		case "render":
			// Folder mode: dest has a trailing slash or LocalDest is a directory.
			// Walk LocalDest and record per-file artifact paths using composite keys
			// "cueName/relpath" → remotePath so reuploadFromLocal can reconstruct each file.
			if cr.LocalDest != "" {
				if info, statErr := os.Stat(cr.LocalDest); statErr == nil && info.IsDir() {
					remoteBase := resolveRemoteDest(strings.TrimRight(cr.Dest, "/"), targetDir)
					m := make(map[string]string)
					_ = filepath.WalkDir(cr.LocalDest, func(p string, d fs.DirEntry, err error) error {
						if err != nil || d.IsDir() {
							return err
						}
						rel, _ := filepath.Rel(cr.LocalDest, p)
						relFwd := filepath.ToSlash(rel)
						key := cr.Name + "/" + relFwd
						remoteFull := remoteBase + "/" + relFwd
						artifacts[key] = remoteFull
						m[key] = remoteFull
						return nil
					})
					cueArtifacts[cr.Name] = m
					continue
				}
			}
			remotePath := resolveRemoteDest(cr.Dest, targetDir)
			artifacts[cr.Name] = remotePath
			cueArtifacts[cr.Name] = map[string]string{cr.Name: remotePath}
		}
	}
	if len(artifacts) == 0 {
		artifacts = nil
	}
	if len(cueArtifacts) == 0 {
		cueArtifacts = nil
	}

	return ReleaseManifest{
		Release:      releaseID,
		DeployedAt:   time.Now().UTC(),
		DeployedBy:   deployedBy,
		Scenarios:    scenarios,
		Checksums:    checksums,
		Artifacts:    artifacts,
		CueArtifacts: cueArtifacts,
	}
}

// resolveRemoteDest resolves dest relative to targetDir for a remote Linux path.
// Uses path.Join (forward slashes) rather than filepath.Join to stay correct on Windows.
func resolveRemoteDest(dest, targetDir string) string {
	if path.IsAbs(dest) {
		return dest
	}
	return path.Join(targetDir, dest)
}

// WriteManifest marshals m as YAML and uploads it to <targetDir>/.regis-release.
func WriteManifest(conn manifestUploader, targetDir string, m ReleaseManifest, sudo bool) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	remotePath := targetDir + "/.regis-release"
	if err := conn.UploadBytes(data, remotePath, 0644, sudo); err != nil {
		return fmt.Errorf("upload manifest: %w", err)
	}
	return nil
}

// NewReleaseID generates a release ID of the form v20060102-150405.
// When HEAD is on a git tag, appends the tag: v20060102-150405+v1.2.3.
func NewReleaseID() string {
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

// SnapshotRelease writes a local release snapshot to releaseDir/releaseID/.
// Copies the source file for each binary/config/secret/render step, plus the manifest YAML.
// Render folder mode: files are stored with composite key "cueName/relpath".
// Pack cues: all local files are stored using LocalArtifacts from executor results (cueName/relpath keys).
// Auto-prune is NOT performed — call PruneLocalSnapshots explicitly when desired.
// Non-fatal: errors are silently ignored.
func SnapshotRelease(releaseDir, releaseID string, manifest ReleaseManifest, steps []Step, results []cue.Result) {
	if releaseDir == "" || releaseID == "" {
		return
	}

	// Build a cueName → Result index for pack artifact lookups.
	resultByName := make(map[string]cue.Result, len(results))
	for _, r := range results {
		resultByName[r.CueName] = r
	}

	files := make(map[string][]byte, len(steps))
	for _, step := range steps {
		cr := step.CueRef

		// Pack cues: use LocalArtifacts populated by the pack executor.
		if cr.Nature == "pack" {
			if r, ok := resultByName[cr.Name]; ok {
				for key, localPath := range r.LocalArtifacts {
					data, err := os.ReadFile(localPath)
					if err != nil {
						continue
					}
					files[key] = data
				}
			}
			continue
		}

		localPath := cueSnapshotPath(cr)
		if localPath == "" {
			continue
		}
		// Render folder mode: LocalDest is a directory — walk and collect all files.
		if cr.Nature == "render" {
			if info, statErr := os.Stat(localPath); statErr == nil && info.IsDir() {
				_ = filepath.WalkDir(localPath, func(p string, d fs.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						return err
					}
					rel, _ := filepath.Rel(localPath, p)
					data, readErr := os.ReadFile(p)
					if readErr != nil {
						return nil
					}
					files[cr.Name+"/"+filepath.ToSlash(rel)] = data
					return nil
				})
				continue
			}
		}
		data, err := os.ReadFile(localPath)
		if err != nil {
			continue
		}
		files[cr.Name] = data
	}
	manifestData, _ := yaml.Marshal(manifest)
	writeSnapshotDir(releaseDir, releaseID, manifestData, files)
}

// WriteSnapshot writes a local release snapshot from pre-loaded byte slices.
// Used by 'fetch' to bootstrap release history from the current remote state.
// Auto-prune is NOT performed — call PruneLocalSnapshots explicitly when desired.
// Non-fatal: errors are silently ignored.
func WriteSnapshot(releaseDir, releaseID string, manifestRaw []byte, files map[string][]byte) {
	if releaseDir == "" || releaseID == "" {
		return
	}
	writeSnapshotDir(releaseDir, releaseID, manifestRaw, files)
}

// PruneLocalSnapshots deletes the oldest snapshot dirs in releaseDir, keeping the most recent keep.
// Pass keep=0 to use the default of 5.
func PruneLocalSnapshots(releaseDir string, keep int) {
	if keep <= 0 {
		keep = 5
	}
	pruneSnapshots(releaseDir, keep)
}

func writeSnapshotDir(releaseDir, releaseID string, manifestData []byte, files map[string][]byte) {
	dir := filepath.Join(releaseDir, releaseID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	if len(manifestData) > 0 {
		_ = os.WriteFile(filepath.Join(dir, ".regis-release"), manifestData, 0644)
	}
	for name, data := range files {
		dest := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			continue
		}
		_ = os.WriteFile(dest, data, 0644)
	}
}

// cueSnapshotPath returns the local source file to include in a release snapshot.
// render → LocalDest; binary/config/secret → Src[0]; others → empty (no local file).
func cueSnapshotPath(cr config.CueRef) string {
	switch cr.Nature {
	case "render":
		return cr.LocalDest
	case "binary", "config", "secret":
		if len(cr.Src) > 0 {
			return cr.Src[0]
		}
	}
	return ""
}

// pruneSnapshots deletes the oldest snapshot dirs in releaseDir, keeping the most recent keep.
// Dirs named vYYYYMMDD-HHMMSS sort lexicographically = chronologically, so oldest = first.
func pruneSnapshots(releaseDir string, keep int) {
	entries, err := os.ReadDir(releaseDir)
	if err != nil || len(entries) <= keep {
		return
	}
	for _, e := range entries[:len(entries)-keep] {
		if e.IsDir() {
			_ = os.RemoveAll(filepath.Join(releaseDir, e.Name()))
		}
	}
}
