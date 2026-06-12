// internal/cue/pack.go
// doc:nature pack
// Ships a local file tree to a remote directory.
// src: is an allowlist of glob patterns; .regisignore in the working directory is the denylist.
// git: true uses the current HEAD commit's file tree (git ls-tree -r HEAD) as the source
// instead of src: glob patterns. .regisignore is still applied as a denylist. Requires the
// working directory to be inside a git repository with at least one commit. src: and git: true
// are mutually exclusive; nature: pack is inferred when git: true is set without nature:.
// Each matched file is compared with its remote counterpart (MD5 by default; text diff with diff_mode: text).
// Only changed files are uploaded. prune: true removes remote files absent from the local set.
// Paths are preserved relative to each glob root:
//
//	src: application/**  dest: /var/www/  →  application/img/logo.png  →  /var/www/img/logo.png
//	git: true            dest: /var/www/  →  cmd/main.go               →  /var/www/cmd/main.go
//
// Direction: local → remote. Always release-affecting.
//
// Prune strategy (three tiers, first match wins):
//
//	Tier 1  — managed-file manifest (.regis-pack-<name> in dest or release archive): exact diff, automatic
//	Tier 2  — src-scope filter + mtime: shows candidates; auto-prunes with --yes
//	Tier 3  — informational: lists unmanaged files, no deletion
//
// The managed manifest is always written after a successful deploy so tier 1 is ready on the next run.
// rollback: true — restores all pack files from the local release snapshot. Pack files are
// automatically included in the snapshot when rollback: true is set.
package cue

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
)

// PackExecutor handles nature: pack cues.
type PackExecutor struct {
	conn             SSHConn
	releaseRemoteDir string // for tier 1b/1c; "" = target.dir/.regis-releases
	skipConfirm      bool   // for tier 2: auto-prune without confirmation
}

// NewPackExecutor creates a PackExecutor.
func NewPackExecutor(conn SSHConn) *PackExecutor { return &PackExecutor{conn: conn} }

// WithReleaseDir configures the remote release archive path and skip-confirm flag
// for tier 1b/1c prune lookups. Called by run.go after loading config.
func (e *PackExecutor) WithReleaseDir(dir string, skipConfirm bool) *PackExecutor {
	e.releaseRemoteDir = dir
	e.skipConfirm = skipConfirm
	return e
}

