package cli

import (
	stdcontext "context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/sync"
)

// ErrNotGitRepo is returned by IsGitWorktree when the target path is not
// inside a git repository at all. Exported as a typed sentinel so callers
// (e.g. the hint system's LiveStateAccessor) can use errors.Is instead of
// matching on the error message string.
var ErrNotGitRepo = errors.New("not a git repository")

// InitOptions contains options for initializing a Thrum repository.
type InitOptions struct {
	RepoPath string
	Force    bool
	Stealth  bool // Use .git/info/exclude instead of .gitignore

	// Yes skips interactive confirmation prompts (e.g. the v0.10.x →
	// v0.11 upgrade prompt that asks whether to add !.thrum/skills/ to
	// an existing .gitignore). Equivalent to --yes / --no-interactive.
	Yes bool

	// Prompter, when non-nil, drives interactive confirmations during
	// init steps that materialize changes to tracked files (notably the
	// skills-substrate .gitignore upgrade per spec §10.2). When nil and
	// Yes is false, those steps default to auto-apply — matching the
	// AC's non-interactive default behavior.
	Prompter Prompter
}

// SyncReconciliation describes how Init should set up the sync branch and
// config, based on the current state of refs/heads/a-sync and
// refs/remotes/origin/a-sync. Produced by reconcileSyncBranch per the matrix
// in dev-docs/specs/2026-04-17-thrum-init-attach-remote-a-sync-design.md.
type SyncReconciliation struct {
	// AttachToRemoteSHA, if non-empty, is the remote-tracking SHA that local
	// refs/heads/a-sync should be pointed at. For rows 5 and 7 (local already
	// exists, caller must overwrite), Init applies this via
	// BranchManager.AttachToRemote after CreateSyncBranch's early-return.
	// For row 3 (no local yet), CreateSyncBranch will attach on its own; the
	// field is still set so Init knows to flip LocalOnly.
	AttachToRemoteSHA string
	// LocalOnlyOverride, if non-nil, is the value to stamp into
	// config.Daemon.LocalOnly. nil means "leave whatever the caller's default
	// was" (current behavior for rows 2 and 4).
	LocalOnlyOverride *bool
}

// boolPtr returns a pointer to b, used for building LocalOnlyOverride.
func boolPtr(b bool) *bool { return &b }

// IsGitWorktree checks if repoPath is a git worktree (not the main working tree).
// Returns (isWorktree, mainRepoRoot, error).
func IsGitWorktree(repoPath string) (bool, string, error) {
	ctx := stdcontext.Background()

	// Get the repo toplevel (current working tree root)
	topLevelOut, err := safecmd.Git(ctx, repoPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return false, "", ErrNotGitRepo
	}
	topLevel := strings.TrimSpace(string(topLevelOut))

	// Get the common git dir (shared across all worktrees)
	commonDirOut, err := safecmd.Git(ctx, repoPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return false, "", nil //nolint:nilerr // can't determine, assume not a worktree
	}
	commonDir := strings.TrimSpace(string(commonDirOut))

	// Make commonDir absolute if relative
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(topLevel, commonDir)
	}
	commonDir = filepath.Clean(commonDir)

	// Get the git dir for this working tree
	gitDirOut, err := safecmd.Git(ctx, repoPath, "rev-parse", "--git-dir")
	if err != nil {
		return false, "", nil //nolint:nilerr // can't determine, assume not a worktree
	}
	gitDir := strings.TrimSpace(string(gitDirOut))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(topLevel, gitDir)
	}
	gitDir = filepath.Clean(gitDir)

	// If git-dir != git-common-dir, this is a worktree
	if gitDir != commonDir {
		// Main repo root is the parent of the common git dir (e.g., /repo/.git -> /repo)
		mainRoot := filepath.Dir(commonDir)
		return true, mainRoot, nil
	}

	return false, "", nil
}

