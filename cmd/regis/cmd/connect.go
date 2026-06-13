// cmd/regis/cmd/connect.go
package cmd

import (
	"context"
	"fmt"
	"os"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/output"
	"git.disroot.org/jmy/regis/internal/runner"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
)

// connectTarget dials SSH to tgt, expands ~, wraps with debug logging, and updates
// tgt.Dir to the expanded path. Returns (rawConn, SSHConn).
// On a soft dial failure rawConn and conn are both nil (warning printed to stderr).
// Returns a non-nil error only when ExpandHome fails — caller should treat this as fatal.
func connectTarget(gf *GlobalFlags, tgt *config.Target, spinner *output.Spinner) (*regssh.Conn, cue.SSHConn, error) {
	if gf.Debug {
		port := "22"
		if tgt.Port != "" {
			port = tgt.Port
		}
		fmt.Fprintf(os.Stderr, "[debug] dialing %s@%s:%s\n", tgt.User, tgt.Host, port)
	}
	rawConn, dialErr := regssh.Dial(*tgt)
	if dialErr != nil {
		fmt.Fprintf(os.Stderr, "warn: SSH connect to %s failed: %v\n", tgt.Name, dialErr)
		return nil, nil, nil
	}
	if rawConn == nil {
		return nil, nil, nil
	}
	expanded, err := rawConn.ExpandHome(tgt.Dir)
	if err != nil {
		if spinner != nil {
			spinner.Stop()
		}
		return nil, nil, fmt.Errorf("FAILED %s: %v", tgt.Name, err)
	}
	tgt.Dir = expanded
	conn := WrapDebug(rawConn, gf.Debug)
	return rawConn, conn, nil
}

// buildBaseCtx sets up the common context for a target:
// state download + populateRemoteFiles + WithLocalDir + WithDebugWriter.
// Returns the updated context and optional ManifestInfo (nil when no state found).
// Tries .regis-state (new) then .regis-release (legacy) via LoadRemoteState.
func buildBaseCtx(gf *GlobalFlags, conn cue.SSHConn, tgt config.Target, cfg *config.Config) (context.Context, *output.ManifestInfo) {
	ctx := context.Background()
	if gf.Debug {
		ctx = cue.WithDebugWriter(ctx, os.Stderr)
	}

	var minfo *output.ManifestInfo
	if conn != nil {
		if state, err := runner.LoadRemoteState(conn, tgt.Dir); err == nil {
			minfo = &output.ManifestInfo{
				ID:    state.ID,
				DeployedAt: state.DeployedAt,
				DeployedBy: state.DeployedBy,
			}
			// Build per-cue hash map for binary drift detection (ManifestDrift field).
			// Uses the single-file hash for single-file cues (binary, config, secret, render).
			hashes := make(map[string]string)
			for cueName, cs := range state.Cues {
				if fs, ok := cs.Files[cueName]; ok && fs.Hash != "" {
					hashes[cueName] = fs.Hash
				}
			}
			ctx = cue.WithManifest(ctx, &cue.Manifest{
				ID:    state.ID,
				DeployedBy: state.DeployedBy,
				Hashes:     hashes,
			})
		}
	}

	ctx = populateRemoteFiles(ctx, conn, tgt.Dir)
	ctx = cue.WithLocalDir(ctx, cfg.BaseDir)
	return ctx, minfo
}

// buildDispatch constructs a runner.Dispatch for tgt.
// withStateDir=true wires the Pack executor's state dir (run mode only).
func buildDispatch(conn cue.SSHConn, cfg *config.Config, tgt *config.Target, gf *GlobalFlags, withStateDir bool) runner.Dispatch {
	env, _ := config.BuildEnvForTarget(cfg, tgt)
	pack := cue.NewPackExecutor(conn)
	if withStateDir {
		pack = pack.WithStateDir(cfg.State.Dir, gf.RunWithoutCheck)
	}
	return runner.Dispatch{
		BulkConn: conn,
		Binary:   cue.NewBinaryExecutor(conn),
		Config:   cue.NewConfigExecutor(conn, env),
		Secret:   cue.NewSecretExecutor(conn),
		Action:   cue.NewActionExecutor(conn),
		Generate: cue.NewGenerateExecutor(),
		Render:   cue.NewRenderExecutor(conn),
		Pack:     pack,
		Service:  cue.NewServiceExecutor(conn, env),
	}
}