// Execute expands src globs, applies .regisignore, diffs each file, uploads changes,
// then writes the managed-file manifest and runs the three-tier prune if prune: true.
func (e *PackExecutor) Execute(ctx context.Context, conn SSHConn, cr config.CueRef, target config.Target) (Result, error) {
	start := time.Now()
	r := Result{CueName: cr.Name, Nature: "pack", AffectsRelease: true}

	if e.conn == nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("pack %q: no SSH connection available", cr.Name)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	var (
		srcs []resolvedSrc
		err  error
	)
	var gitRef string
	if cr.Git {
		srcs, err = expandSrcFromGit()
		if err != nil {
			r.Status = StatusFailed
			r.Err = fmt.Errorf("pack %q: %w", cr.Name, err)
			r.Elapsed = time.Since(start)
			return r, nil
		}
		gitRef = gitShortHash()
		staged, untracked := gitUncommittedInfo()
		if len(staged) > 0 {
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"%d staged file(s) not committed — will NOT be deployed: %s",
				len(staged), joinFiles(staged, 5)))
		}
		if len(untracked) > 0 {
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"%d untracked file(s) will NOT be deployed: %s",
				len(untracked), joinFiles(untracked, 5)))
		}
	} else {
		srcs, err = expandSrcResolved(cr.Src)
		if err != nil {
			r.Status = StatusFailed
			r.Err = fmt.Errorf("pack %q: expand src: %w", cr.Name, err)
			r.Elapsed = time.Since(start)
			return r, nil
		}
	}

	ignorePatterns, err := loadIgnorePatterns(".")
	if err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("pack %q: load .regisignore: %w", cr.Name, err)
		r.Elapsed = time.Since(start)
		return r, nil
	}
	srcs = applyIgnore(srcs, ignorePatterns)

	if len(srcs) == 0 {
		r.Status = StatusEqual
		r.Elapsed = time.Since(start)
		return r, nil
	}

	remoteDest := JoinRemotePath(e.conn, target.Dir, strings.TrimRight(cr.Dest, "/"))
	sep := e.conn.PathSep()
	dryRun := IsDryRun(ctx)
	useSudo := cr.Sudo || target.Sudo

	diffMode := cr.DiffMode
	if diffMode == "" {
		diffMode = "binary"
	}

	var changedNames []string
	var totalSize int64
	var diffBuf strings.Builder
	localRelPaths := make(map[string]bool, len(srcs))
	progressFn := FileProgressFrom(ctx)
	var scanned int

	for _, sf := range srcs {
		rel := remoteRelPath(sf.path, sf.pattern)
		// Skip .regis-pack-* files (our own managed manifests on the remote).
		if strings.HasPrefix(rel, ".regis-pack-") {
			continue
		}
		localRelPaths[rel] = true
		remoteRel := strings.ReplaceAll(rel, "/", sep)
		remotePath := remoteDest + sep + remoteRel

		localData, err := os.ReadFile(sf.path)
		if err != nil {
			r.Status = StatusFailed
			r.Err = fmt.Errorf("read %s: %w", sf.path, err)
			r.Elapsed = time.Since(start)
			return r, nil
		}

		// Skip the download when we know the file is absent on the target.
		var remoteData []byte
		remoteMissing := RemoteFilesKnown(ctx) && !RemoteFileExists(ctx, remotePath)
		if !remoteMissing {
			remoteData, _ = e.conn.Download(remotePath)
			if remoteData == nil {
				remoteMissing = true
			}
		}

		remoteLabel := func(rmd5 string) string {
			if remoteMissing {
				return "missing"
			}
			return truncateHash(rmd5)
		}

		var fileChanged bool
		switch diffMode {
		case "text":
			remoteStr := "remote:" + rel
			if remoteMissing {
				remoteStr = "(missing)"
			}
			diff, changed := TextDiff(string(localData), string(remoteData), remoteStr, "local:"+rel)
			fileChanged = changed || remoteMissing
			if fileChanged {
				diffBuf.WriteString(diff)
			}
		case "auto":
			if isBinaryContent(localData) || (!remoteMissing && isBinaryContent(remoteData)) {
				lmd5 := localHashBytes(localData)
				rmd5 := localHashBytes(remoteData)
				fileChanged = remoteMissing || lmd5 != rmd5
				if fileChanged {
					fmt.Fprintf(&diffBuf, "binary %s  remote:%s  local:%s\n", rel, remoteLabel(rmd5), truncateHash(lmd5))
				}
			} else {
				remoteStr := "remote:" + rel
				if remoteMissing {
					remoteStr = "(missing)"
				}
				diff, changed := TextDiff(string(localData), string(remoteData), remoteStr, "local:"+rel)
				fileChanged = changed || remoteMissing
				if fileChanged {
					diffBuf.WriteString(diff)
				}
			}
		default: // "binary"
			lmd5 := localHashBytes(localData)
			rmd5 := localHashBytes(remoteData)
			fileChanged = remoteMissing || lmd5 != rmd5
			if fileChanged {
				fmt.Fprintf(&diffBuf, "binary %s  remote:%s  local:%s\n", rel, remoteLabel(rmd5), truncateHash(lmd5))
			}
		}

		scanned++
		if progressFn != nil {
			progressFn(cr.Name, scanned, len(srcs))
		}

		if !fileChanged {
			continue
		}
		changedNames = append(changedNames, rel)

		if dryRun {
			continue
		}

		// Ensure remote parent directory exists.
		if strings.Contains(rel, "/") {
			relDir := rel[:strings.LastIndex(rel, "/")]
			remoteParent := remoteDest + sep + strings.ReplaceAll(relDir, "/", sep)
			mkdirCmd := "mkdir -p " + shellQuote(remoteParent)
			if useSudo {
				e.conn.RunSudo(mkdirCmd)
			} else {
				e.conn.Run(mkdirCmd)
			}
		}

		if err := e.conn.UploadBytes(localData, remotePath, os.FileMode(0644), useSudo); err != nil {
			r.Status = StatusFailed
			r.Err = fmt.Errorf("upload %s: %w", rel, err)
			r.Elapsed = time.Since(start)
			return r, nil
		}
		totalSize += int64(len(localData))
	}

	// Always write the managed-file manifest after a successful non-dry-run deploy so
	// tier-1 prune is available on the next run regardless of whether prune: true today.
	if !dryRun {
		e.writePackManifest(remoteDest, sep, cr.Name, localRelPaths, useSudo)
	}

	// Three-tier prune.
	type pruneResult struct {
		report   string
		affected bool // true if files were actually deleted (drives AffectsRelease)
	}
	var pr pruneResult
	if packPruneEnabled(cr) && !dryRun {
		report, affected := e.runPrune(cr, target, remoteDest, sep, localRelPaths, useSudo)
		pr = pruneResult{report: report, affected: affected}
	}

	// AffectsRelease only when something actually changed or was pruned on the remote.
	r.AffectsRelease = len(changedNames) > 0 || pr.affected

	// StatusChanged only when files were actually uploaded or pruned.
	// Tier-3 informational reports (no actual deletions) are surfaced as stdout
	// but do not flip the status — the remote is still in sync.
	if len(changedNames) == 0 && !pr.affected {
		r.Status = StatusEqual
		var equalStdout strings.Builder
		if gitRef != "" {
			fmt.Fprintf(&equalStdout, "commit %s", gitRef)
		}
		if pr.report != "" {
			if equalStdout.Len() > 0 {
				equalStdout.WriteString("\n")
			}
			equalStdout.WriteString(pr.report)
		}
		r.Stdout = equalStdout.String()
		r.FileTotal = len(localRelPaths)
		r.FileChanged = 0
		r.Elapsed = time.Since(start)
		return r, nil
	}

	r.Status = StatusChanged
	r.Size = totalSize
	r.FileTotal = len(localRelPaths)
	r.FileChanged = len(changedNames)
	r.Diff = strings.TrimRight(diffBuf.String(), "\n")

	var summary strings.Builder
	if gitRef != "" {
		fmt.Fprintf(&summary, "commit %s\n", gitRef)
	}
	if len(changedNames) > 0 {
		fmt.Fprintf(&summary, "%d file(s) changed: %s", len(changedNames), strings.Join(changedNames, ", "))
	}
	if pr.report != "" {
		if summary.Len() > 0 {
			summary.WriteString("\n")
		}
		summary.WriteString(pr.report)
	}
	r.Stdout = strings.TrimRight(summary.String(), "\n")

	if !dryRun && cr.Post.Cmd != "" {
		r.PostActions = []PostAction{{Cmd: cr.Post.Cmd, Sudo: cr.Post.Sudo || target.Sudo}}
	}

	// Populate artifact maps for per-cue rollback support.
	// Covers all pack files (not just changed ones) so the snapshot is complete.
	r.LocalArtifacts = make(map[string]string, len(srcs))
	r.ArtifactPaths = make(map[string]string, len(srcs))
	for _, sf := range srcs {
		rel := remoteRelPath(sf.path, sf.pattern)
		if strings.HasPrefix(rel, ".regis-pack-") {
			continue
		}
		key := cr.Name + "/" + rel
		r.LocalArtifacts[key] = sf.path
		r.ArtifactPaths[key] = remoteDest + sep + strings.ReplaceAll(rel, "/", sep)
	}

	r.Elapsed = time.Since(start)
	return r, nil
}

