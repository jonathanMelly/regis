// internal/runner/prefetch.go
// Bulk remote stat+hash prefetch for binary cues — reduces N SSH calls to 1-2 per phase.
package runner

import (
	"context"
	"os"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// bulkPrefetchBinary runs at most two SSH calls before the parallel check phase:
//  1. stat all existing binary remote paths → mtime+size map
//  2. md5sum only those where mtime/size differ from local → hash map
//
// Results are stored in ctx via cue.WithRemoteStats.
// Missing or failing entries are silently skipped — executors fall back gracefully.
func bulkPrefetchBinary(ctx context.Context, conn cue.SSHConn, steps []Step, target config.Target) context.Context {
	if conn == nil || len(steps) == 0 {
		return ctx
	}

	type entry struct {
		remotePath string
		localPath  string
	}
	var entries []entry
	for _, step := range steps {
		cr := step.CueRef
		if cr.Nature != "binary" || len(cr.Src) == 0 {
			continue
		}
		entries = append(entries, entry{
			remotePath: cue.JoinRemotePath(conn, target.Dir, cr.Dest),
			localPath:  cr.Src[0],
		})
	}
	if len(entries) == 0 {
		return ctx
	}

	// Only stat files known to exist (avoids stat errors on new files).
	// If the file-existence set isn't loaded, stat all candidate paths.
	var statPaths []string
	for _, e := range entries {
		if !cue.RemoteFilesKnown(ctx) || cue.RemoteFileExists(ctx, e.remotePath) {
			statPaths = append(statPaths, e.remotePath)
		}
	}

	stats := make(map[string]cue.RemoteStat, len(entries))

	// Mark known-missing files explicitly so executors skip SSH calls for them.
	if cue.RemoteFilesKnown(ctx) {
		for _, e := range entries {
			if !cue.RemoteFileExists(ctx, e.remotePath) {
				stats[e.remotePath] = cue.RemoteStat{Size: -1}
			}
		}
	}

	if len(statPaths) > 0 {
		for path, rs := range cue.BulkStatRemote(conn, statPaths) {
			stats[path] = rs
		}
	}

	// Identify which files need hash comparison (mtime/size differ from local).
	var hashPaths []string
	for _, e := range entries {
		rs, ok := stats[e.remotePath]
		if !ok || rs.Mtime.IsZero() || rs.Size < 0 {
			continue // absent or stat failed — executor handles it
		}
		localFi, err := os.Stat(e.localPath)
		if err != nil {
			continue
		}
		if rs.Mtime.Unix() != localFi.ModTime().Unix() || rs.Size != localFi.Size() {
			hashPaths = append(hashPaths, e.remotePath)
		}
		// mtime+size match → leave rs.Hash empty (executor treats as equal)
	}

	if len(hashPaths) > 0 {
		for path, h := range cue.BulkHashRemote(conn, hashPaths) {
			if rs, ok := stats[path]; ok {
				rs.Hash = h
				stats[path] = rs
			}
		}
	}

	return cue.WithRemoteStats(ctx, stats)
}
