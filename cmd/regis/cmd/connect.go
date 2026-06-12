// cmd/regis/cmd/connect.go
package cmd

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
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
// manifest download + populateRemoteFiles + WithLocalDir + WithDebugWriter.
// Returns the updated context and optional ManifestInfo (nil when no manifest found).
func buildBaseCtx(gf *GlobalFlags, conn cue.SSHConn, tgt config.Target, cfg *config.Config) (context.Context, *output.ManifestInfo) {
	ctx := context.Background()
	if gf.Debug {
		ctx = cue.WithDebugWriter(ctx, os.Stderr)
	}

	var minfo *output.ManifestInfo
	if conn != nil {
		if data, dlErr := conn.Download(tgt.Dir + "/.regis-release"); dlErr == nil {
			var m runner.ReleaseManifest
			if parseErr := yaml.Unmarshal(data, &m); parseErr == nil {
				minfo = &output.ManifestInfo{
					Release:    m.Release,
					DeployedAt: m.DeployedAt,
					DeployedBy: m.DeployedBy,
				}
				ctx = cue.WithManifest(ctx, &cue.Manifest{
					Release:    m.Release,
					DeployedBy: m.DeployedBy,
					Hashes:     m.Hashes,
				})
			}
		}
	}

	ctx = populateRemoteFiles(ctx, conn, tgt.Dir)
	ctx = cue.WithLocalDir(ctx, cfg.BaseDir)
	return ctx, minfo
}

// buildDispatch constructs a runner.Dispatch for tgt.
// withReleaseDir=true wires the Pack executor's release dir (run mode only).
func buildDispatch(conn cue.SSHConn, cfg *config.Config, tgt *config.Target, gf *GlobalFlags, withReleaseDir bool) runner.Dispatch {
	env, _ := config.BuildEnvForTarget(cfg, tgt)
	pack := cue.NewPackExecutor(conn)
	if withReleaseDir {
		pack = pack.WithReleaseDir(cfg.Release.Dir, gf.Yes)
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