// writePackManifest writes .regis-pack-<cueName> in remoteDest listing all managed rel paths.
func (e *PackExecutor) writePackManifest(remoteDest, sep, cueName string, localRelPaths map[string]bool, useSudo bool) {
	lines := make([]string, 0, len(localRelPaths))
	for rel := range localRelPaths {
		lines = append(lines, rel)
	}
	sort.Strings(lines)
	content := strings.Join(lines, "\n") + "\n"
	manifestPath := remoteDest + sep + ".regis-pack-" + cueName
	_ = e.conn.UploadBytes([]byte(content), manifestPath, 0644, useSudo)
}

// runPrune implements the three-tier prune strategy.
// Returns (report string, actuallyPruned bool).
func (e *PackExecutor) runPrune(cr config.CueRef, target config.Target, remoteDest, sep string, localRelPaths map[string]bool, useSudo bool) (string, bool) {
	// Tier 1a: managed manifest in the live dest.
	if report, ok := e.pruneTier1a(cr.Name, remoteDest, sep, localRelPaths, useSudo); ok {
		return report, true
	}
	// Tier 1b/1c: managed manifest or file listing from release archive.
	if report, ok := e.pruneTier1bc(cr, target, remoteDest, sep, localRelPaths, useSudo); ok {
		return report, true
	}
	// Tier 2: src-scope heuristic + mtime.
	if report, affected, ok := e.pruneTier2(cr, remoteDest, sep, localRelPaths, useSudo); ok {
		return report, affected
	}
	// Tier 3: informational listing.
	return e.pruneTier3(remoteDest, sep, localRelPaths), false
}

