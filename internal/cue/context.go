// internal/cue/context.go
package cue

import "context"

type manifestKey struct{}

// Manifest holds release manifest data used during rdiff drift detection.
type Manifest struct {
	Release   string
	DeployedAt string // formatted for display
	DeployedBy string
	Checksums map[string]string
}

// WithManifest returns a context carrying the release manifest for drift detection.
func WithManifest(ctx context.Context, m *Manifest) context.Context {
	return context.WithValue(ctx, manifestKey{}, m)
}

// ManifestFrom returns the manifest stored in ctx, or nil if absent.
func ManifestFrom(ctx context.Context) *Manifest {
	m, _ := ctx.Value(manifestKey{}).(*Manifest)
	return m
}
