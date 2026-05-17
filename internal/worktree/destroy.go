package worktree

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

// Destroy removes a git worktree and (optionally) deletes its
// branch. See spec §3.2 for the full contract.
//
// Idempotent: returns (&DestroyResult{}, nil) when WorktreePath does
// not exist. Branch deletion is best-effort: logs failure but does
// not return an error; DestroyResult.BranchDeleted exposes the
// outcome so callers can avoid claiming success on best-effort
// failure.
//
// Path safety: WorktreePath must not contain `..` segments and must
// not coincide with RepoPath. The cobra wrapper validates a separate
// `name` arg, but daemon callers (B-B1 scheduler) reach this API
// directly with composed paths; the check defends the API surface
// itself.
func Destroy(ctx context.Context, opts DestroyOpts) (*DestroyResult, error) {
	if opts.RepoPath == "" || opts.WorktreePath == "" {
		return nil, fmt.Errorf("%w: RepoPath and WorktreePath required",
			ErrInvalidOpts)
	}

	// Inspect the ORIGINAL path (pre-cleaning) — `filepath.Clean`
	// resolves `/a/../b` to `/b`, masking caller-supplied escape
	// attempts. The threat is a caller composing a path with `..`
	// segments to escape an intended base directory; the cleaned
	// form is what gets passed to git but the original is what
	// betrays the caller's intent.
	slash := filepath.ToSlash(opts.WorktreePath)
	if slash == ".." || strings.HasPrefix(slash, "../") ||
		strings.HasSuffix(slash, "/..") ||
		strings.Contains(slash, "/../") {
		return nil, fmt.Errorf("%w: WorktreePath %q contains parent-reference segments",
			ErrInvalidOpts, opts.WorktreePath)
	}
	resolvedWT, _ := filepath.Abs(opts.WorktreePath)
	resolvedRepo, _ := filepath.Abs(opts.RepoPath)
	if resolvedWT != "" && resolvedWT == resolvedRepo {
		return nil, fmt.Errorf("%w: WorktreePath %q resolves to RepoPath",
			ErrInvalidOpts, opts.WorktreePath)
	}

	// Idempotency pre-check.
	if _, err := os.Stat(opts.WorktreePath); os.IsNotExist(err) {
		slog.Info("worktree.Destroy done (idempotent skip — path absent)",
			slog.String("path", opts.WorktreePath))
		return &DestroyResult{}, nil
	}

	// Spec §3.6: slog.Info at entry.
	slog.Info("worktree.Destroy beginning",
		slog.String("path", opts.WorktreePath),
		slog.String("branch", opts.Branch),
		slog.Bool("force", opts.Force))

	args := []string{"worktree", "remove"}
	if opts.Force {
		args = append(args, "--force")
	}
	args = append(args, opts.WorktreePath)

	if out, err := safecmd.Git(ctx, opts.RepoPath, args...); err != nil {
		return nil, fmt.Errorf("git worktree remove: %s: %w", out, err)
	}

	result := &DestroyResult{}
	if opts.Branch != "" {
		if out, err := safecmd.Git(ctx, opts.RepoPath,
			"branch", "-D", opts.Branch); err != nil {
			slog.Warn("worktree.Destroy: branch delete failed (best-effort)",
				slog.String("branch", opts.Branch),
				slog.String("output", string(out)),
				slog.String("error", err.Error()))
			// Non-fatal per spec §3.2.
		} else {
			result.BranchDeleted = true
		}
	}

	// Spec §3.6: slog.Info at success.
	slog.Info("worktree.Destroy done",
		slog.String("path", opts.WorktreePath),
		slog.String("branch", opts.Branch),
		slog.Bool("branch_deleted", result.BranchDeleted))
	return result, nil
}
