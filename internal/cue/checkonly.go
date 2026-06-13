// internal/cue/checkonly.go
package cue

import "context"

type checkOnlyKey struct{}

// WithCheckOnly returns a context that signals executors to compare-only (no upload/exec).
// Used by the pipeline's Stage 1 parallel pre-check (rdiff phase).
func WithCheckOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, checkOnlyKey{}, true)
}

// IsCheckOnly reports whether the context was created with WithCheckOnly.
func IsCheckOnly(ctx context.Context) bool {
	v, _ := ctx.Value(checkOnlyKey{}).(bool)
	return v
}

type updateMtimeKey struct{}

// WithUpdateMtime returns a context that allows executors to update remote mtime
// when a hash comparison finds files equal, even in check-only (rdiff) mode.
func WithUpdateMtime(ctx context.Context) context.Context {
	return context.WithValue(ctx, updateMtimeKey{}, true)
}

// IsUpdateMtime reports whether remote mtime should be updated on hash-equal results.
func IsUpdateMtime(ctx context.Context) bool {
	v, _ := ctx.Value(updateMtimeKey{}).(bool)
	return v
}
