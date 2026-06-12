// internal/cue/types.go
package cue

import "time"

// Status is the outcome of one cue execution.
type Status int

const (
	StatusEqual   Status = iota // = remote matches local
	StatusChanged               // ok — cue ran and changed something
	StatusFailed                // FAILED — cue failed
	StatusSkipped               // skipped — if: evaluated false
	StatusRunning               // ... — in progress
)

// Applied returns the display label used after a real deployment.
// StatusChanged shows "deployed" instead of "+".
func (s Status) Applied() string {
	if s == StatusChanged {
		return "deployed"
	}
	return s.String()
}

func (s Status) String() string {
	switch s {
	case StatusEqual:
		return "="
	case StatusChanged:
		return "~"
	case StatusFailed:
		return "FAILED"
	case StatusSkipped:
		return "skipped"
	case StatusRunning:
		return "..."
	}
	return "?"
}

// Result is the outcome of executing one cue.
type Result struct {
	CueName          string
	ScenarioName     string
	ScenarioDesc     string        // human-readable label from scenario.describe
	Nature           string        // binary | config | secret | action | generate | render
	Status           Status
	Size             int64         // bytes uploaded (binary/config/secret)
	Elapsed          time.Duration
	Stdout           string
	Stderr           string
	Diff             string        // unified diff text (config/secret)
	Err              error
	PostActions      []PostAction  // collected if Changed, for dedup phase
	AffectsRelease   bool          // mirrors cue field — used by runner
	IsLocal          bool          // local action — never release-affecting
	LocalPath        string        // local file path (binary cues, for display)
	RemotePath       string        // remote file path (binary cues, for display)
	LocalMtime       time.Time     // mtime of local file (binary cues)
	RemoteMtime      time.Time     // mtime of remote file via Stat (binary cues)
	LocalHash        string        // local hash (binary cues, for display)
	RemoteHash       string        // remote hash (binary cues, for display + manifest verify)
	ManifestDrift    bool          // true when remote hash ≠ manifest hash
	ManifestHash     string        // manifest's expected hash for this cue
	ArtifactPaths    map[string]string // snapshotKey → remote path (pack: cueName/relpath; populated by pack executor)
	LocalArtifacts   map[string]string // snapshotKey → local file path (pack: cueName/relpath; populated by pack executor)
	Warnings         []string          // shown with ⚠ in output for all statuses; used e.g. for git: true uncommitted-state alerts
	FileTotal        int               // total files considered by multi-file cues (pack, render, multi-src config)
	FileChanged      int               // how many of those files differ from remote
	Cmd              string            // human-readable command/path that was or would be executed (for display in exec tab)
}

// IsReleaseAffecting reports whether this result should trigger release creation.
// Matches spec §4.3 "Release-affecting cues".
func (r Result) IsReleaseAffecting() bool {
	if r.Status != StatusChanged {
		return false
	}
	if r.IsLocal {
		return false // local actions never affect release
	}
	switch r.Nature {
	case "binary", "config", "secret", "render":
		return true
	case "action":
		return r.AffectsRelease
	}
	return false
}

// PostAction is a collected post-action command from a cue.
type PostAction struct {
	Cmd  string
	Sudo bool
}
