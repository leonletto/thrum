package guard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/process"
	ttmux "github.com/leonletto/thrum/internal/tmux"
)

// Check is the CLI-side entrypoint for Rule #4‴. Every command that
// resolves a daemon client (getClient) calls Check first: the guard
// gets a chance to refuse on ownership drift, and non-PID drift
// (Runtime, PreferredRuntime, TmuxSession, Branch) is reconciled to
// disk along the way. Check does not touch AgentPID — that mutation
// is the sole domain of prime / quickstart / Rule's auto-reclaim via
// WritePID.
//
// The cfg parameter carries the per-guard enforcement matrix; callers
// typically build it as Merge(DefaultConfig(), repoConfig, daemonConfig).
// Logger receives structured warn-mode events; nil is tolerated.
//
// Returns nil on proceed. Returns a *Error on strict-mode refusal.
// Any other error is a wrapped I/O failure.
func Check(ctx context.Context, repoPath string, cfg Config, logger *slog.Logger) error {
	cc, idFile, idPath, err := buildCheckContext(ctx, repoPath, cfg, logger)
	if err != nil {
		return fmt.Errorf("build check context: %w", err)
	}
	if idFile == nil {
		// Pre-quickstart state: no identity file to protect. The
		// ownership check is vacuous; skip drift reconciliation too.
		return nil
	}
	_ = idPath // reserved for future DetectedAgent resolution callers.
	if derr := reconcileDrift(ctx, repoPath, idFile, cc); derr != nil {
		// Drift failures must not mask the ownership check — log
		// and fall through so Rule still runs.
		if logger != nil {
			logger.Warn("drift_reconcile_failed", "err", derr)
		}
	}
	return Rule(cc)
}

// buildCheckContext assembles the CheckContext shared by Rule + the
// companion guards. It walks the caller's process ancestry, resolves
// the closest runtime ancestor, loads the identity file (if any),
// realpath-canonicalizes the CWD vs. The file's worktree, and compares
// TMUX state. The identity file + its disk path are returned so
// reconcileDrift can mutate and re-persist the non-PID fields without
// re-loading.
func buildCheckContext(ctx context.Context, repoPath string, cfg Config, logger *slog.Logger) (*CheckContext, *config.IdentityFile, string, error) {
	self := os.Getpid()
	chain, _ := WalkAncestors(ctx, self)
	rtPID, _, _ := ClosestRuntimeAncestor(ctx, self)

	idFile, relPath, err := config.LoadIdentityWithPath(repoPath)
	switch {
	case err != nil && isNoIdentityFile(err):
		// No file — caller is in the pre-quickstart state; return a
		// minimal CheckContext so the caller can still decide to run
		// Rule (Rule with IdentityAgentPID=0 and ClosestRtPID>0 will
		// fall into step 3.2 and require CWD+TMUX match; in this
		// caller the identity file is absent so we short-circuit in
		// Check instead).
		return &CheckContext{
			Ctx:             ctx,
			Mode:            cfg.CrossWorktree,
			DeadReclaimMode: cfg.DeadPIDAutoReclaim,
			Chain:           chain,
			ClosestRtPID:    rtPID,
			IsPIDAlive:      func(pid int) bool { return process.IsRunning(pid) },
			warnLogger:      logger,
		}, nil, "", nil
	case err != nil:
		return nil, nil, "", err
	}

	effective := paths.EffectiveRepoPath(repoPath)
	idPath := filepath.Join(effective, relPath)
	identitiesDir := filepath.Join(effective, ".thrum", "identities")

	cc := &CheckContext{
		Ctx:              ctx,
		Mode:             cfg.CrossWorktree,
		DeadReclaimMode:  cfg.DeadPIDAutoReclaim,
		Chain:            chain,
		ClosestRtPID:     rtPID,
		IdentityAgentPID: idFile.AgentPID,
		IsPIDAlive:       func(pid int) bool { return process.IsRunning(pid) },
		CWDMatches:       cwdMatches(effective, idFile.Worktree),
		TmuxMatches:      tmuxMatches(idFile.TmuxSession),
		IdentitiesDir:    identitiesDir,
		ExpectedAgent:    idFile.Agent.Name,
		warnLogger:       logger,
	}
	return cc, idFile, idPath, nil
}

