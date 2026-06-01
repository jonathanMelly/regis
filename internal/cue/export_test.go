// internal/cue/export_test.go
// Exports internal functions and types for white-box testing in the cue_test package.
package cue

import (
	"time"

	"git.disroot.org/jmy/regis/internal/config"
)

// PackCandidate is the exported alias of the internal packCandidate type.
type PackCandidate = packCandidate

// PackCandidateWith constructs a PackCandidate for tests.
func PackCandidateWith(rel string) PackCandidate {
	return packCandidate{rel: rel, mtime: time.Time{}}
}

// ParseManifestSet wraps the internal parseManifestSet for testing.
func ParseManifestSet(s string) map[string]bool { return parseManifestSet(s) }

// ExtractReleaseIDFromManifest wraps the internal helper for testing.
func ExtractReleaseIDFromManifest(s string) string { return extractReleaseIDFromManifest(s) }

// DestRelativeToTarget wraps the internal helper for testing.
func DestRelativeToTarget(dest string) (string, bool) { return destRelativeToTarget(dest) }

// PackScopeFilter wraps the internal packScopeFilter for testing.
func PackScopeFilter(candidates []PackCandidate, srcs config.StringOrList) []PackCandidate {
	return packScopeFilter(candidates, srcs)
}