// Init initializes a Thrum repository.
func Init(opts InitOptions) error {
	thrumDir := filepath.Join(opts.RepoPath, ".thrum")
	varDir := filepath.Join(thrumDir, "var")

	// Check if already initialized
	if !opts.Force {
		if _, err := os.Stat(thrumDir); err == nil {
			return fmt.Errorf(".thrum/ already exists. Use --force to reinitialize")
		}
	}

	// Row 1 short-circuit (spec: 2026-04-17-thrum-init-attach-remote-a-sync-design.md).
	// If --force against an already-sync-configured install (LocalOnly=false),
	// skip all sync-branch work and only refresh identity/strategy files.
	//
	// If config load fails (corrupt or unreadable), we intentionally fall
	// through to the full init flow — it will overwrite a corrupt config on
	// its way through, which is the correct recovery path.
	if opts.Force {
		if existing, loadErr := config.LoadThrumConfig(thrumDir); loadErr == nil && !existing.Daemon.LocalOnly {
			return reinitIdentityOnly(opts)
		}
	}

	// Track whether .thrum/ existed before we started, so we can clean up on failure
	thrumDirExisted := true
	if _, err := os.Stat(thrumDir); os.IsNotExist(err) {
		thrumDirExisted = false
	}

	// Use a named return so the deferred cleanup can check for errors
	var retErr error
	defer func() {
		if retErr != nil && !thrumDirExisted {
			cleanupCtx := stdcontext.Background()
			// Clean up worktree metadata first
			if syncDir, syncErr := paths.SyncWorktreePath(opts.RepoPath); syncErr == nil {
				_, _ = safecmd.Git(cleanupCtx, opts.RepoPath, "worktree", "remove", "--force", syncDir)
			}

			// Clean up orphan branch ref
			_, _ = safecmd.Git(cleanupCtx, opts.RepoPath, "update-ref", "-d", "refs/heads/a-sync")

			// Remove the .thrum/ directory
			_ = os.RemoveAll(thrumDir)
		}
	}()

	// 1. Create .thrum/ directory
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		retErr = fmt.Errorf("failed to create .thrum/: %w", err)
		return retErr
	}

	// 2. Create .thrum/var/ directory
	if err := os.MkdirAll(varDir, 0750); err != nil {
		retErr = fmt.Errorf("failed to create .thrum/var/: %w", err)
		return retErr
	}

	// 2b. Create .thrum/identities/ directory
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		retErr = fmt.Errorf("failed to create .thrum/identities/: %w", err)
		return retErr
	}

	// 2c. Write strategy/reference files to .thrum/ (strategies/*.md + llms.txt)
	if err := context.WriteStrategies(thrumDir); err != nil {
		retErr = fmt.Errorf("failed to write strategies: %w", err)
		return retErr
	}

	// 3. Create .thrum/schema_version with "1"
	schemaVersionPath := filepath.Join(thrumDir, "schema_version")
	if err := os.WriteFile(schemaVersionPath, []byte("1\n"), 0600); err != nil {
		retErr = fmt.Errorf("failed to create schema_version: %w", err)
		return retErr
	}

	// 4. Add exclusions (.gitignore or .git/info/exclude in stealth mode)
	if opts.Stealth {
		if err := updateGitExclude(opts.RepoPath); err != nil {
			retErr = fmt.Errorf("failed to update .git/info/exclude: %w", err)
			return retErr
		}
	} else {
		if err := updateGitignore(opts.RepoPath); err != nil {
			retErr = fmt.Errorf("failed to update .gitignore: %w", err)
			return retErr
		}
	}

	// 5. Run sync-branch reconciliation matrix
	// (spec: 2026-04-17-thrum-init-attach-remote-a-sync-design.md rows 2–8).
	recon, reconErr := reconcileSyncBranch(stdcontext.Background(), opts.RepoPath)
	if reconErr != nil {
		retErr = reconErr
		return retErr
	}

	// 6. Write default config.json (local-only by default — user must opt in to
	// remote sync). The reconciliation result may flip LocalOnly to false when
	// an existing origin/a-sync is detected.
	configPath := filepath.Join(thrumDir, "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		localOnly := true
		if recon.LocalOnlyOverride != nil {
			localOnly = *recon.LocalOnlyOverride
		}
		cfg := &config.ThrumConfig{
			Daemon: config.DaemonConfig{
				LocalOnly:    localOnly,
				SyncInterval: config.DefaultSyncInterval,
				WSPort:       config.DefaultWSPort,
			},
		}
		if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
			retErr = fmt.Errorf("failed to write config.json: %w", err)
			return retErr
		}
	} else if recon.LocalOnlyOverride != nil {
		// --force reinit path: existing config.json; flip LocalOnly per matrix.
		existing, err := config.LoadThrumConfig(thrumDir)
		if err != nil {
			retErr = fmt.Errorf("load config for override: %w", err)
			return retErr
		}
		existing.Daemon.LocalOnly = *recon.LocalOnlyOverride
		if err := config.SaveThrumConfig(thrumDir, existing); err != nil {
			retErr = fmt.Errorf("save config with override: %w", err)
			return retErr
		}
	}

	// 6b. Populate identity block (daemon_id, repo metadata) in config.json.
	// Bootstrap is idempotent: re-init keeps the existing daemon_id.
	if _, err := identity.Bootstrap(thrumDir, opts.RepoPath); err != nil {
		retErr = fmt.Errorf("failed to bootstrap identity: %w", err)
		return retErr
	}

	// 7. Initialize a-sync branch (applying attach directive from reconciliation)
	if err := initASyncBranch(opts.RepoPath, recon); err != nil {
		retErr = fmt.Errorf("failed to initialize a-sync branch: %w", err)
		return retErr
	}

	// 8. Skills substrate bootstrap (C-B1 E8.3): .thrum/skills/.gitkeep,
	// .gitignore negation, skills.pending_reminder_after default. Runs
	// after config.json materializes (step 6) so applySkillsConfigDefaults
	// has something to load + merge.
	if err := applySkillsBootstrap(opts); err != nil {
		retErr = fmt.Errorf("failed to bootstrap skills substrate: %w", err)
		return retErr
	}

	// Note: Daemon start will be implemented when daemon is ready (Epic 2)

	return nil
}

