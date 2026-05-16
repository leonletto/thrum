package worktree

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/identity"
)

// derivePathAndBranch computes the worktree path and branch name
// per spec §3.4's naming convention table. The BranchOverride
// field, when non-empty, overrides the convention for the branch
// (but not the path). Internal helper exposed for unit testing.
func derivePathAndBranch(opts CreateOpts) (path, branch string) {
	var leaf string
	var defaultBranch string
	if opts.Persistent {
		leaf = opts.AgentName
		defaultBranch = "agent/" + opts.AgentName
	} else {
		ts := strconv.FormatInt(opts.WakeTimestamp, 10)
		leaf = fmt.Sprintf("%s-%s-%s", opts.AgentName, opts.JobID, ts)
		defaultBranch = fmt.Sprintf("agent/%s/job-%s-%s",
			opts.AgentName, opts.JobID, ts)
	}
	path = filepath.Join(opts.BasePath, leaf)
	if opts.BranchOverride != "" {
		branch = opts.BranchOverride
	} else {
		branch = defaultBranch
	}
	return path, branch
}

// validateOpts checks CreateOpts at API entry per spec §3.4.
// Returns ErrInvalidOpts (wrapped with context) on failure.
//
// AgentName validation delegates to identity.ValidateAgentName so
// the agent-name regex is DRY across the project: lowercase
// letters, digits, underscores, hyphens, colons; rejects reserved
// names like 'daemon', 'system', 'thrum', 'all', 'broadcast'.
func validateOpts(opts CreateOpts) error {
	if opts.RepoPath == "" {
		return fmt.Errorf("%w: RepoPath required", ErrInvalidOpts)
	}
	if err := identity.ValidateAgentName(opts.AgentName); err != nil {
		return fmt.Errorf("%w: AgentName: %v", ErrInvalidOpts, err)
	}
	// Defense-in-depth: identity.ValidateAgentName rejects '/'
	// already (only the agentNameRegex character set passes), but
	// an explicit '..' check is cheap and makes the contract
	// self-evident at the call site.
	if strings.Contains(opts.AgentName, "..") {
		return fmt.Errorf("%w: AgentName %q must not contain parent references",
			ErrInvalidOpts, opts.AgentName)
	}
	if !opts.Persistent {
		if opts.JobID == "" {
			return fmt.Errorf("%w: JobID required when Persistent=false",
				ErrInvalidOpts)
		}
		if opts.WakeTimestamp <= 0 {
			return fmt.Errorf("%w: WakeTimestamp must be > 0 when Persistent=false",
				ErrInvalidOpts)
		}
		// ULID alphabet validation: Crockford Base32 excludes the
		// hyphen by construction. Allow alphanumeric + underscore
		// (the ulid package may produce lowercase or uppercase).
		for _, r := range opts.JobID {
			ok := (r >= '0' && r <= '9') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= 'a' && r <= 'z') ||
				r == '_'
			if !ok {
				return fmt.Errorf("%w: JobID %q contains character %q outside ULID alphabet",
					ErrInvalidOpts, opts.JobID, r)
			}
		}
	}
	// Persistent==true skips JobID/WakeTimestamp checks per spec §3.4.

	// Field-length bound for the 255-byte path-cap test (spec §3.4).
	// The constructive leaf is <agent>-<job>-<ts>; cap each
	// contributor so an over-long single field surfaces as
	// ErrInvalidOpts at validateOpts-time rather than waiting for
	// the constructive path check in Create.
	if len(opts.BasePath) > 200 {
		return fmt.Errorf("%w: BasePath length %d exceeds 200-byte working budget",
			ErrInvalidOpts, len(opts.BasePath))
	}
	return nil
}

// testInjectAfterAdd is a package-private test hook. When non-nil,
// it is invoked by Create AFTER git worktree add succeeds but
// BEFORE the ctx.Err() check + EnsureRedirects, with the new
// worktree path. Tests use it to inject fault conditions (e.g.,
// chmod, force a cancel) that exercise the failure-contract
// cleanup paths. Production code MUST keep this nil.
var testInjectAfterAdd func(worktreePath string)

