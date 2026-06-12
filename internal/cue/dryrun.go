// internal/cue/dryrun.go
package cue

import "context"

type dryRunKey struct{}

// WithDryRun returns a context that signals executors to compare-only (no upload/exec).
func WithDryRun(ctx context.Context) context.Context {
	return context.WithValue(ctx, dryRunKey{}, true)
}

// IsDryRun reports whether the context was created with WithDryRun.
func IsDryRun(ctx context.Context) bool {
	v, _ := ctx.Value(dryRunKey{}).(bool)
	return v
}

type updateMtimeKey struct{}

// WithUpdateMtime returns a context that allows executors to update remote mtime
// when a hash comparison finds files equal, even in dry-run (rdiff) mode.
func WithUpdateMtime(ctx context.Context) context.Context {
	return context.WithValue(ctx, updateMtimeKey{}, true)
}

// IsUpdateMtime reports whether remote mtime should be updated on hash-equal results.
func IsUpdateMtime(ctx context.Context) bool {
	v, _ := ctx.Value(updateMtimeKey{}).(bool)
	return v
}