// thrumExcludeEntries are the patterns thrum needs excluded from git tracking.
var thrumExcludeEntries = []string{
	".thrum/",
	".thrum.*.json",
	"scripts/thrum-startup.sh",
	"scripts/thrum-check-inbox.sh",
}

// updateGitignore adds Thrum-related entries to .gitignore.
func updateGitignore(repoPath string) error {
	gitignorePath := filepath.Join(repoPath, ".gitignore")

	// Entries to add
	entries := append([]string{
		"# Thrum data directory (all data lives on a-sync branch via worktree)",
	}, thrumExcludeEntries...)

	// Read existing .gitignore if it exists
	var existing []byte
	var err error
	if _, statErr := os.Stat(gitignorePath); statErr == nil {
		existing, err = os.ReadFile(gitignorePath) // #nosec G304 -- gitignorePath is <repoPath>/.gitignore, derived from the CLI-provided repo root
		if err != nil {
			return err
		}
	}

	existingStr := string(existing)

	// Check if entries already exist (line-by-line to avoid substring false positives)
	needsUpdate := false
	existingLines := strings.Split(existingStr, "\n")
	for _, entry := range entries {
		// Skip comment line when checking
		if strings.HasPrefix(entry, "#") {
			continue
		}
		found := false
		for _, line := range existingLines {
			if strings.TrimSpace(line) == entry {
				found = true
				break
			}
		}
		if !found {
			needsUpdate = true
			break
		}
	}

	if !needsUpdate {
		return nil
	}

	// Append entries
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) // #nosec G304 -- gitignorePath is <repoPath>/.gitignore, derived from the CLI-provided repo root
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Add newline if file doesn't end with one
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	// Add a blank line before our section if file has content
	if len(existing) > 0 {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	// Write entries
	for _, entry := range entries {
		if _, err := f.WriteString(entry + "\n"); err != nil {
			return err
		}
	}

	return nil
}

// updateGitExclude adds Thrum-related entries to .git/info/exclude (stealth mode).
// This avoids any footprint in tracked files like .gitignore.
func updateGitExclude(repoPath string) error {
	// Resolve the git dir (handles worktrees correctly)
	out, err := safecmd.Git(stdcontext.Background(), repoPath, "rev-parse", "--git-dir")
	if err != nil {
		return fmt.Errorf("resolve git dir: %w", err)
	}
	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoPath, gitDir)
	}

	infoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(infoDir, 0750); err != nil {
		return fmt.Errorf("create .git/info/: %w", err)
	}

	excludePath := filepath.Join(infoDir, "exclude")

	entries := append([]string{
		"# Thrum stealth mode (added by thrum init --stealth)",
	}, thrumExcludeEntries...)

	// Read existing exclude file
	var existing []byte
	if _, statErr := os.Stat(excludePath); statErr == nil {
		existing, err = os.ReadFile(excludePath) // #nosec G304 -- excludePath is .git/info/exclude, a known git internal path
		if err != nil {
			return err
		}
	}

	// Check if entries already present
	existingLines := strings.Split(string(existing), "\n")
	needsUpdate := false
	for _, entry := range entries {
		if strings.HasPrefix(entry, "#") {
			continue
		}
		found := false
		for _, line := range existingLines {
			if strings.TrimSpace(line) == entry {
				found = true
				break
			}
		}
		if !found {
			needsUpdate = true
			break
		}
	}

	if !needsUpdate {
		return nil
	}

	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) // #nosec G304 -- excludePath is .git/info/exclude, a known git internal path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	if len(existing) > 0 {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	for _, entry := range entries {
		if _, err := f.WriteString(entry + "\n"); err != nil {
			return err
		}
	}

	return nil
}