// Create creates (or, for persistent mode, reuses) a git worktree
// configured with thrum/beads redirects and hook scripts. See spec
// §3.1 for the full contract.
//
// BasePath resolution priority (spec §3.4): opts.BasePath →
// config.Worktrees.BasePath → InferBasePath(opts.RepoPath). The
// fallback chain lives inside Create because the daemon scheduler
// (B-B1 E6.1) bypasses cobra; putting it only in the cobra wrapper
// would silently skip operator config for scheduler-driven creates.
//
// Failure contract (spec §3.5): every non-cancellation error path
// after `git worktree add` attempts inline best-effort cleanup
// (`git worktree remove --force` + `git branch -D`). Context
// cancellation post-add is the ONE intentional shortcut (residue
// class #4) — daemon shutdown stays fast and B-B1's Q10 sweep
// handles the orphan.
func Create(ctx context.Context, opts CreateOpts) (*CreateResult, error) {
	if err := validateOpts(opts); err != nil {
		return nil, err
	}

	// Resolve BasePath in the three-tier priority order from spec §3.4.
	if opts.BasePath == "" {
		thrumDir := filepath.Join(opts.RepoPath, ".thrum")
		if cfg, cfgErr := config.LoadThrumConfig(thrumDir); cfgErr == nil &&
			cfg.Worktrees.BasePath != "" {
			opts.BasePath = cfg.Worktrees.BasePath
		}
	}
	if opts.BasePath == "" {
		opts.BasePath = InferBasePath(opts.RepoPath)
	}
	if opts.BasePath == "" {
		return nil, fmt.Errorf("%w: BasePath unresolved (RepoPath=%s)",
			ErrInvalidOpts, opts.RepoPath)
	}
	// Post-resolution BasePath length re-check: validateOpts caps
	// caller-supplied BasePath, but the tier-2 / tier-3 paths
	// (config + InferBasePath) can also blow the 200-byte working
	// budget. Re-checking here keeps the contract symmetric across
	// the three priority tiers.
	if len(opts.BasePath) > 200 {
		return nil, fmt.Errorf("%w: resolved BasePath length %d exceeds 200-byte working budget",
			ErrInvalidOpts, len(opts.BasePath))
	}

	path, branch := derivePathAndBranch(opts)
	mode := "ephemeral"
	if opts.Persistent {
		mode = "persistent"
	}

	// 255-byte path-length guard (spec §3.4). Run BEFORE the entry
	// slog so a too-long path is rejected without a stray log entry
	// claiming the call is proceeding.
	if len(path) > 255 {
		return nil, fmt.Errorf("%w: resulting path %d bytes exceeds 255-byte filesystem limit",
			ErrInvalidOpts, len(path))
	}

	// Spec §3.6: slog.Info at entry with agent, job_id, mode, path.
	slog.Info("worktree.Create beginning",
		slog.String("agent", opts.AgentName),
		slog.String("job_id", opts.JobID),
		slog.String("mode", mode),
		slog.String("path", path))

	baseBranch := opts.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	// Persistent reuse pre-check.
	if opts.Persistent {
		reused, err := persistentReuseCheck(ctx, path, branch)
		if err != nil {
			return nil, err
		}
		if reused {
			slog.Info("worktree.Create done (reused)",
				slog.String("agent", opts.AgentName),
				slog.String("mode", mode),
				slog.String("path", path),
				slog.String("branch", branch),
				slog.Bool("reused", true))
			return &CreateResult{Path: path, Branch: branch, Reused: true}, nil
		}
	} else {
		// Ephemeral mode: path-already-exists is a typed error.
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("%w: %s", ErrPathExists, path)
		}
	}

	// git worktree add -b <branch> <path> <baseBranch>
	if out, err := safecmd.Git(ctx, opts.RepoPath,
		"worktree", "add", "-b", branch, path, baseBranch); err != nil {
		return nil, fmt.Errorf("git worktree add: %s: %w", out, err)
	}

	if testInjectAfterAdd != nil {
		testInjectAfterAdd(path)
	}

	// Best-effort cleanup wrapper for any subsequent error.
	cleanup := func(origErr error) error {
		// Cancellation: skip cleanup per spec §3.7 (residue class #4).
		if errors.Is(origErr, context.Canceled) ||
			errors.Is(origErr, context.DeadlineExceeded) {
			return origErr
		}
		// Best-effort: remove worktree + delete branch.
		_, removeErr := safecmd.Git(context.Background(), opts.RepoPath,
			"worktree", "remove", "--force", path)
		_, branchErr := safecmd.Git(context.Background(), opts.RepoPath,
			"branch", "-D", branch)
		if removeErr != nil || branchErr != nil {
			return fmt.Errorf("%w (residue: worktree=%s branch=%s remove_err=%v branch_err=%v)",
				origErr, path, branch, removeErr, branchErr)
		}
		return origErr
	}

	if err := ctx.Err(); err != nil {
		return nil, cleanup(err)
	}

	// EnsureRedirects on the freshly-created worktree.
	if err := EnsureRedirects(path, opts.RepoPath); err != nil {
		return nil, cleanup(fmt.Errorf("ensure redirects: %w", err))
	}

	slog.Info("worktree.Create done",
		slog.String("agent", opts.AgentName),
		slog.String("job_id", opts.JobID),
		slog.String("mode", mode),
		slog.String("path", path),
		slog.String("branch", branch),
		slog.Bool("reused", false))
	return &CreateResult{Path: path, Branch: branch, Reused: false}, nil
}

// persistentReuseCheck returns (true, nil) when path already exists
// and contains the expected branch (idempotent reuse). Returns
// (false, ErrPersistentBranchMismatch) when path exists with a
// different branch. Returns (false, nil) when path does not exist
// (fresh persistent create proceeds).
func persistentReuseCheck(ctx context.Context, path, branch string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	// Path exists; resolve its current branch via git rev-parse.
	out, err := safecmd.Git(ctx, path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return false, fmt.Errorf("rev-parse existing worktree: %w", err)
	}
	actual := strings.TrimSpace(string(out))
	if actual != branch {
		return false, fmt.Errorf("%w: path=%s expected=%s actual=%s",
			ErrPersistentBranchMismatch, path, branch, actual)
	}
	return true, nil
}