// pruneTier1a reads .regis-pack-<name> from the live dest and prunes stale entries.
// Returns ("", false) when the manifest is absent.
func (e *PackExecutor) pruneTier1a(cueName, remoteDest, sep string, localRelPaths map[string]bool, useSudo bool) (string, bool) {
	manifestPath := remoteDest + sep + ".regis-pack-" + cueName
	out, _, _, err := e.conn.Run("cat " + shellQuote(manifestPath) + " 2>/dev/null")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	previous := parseManifestSet(out)
	if len(previous) == 0 {
		return "", false
	}
	var toPrune []string
	for rel := range previous {
		if !localRelPaths[rel] {
			toPrune = append(toPrune, rel)
		}
	}
	if len(toPrune) == 0 {
		return "", true // manifest present, nothing to prune
	}
	sort.Strings(toPrune)
	pruned := e.doPrune(toPrune, remoteDest, sep, useSudo)
	return fmt.Sprintf("%d file(s) pruned [manifest]: %s", len(pruned), strings.Join(pruned, ", ")), true
}

// pruneTier1bc looks for the managed manifest or a file listing inside the release archive.
func (e *PackExecutor) pruneTier1bc(cr config.CueRef, target config.Target, remoteDest, sep string, localRelPaths map[string]bool, useSudo bool) (string, bool) {
	// Read the current release manifest to learn the previous release ID.
	raw, _, _, err := e.conn.Run("cat " + shellQuote(target.Dir+"/.regis-release") + " 2>/dev/null")
	if err != nil || strings.TrimSpace(raw) == "" {
		return "", false
	}
	prevID := extractReleaseIDFromManifest(raw)
	if prevID == "" {
		return "", false
	}

	releaseDir := e.releaseRemoteDir
	if releaseDir == "" {
		releaseDir = target.Dir + "/.regis-releases"
	}
	archiveBase := releaseDir + "/" + prevID

	destRel, isRel := destRelativeToTarget(cr.Dest)
	if !isRel {
		return "", false // absolute dest: archive layout unknown
	}

	// Tier 1b: .regis-pack-<name> inside archive dest.
	archiveDestDir := archiveBase
	if destRel != "" {
		archiveDestDir = archiveBase + "/" + destRel
	}
	archiveManifest := archiveDestDir + "/.regis-pack-" + cr.Name
	out, _, _, err := e.conn.Run("cat " + shellQuote(archiveManifest) + " 2>/dev/null")
	if err == nil && strings.TrimSpace(out) != "" {
		previous := parseManifestSet(out)
		var toPrune []string
		for rel := range previous {
			if !localRelPaths[rel] {
				toPrune = append(toPrune, rel)
			}
		}
		if len(toPrune) == 0 {
			return "", true
		}
		sort.Strings(toPrune)
		pruned := e.doPrune(toPrune, remoteDest, sep, useSudo)
		return fmt.Sprintf("%d file(s) pruned [archive manifest]: %s", len(pruned), strings.Join(pruned, ", ")), true
	}

	// Tier 1c: file listing from archive dest dir.
	findOut, _, _, err := e.conn.Run(
		"find " + shellQuote(archiveDestDir) + " -type f -not -name '.regis-pack-*' 2>/dev/null")
	if err != nil || strings.TrimSpace(findOut) == "" {
		return "", false
	}
	pfx := archiveDestDir + "/"
	previous := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(findOut), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel := strings.ReplaceAll(strings.TrimPrefix(line, pfx), sep, "/")
		if rel != "" {
			previous[rel] = true
		}
	}
	if len(previous) == 0 {
		return "", false
	}
	var toPrune []string
	for rel := range previous {
		if !localRelPaths[rel] {
			toPrune = append(toPrune, rel)
		}
	}
	if len(toPrune) == 0 {
		return "", true
	}
	sort.Strings(toPrune)
	pruned := e.doPrune(toPrune, remoteDest, sep, useSudo)
	return fmt.Sprintf("%d file(s) pruned [archive listing]: %s", len(pruned), strings.Join(pruned, ", ")), true
}