// reinitIdentityOnly refreshes identity and strategy files without touching
// the sync branch or config. Used for row 1 of the behavior matrix (an
// already-sync-configured install where the user is running `thrum init
// --force` just to reset identities).
func reinitIdentityOnly(opts InitOptions) error {
	thrumDir := filepath.Join(opts.RepoPath, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		return fmt.Errorf("mkdir identities: %w", err)
	}
	if err := context.WriteStrategies(thrumDir); err != nil {
		return fmt.Errorf("write strategies: %w", err)
	}
	if _, err := identity.Bootstrap(thrumDir, opts.RepoPath); err != nil {
		return fmt.Errorf("bootstrap identity: %w", err)
	}
	// Skills bootstrap also runs on the row-1 short-circuit so a
	// v0.10.x → v0.11 upgrade via `thrum init --force` on a sync-
	// configured install picks up the .gitignore negation +
	// skills.pending_reminder_after default. Idempotent on repeat.
	if err := applySkillsBootstrap(opts); err != nil {
		return fmt.Errorf("bootstrap skills substrate: %w", err)
	}
	return nil
}

// initASyncBranch creates the a-sync branch and worktree for message
// synchronization. The recon argument (from reconcileSyncBranch) may direct
// this function to attach an already-existing local a-sync to a remote SHA
// (matrix rows 5 and 7 in the design spec).
func initASyncBranch(repoPath string, recon SyncReconciliation) error {
	ctx := stdcontext.Background()
	bm := sync.NewBranchManager(repoPath, true)

	// Create orphan or attach a-sync (rows 2 and 3 of the matrix are handled
	// here via CreateSyncBranch's internal logic).
	if err := bm.CreateSyncBranch(ctx); err != nil {
		return fmt.Errorf("create sync branch: %w", err)
	}

	// Rows 5 and 7: local a-sync already existed before Init; CreateSyncBranch
	// short-circuited. Apply the attach directive now to repoint local at the
	// remote SHA.
	if recon.AttachToRemoteSHA != "" && bm.BranchExists(ctx, sync.SyncBranchName) {
		currentSHA, err := bm.GetSyncBranchRef(ctx)
		if err != nil {
			return fmt.Errorf("read current a-sync ref: %w", err)
		}
		if currentSHA != recon.AttachToRemoteSHA {
			if err := bm.AttachToRemote(ctx, recon.AttachToRemoteSHA); err != nil {
				return fmt.Errorf("attach to remote: %w", err)
			}
		}
	}

	// If a pre-existing sync worktree's working-tree content has diverged from
	// refs/heads/a-sync (e.g. the branch was just attached to a remote SHA, or
	// was updated externally between inits), force-reset it so the subsequent
	// `git add .` + commit hits the "nothing to commit" path instead of
	// staging stale content and creating an unwanted commit on top of the
	// current tip. No-op when the worktree doesn't exist yet or already
	// matches the branch tip.
	//
	// Reset errors are propagated because silent failure here would
	// re-introduce the exact bug this change fixes: a commit added on top
	// of the attached SHA, producing a disjoint history that can't push.
	if syncDir, pathErr := paths.SyncWorktreePath(repoPath); pathErr == nil {
		if _, statErr := os.Stat(syncDir); statErr == nil {
			if _, err := safecmd.Git(ctx, syncDir, "reset", "--hard", "refs/heads/"+sync.SyncBranchName); err != nil {
				return fmt.Errorf("reset sync worktree to current a-sync tip: %w", err)
			}
		}
	}

	// Create worktree at .git/thrum-sync/a-sync
	syncDir, err := paths.SyncWorktreePath(repoPath)
	if err != nil {
		return fmt.Errorf("resolve sync worktree path: %w", err)
	}
	if err := bm.CreateSyncWorktree(ctx, syncDir); err != nil {
		return fmt.Errorf("create sync worktree: %w", err)
	}

	// Create initial data files in the worktree
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); os.IsNotExist(err) {
		if err := os.WriteFile(eventsPath, []byte{}, 0600); err != nil {
			return fmt.Errorf("create events.jsonl: %w", err)
		}
	}

	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0750); err != nil {
		return fmt.Errorf("create messages dir: %w", err)
	}

	// Stage and commit initial files in the worktree
	// (safecmd.Git injects the thrum user.name/user.email overrides automatically)
	if _, err := safecmd.Git(ctx, syncDir, "add", "."); err != nil {
		return fmt.Errorf("git add in sync worktree: %w", err)
	}

	output, err := safecmd.Git(ctx, syncDir, "commit", "--no-verify", "-m", "Initialize Thrum sync data")
	if err != nil {
		outStr := strings.ToLower(string(output))
		// "nothing to commit" is acceptable (idempotent re-init)
		if !strings.Contains(outStr, "nothing to commit") &&
			!strings.Contains(outStr, "nothing added to commit") {
			return fmt.Errorf("git commit in sync worktree: %w\noutput: %s", err, strings.TrimSpace(string(output)))
		}
	}

	return nil
}