// cwdMatches returns true when the caller's realpath'd effective repo
// path and the identity file's realpath'd worktree resolve to the same
// location. MacOS /var/folders → /private/var/folders aliasing and
// symlink farms both produce string-level drift that would otherwise
// mis-flag legitimate owners (spec §Rule #4‴ MINOR 10).
func cwdMatches(cwd, worktree string) bool {
	if worktree == "" {
		// No recorded worktree → cannot corroborate; be conservative
		// and treat as match, deferring the strict check to Rule's
		// other signals (TMUX, PID-in-chain).
		return true
	}
	resolvedCWD, err1 := filepath.EvalSymlinks(cwd)
	resolvedWT, err2 := filepath.EvalSymlinks(worktree)
	if err1 != nil || err2 != nil {
		// Fall back to cleaned-path comparison when realpath fails
		// (e.g. worktree moved and stale path in file).
		return filepath.Clean(cwd) == filepath.Clean(worktree)
	}
	return resolvedCWD == resolvedWT
}

// tmuxMatches implements the "equal, or absent on both sides"
// convention from CheckContext.TmuxMatches.
func tmuxMatches(stored string) bool {
	live := ""
	if ttmux.InTmux() {
		if t, err := ttmux.PaneTarget(); err == nil {
			live = t
		}
	}
	return live == stored
}

// reconcileDrift persists non-PID field updates (Runtime,
// PreferredRuntime, TmuxSession, Branch) when live state diverges
// from what's on disk. Mirrors the behavior historically in
// RefreshLocalIdentity's Step 3; the PID-write branch has been
// removed because PID writes now route exclusively through WritePID.
//
// Note on write path: reconcileDrift writes through
// config.SaveIdentityFile (atomic at the os.WriteFile level but
// without fcntl serialization), NOT through guard.AtomicWrite. This
// is intentional — these fields (Runtime, PreferredRuntime,
// TmuxSession, Branch) are advisory metadata, not ownership state.
// Two concurrent writes here would merge into whichever arrived last;
// neither produces corruption nor violates an invariant Rule #4‴
// depends on. WritePID, by contrast, does route through AtomicWrite
// because a torn PID write WOULD corrupt ownership.
func reconcileDrift(ctx context.Context, repoPath string, idFile *config.IdentityFile, cc *CheckContext) error {
	detectedRuntime := ""
	if cc.ClosestRtPID > 0 {
		detectedRuntime = runtimeNameFn(ctx, cc.ClosestRtPID)
	}

	tmuxTarget := ""
	if ttmux.InTmux() {
		if target, err := ttmux.PaneTarget(); err == nil {
			tmuxTarget = target
		}
	}

	branch := ""
	effective := paths.EffectiveRepoPath(repoPath)
	if out, err := safecmd.Git(ctx, effective, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = strings.TrimRight(string(out), "\n")
	}

	changed := false
	if detectedRuntime != "" && idFile.Runtime != detectedRuntime {
		idFile.Runtime = detectedRuntime
		changed = true
	}
	if detectedRuntime != "" && idFile.PreferredRuntime != detectedRuntime {
		idFile.PreferredRuntime = detectedRuntime
		changed = true
	}
	if tmuxTarget != "" && idFile.TmuxSession != tmuxTarget {
		idFile.TmuxSession = tmuxTarget
		changed = true
	}
	if branch != "" && idFile.Branch != branch {
		idFile.Branch = branch
		changed = true
	}
	if !changed {
		return nil
	}

	// Persist via SaveIdentityFile — it handles marshaling,
	// versioning, and umask-safe writes. The PID field is NOT
	// modified by this path; callers that need to mutate AgentPID
	// must route through WritePID.
	thrumDir := filepath.Join(effective, ".thrum")
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		return fmt.Errorf("save identity: %w", err)
	}
	return nil
}

// isNoIdentityFile mirrors internal/cli/refresh.go's sentinel check.
// LoadIdentityFromDir signals "no identity file" via either a wrapped
// os.ErrNotExist or an error containing "no identity files found.".
func isNoIdentityFile(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return strings.Contains(err.Error(), "no identity files found")
}

// ParseConfigFromRaw decodes a ThrumConfig.IdentityGuard
// *json.RawMessage into a guard Config. Empty / nil input returns the
// defaults. The intermediate RawMessage exists so internal/config can
// carry the per-guard modes without importing this package (avoiding
// an import cycle with Rule / Error / CheckContext).
func ParseConfigFromRaw(raw *json.RawMessage) (Config, error) {
	out := DefaultConfig()
	if raw == nil || len(*raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(*raw, &out); err != nil {
		return out, fmt.Errorf("unmarshal identity_guard config: %w", err)
	}
	return out, nil
}

// LoadConfigFromDir reads .thrum/config.json under dir and returns
// the merged guard Config. Kept as a thin wrapper around LoadConfig
// for callers that have a repo/worktree path rather than a thrumDir;
// new code should prefer LoadConfig(thrumDir).
func LoadConfigFromDir(dir string) Config {
	return LoadConfig(filepath.Join(dir, ".thrum"))
}