// pruneTier2 uses GNU find -printf to get file mtimes, then shows/prunes scope-matched candidates.
// ok=true means tier 2 ran (even if no candidates); ok=false means tier 2 is unavailable or
// no scope-filtered candidates exist (caller should fall to tier 3).
func (e *PackExecutor) pruneTier2(cr config.CueRef, remoteDest, sep string, localRelPaths map[string]bool, useSudo bool) (report string, affected bool, ok bool) {
	findOut, _, _, err := e.conn.Run(
		"find " + shellQuote(remoteDest) + " -maxdepth 20 -type f -not -name '.regis-pack-*' -printf '%Ts\\t%P\\n' 2>/dev/null")
	if err != nil || strings.TrimSpace(findOut) == "" {
		return "", false, false
	}

	var all []packCandidate
	for _, line := range strings.Split(strings.TrimSpace(findOut), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		rel := strings.ReplaceAll(parts[1], sep, "/")
		if localRelPaths[rel] {
			continue
		}
		ts, _ := strconv.ParseInt(parts[0], 10, 64)
		all = append(all, packCandidate{rel: rel, mtime: time.Unix(ts, 0)})
	}

	// Scope filter: only reliable for flat patterns (globRoot == "").
	// Patterns with a non-empty glob root strip prefixes on upload, making remote rels
	// ambiguous — those fall through to tier 3.
	scoped := packScopeFilter(all, cr.Src)
	if len(scoped) == 0 {
		return "", false, false
	}

	sort.Slice(scoped, func(i, j int) bool { return scoped[i].rel < scoped[j].rel })

	if e.skipConfirm {
		var rels []string
		for _, c := range scoped {
			rels = append(rels, c.rel)
		}
		pruned := e.doPrune(rels, remoteDest, sep, useSudo)
		return fmt.Sprintf("%d file(s) pruned [scope heuristic, -y]: %s", len(pruned), strings.Join(pruned, ", ")), true, true
	}

	var sb strings.Builder
	sb.WriteString("prune candidates [scope heuristic — use -y to apply]:\n")
	for _, c := range scoped {
		fmt.Fprintf(&sb, "  %s  %s\n", c.rel, c.mtime.UTC().Format("2006-01-02 15:04"))
	}
	return strings.TrimRight(sb.String(), "\n"), false, true
}

// pruneTier3 lists all unmanaged files in the dest as informational output; nothing is deleted.
func (e *PackExecutor) pruneTier3(remoteDest, sep string, localRelPaths map[string]bool) string {
	findOut, _, _, err := e.conn.Run(
		"find " + shellQuote(remoteDest) + " -maxdepth 20 -type f -not -name '.regis-pack-*' 2>/dev/null")
	if err != nil || strings.TrimSpace(findOut) == "" {
		return "prune skipped [no managed manifest — will be ready after first successful deploy]"
	}
	pfx := remoteDest + sep
	var unmanaged []string
	for _, line := range strings.Split(strings.TrimSpace(findOut), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel := strings.ReplaceAll(strings.TrimPrefix(line, pfx), sep, "/")
		if !localRelPaths[rel] {
			unmanaged = append(unmanaged, rel)
		}
	}
	if len(unmanaged) == 0 {
		return ""
	}
	sort.Strings(unmanaged)
	return "prune skipped [no manifest — re-run after first deploy]: unmanaged: " + strings.Join(unmanaged, ", ")
}

// doPrune removes the given rel paths from remoteDest and returns the successfully deleted list.
func (e *PackExecutor) doPrune(rels []string, remoteDest, sep string, useSudo bool) []string {
	var pruned []string
	for _, rel := range rels {
		remotePath := remoteDest + sep + strings.ReplaceAll(rel, "/", sep)
		rmCmd := "rm -f " + shellQuote(remotePath)
		var runErr error
		if useSudo {
			_, _, _, runErr = e.conn.RunSudo(rmCmd)
		} else {
			_, _, _, runErr = e.conn.Run(rmCmd)
		}
		if runErr == nil {
			pruned = append(pruned, rel)
		}
	}
	return pruned
}