// reconcileSyncBranch runs the behavior matrix from
// dev-docs/specs/2026-04-17-thrum-init-attach-remote-a-sync-design.md for
// matrix rows 2–8. Row 1 (skip branch work when already syncing) is handled
// by Init itself before this function is called. This is pure decision logic —
// does not mutate refs or config.
// Returns an error only for row 8 (both local and remote have content).
//
// Invariant: rows 4–8 (local a-sync pre-exists) are only reachable with
// --force, because Init's earlier `.thrum/` stat guard rejects a non-force
// reinit against an existing install. If that guard is ever removed, this
// function will need an explicit opts.Force check before the localExists
// branches to preserve the spec's promise.
func reconcileSyncBranch(ctx stdcontext.Context, repoPath string) (SyncReconciliation, error) {
	bm := sync.NewBranchManager(repoPath, true)
	localExists := bm.BranchExists(ctx, sync.SyncBranchName)
	remoteSHA, remoteExists := bm.RemoteTrackingSyncSHA(ctx)

	switch {
	case !localExists && !remoteExists:
		// Row 2: fresh, no remote. CreateSyncBranch will create an orphan. No override.
		return SyncReconciliation{}, nil

	case !localExists && remoteExists:
		// Row 3: fresh, remote present. CreateSyncBranch will attach; flip LocalOnly.
		return SyncReconciliation{
			AttachToRemoteSHA: remoteSHA,
			LocalOnlyOverride: boolPtr(false),
		}, nil

	case localExists && !remoteExists:
		// Row 4: keep local as-is, no remote, no override.
		return SyncReconciliation{}, nil

	case localExists && remoteExists:
		localHasContent, err := bm.BranchHasContent(ctx, "refs/heads/"+sync.SyncBranchName)
		if err != nil {
			return SyncReconciliation{}, fmt.Errorf("check local a-sync content: %w", err)
		}
		remoteHasContent, err := bm.BranchHasContent(ctx, "refs/remotes/origin/"+sync.SyncBranchName)
		if err != nil {
			return SyncReconciliation{}, fmt.Errorf("check remote a-sync content: %w", err)
		}
		switch {
		case localHasContent && remoteHasContent:
			// Row 8: conflict — refuse init with recovery commands.
			return SyncReconciliation{}, row8ConflictError(ctx, repoPath)
		case !localHasContent && remoteHasContent:
			// Row 5: local is empty placeholder, remote has real data — attach.
			return SyncReconciliation{
				AttachToRemoteSHA: remoteSHA,
				LocalOnlyOverride: boolPtr(false),
			}, nil
		case localHasContent && !remoteHasContent:
			// Row 6: local has real data, remote empty — keep local, flip LocalOnly.
			return SyncReconciliation{
				LocalOnlyOverride: boolPtr(false),
			}, nil
		default:
			// Row 7: both empty — attach to remote (equivalent histories).
			return SyncReconciliation{
				AttachToRemoteSHA: remoteSHA,
				LocalOnlyOverride: boolPtr(false),
			}, nil
		}
	}

	return SyncReconciliation{}, nil
}

// row8ConflictError builds the row-8 error with recovery commands filled in.
// Includes the remote URL (raw, so it works for HTTPS/SSH/file:// remotes)
// if one is configured; otherwise omits that line.
func row8ConflictError(ctx stdcontext.Context, repoPath string) error {
	var remoteLine string
	if out, err := safecmd.Git(ctx, repoPath, "config", "--get", "remote.origin.url"); err == nil {
		if url := strings.TrimSpace(string(out)); url != "" {
			remoteLine = fmt.Sprintf("  Remote origin: %s (a-sync branch)\n", url)
		}
	}
	return fmt.Errorf(`local a-sync and origin/a-sync both contain data — cannot safely reconcile without manual intervention.

Inspect each side before choosing:
%s  Local:  .git/thrum-sync/a-sync/events.jsonl  (after one-time checkout)

Then run ONE of the following, based on which side is authoritative:

  # Keep local history (force-pushes over remote):
  git push --force-with-lease origin a-sync

  # Keep remote history (discards local a-sync, then re-run init):
  git update-ref refs/heads/a-sync refs/remotes/origin/a-sync
  thrum init --force

A future 'thrum doctor --fix' will automate this (tracked as thrum-uvpp.1)`, remoteLine)
}
