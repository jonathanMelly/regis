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