// packPruneEnabled reports whether prune is active for a pack cue.
// Pack defaults to true because tier 1 (manifest-based) is always safe;
// tier 2 only deletes with --yes; tier 3 is informational only.
// Set prune: false in YAML to disable entirely.
func packPruneEnabled(cr config.CueRef) bool {
	if cr.Prune != nil {
		return *cr.Prune
	}
	return true // pack default
}

// parseManifestSet parses newline-separated relative paths into a set.
// Skips blank lines and any .regis-pack-* entries.
func parseManifestSet(s string) map[string]bool {
	m := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, ".regis-pack-") {
			m[line] = true
		}
	}
	return m
}

// extractReleaseIDFromManifest extracts the release ID from a YAML manifest string
// by scanning for the "release: " line (avoids a yaml.Unmarshal import).
func extractReleaseIDFromManifest(yamlText string) string {
	for _, line := range strings.Split(yamlText, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "release: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "release: "))
		}
	}
	return ""
}

// destRelativeToTarget reports whether dest is relative (not absolute) and returns
// the cleaned relative portion. Absolute Unix paths and Windows drive paths return false.
func destRelativeToTarget(dest string) (string, bool) {
	dest = strings.TrimRight(dest, "/\\")
	if dest == "" || dest == "." {
		return "", true
	}
	if path.IsAbs(dest) {
		return "", false
	}
	if len(dest) >= 2 && dest[1] == ':' { // Windows drive letter
		return "", false
	}
	return dest, true
}

// gitShortHash returns the short commit hash of HEAD, or "" on error.
func gitShortHash() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitUncommittedInfo returns lists of staged (indexed but not committed) and
// untracked (non-ignored) files in the current working directory.
func gitUncommittedInfo() (staged, untracked []string) {
	if out, err := exec.Command("git", "diff", "--cached", "--name-only").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				staged = append(staged, line)
			}
		}
	}
	if out, err := exec.Command("git", "ls-files", "--others", "--exclude-standard").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				untracked = append(untracked, line)
			}
		}
	}
	return
}

// joinFiles joins up to limit file names; appends "... and N more" when the list is longer.
func joinFiles(files []string, limit int) string {
	if len(files) <= limit {
		return strings.Join(files, ", ")
	}
	return strings.Join(files[:limit], ", ") + fmt.Sprintf(", ... and %d more", len(files)-limit)
}

// expandSrcFromGit runs git ls-tree -r HEAD --name-only and returns the committed file
// list as resolvedSrc entries. pattern is set to "**" so remoteRelPath preserves the
// full relative path (tree mode, no root stripping).
func expandSrcFromGit() ([]resolvedSrc, error) {
	out, err := exec.Command("git", "ls-tree", "-r", "HEAD", "--name-only").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree: not in a git repository or HEAD has no commits")
	}
	var result []resolvedSrc
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		result = append(result, resolvedSrc{path: line, pattern: "**"})
	}
	return result, nil
}

// packCandidate is a remote file that is not in the local managed set.
type packCandidate struct {
	rel   string
	mtime time.Time
}

// packScopeFilter returns candidates whose rel path is matched by at least one
// flat src pattern (globRoot == ""). Patterns with a non-empty glob root are skipped
// because path prefixes are stripped on upload, making remote rels ambiguous.
// Returns nil when no flat patterns exist (caller falls through to tier 3).
func packScopeFilter(candidates []packCandidate, srcs config.StringOrList) []packCandidate {
	var flatPats []string
	for _, s := range srcs {
		if strings.ContainsAny(s, "*?[") && globRoot(s) == "" {
			flatPats = append(flatPats, s)
		}
	}
	if len(flatPats) == 0 {
		return nil
	}
	var out []packCandidate
	for _, c := range candidates {
		for _, pat := range flatPats {
			// Use path.Match (always forward-slash) since remote rel-paths use /.
			if ok, _ := path.Match(pat, c.rel); ok {
				out = append(out, c)
				break
			}
		}
	}
	return out
}
