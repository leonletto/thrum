package worktree

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

// Destroy removes a git worktree and (optionally) deletes its
// branch. See spec §3.2 for the full contract.
//
// Idempotent: returns nil when WorktreePath does not exist.
// Branch deletion is best-effort: logs failure but does not
// return an error.
func Destroy(ctx context.Context, opts DestroyOpts) error {
	if opts.RepoPath == "" || opts.WorktreePath == "" {
		return fmt.Errorf("%w: RepoPath and WorktreePath required",
			ErrInvalidOpts)
	}

	// Idempotency pre-check.
	if _, err := os.Stat(opts.WorktreePath); os.IsNotExist(err) {
		slog.Info("worktree.Destroy done (idempotent skip — path absent)",
			slog.String("path", opts.WorktreePath))
		return nil
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
		return fmt.Errorf("git worktree remove: %s: %w", out, err)
	}

	branchDeleted := false
	if opts.Branch != "" {
		if out, err := safecmd.Git(ctx, opts.RepoPath,
			"branch", "-D", opts.Branch); err != nil {
			slog.Warn("worktree.Destroy: branch delete failed (best-effort)",
				slog.String("branch", opts.Branch),
				slog.String("output", string(out)),
				slog.String("error", err.Error()))
			// Non-fatal per spec §3.2.
		} else {
			branchDeleted = true
		}
	}

	// Spec §3.6: slog.Info at success.
	slog.Info("worktree.Destroy done",
		slog.String("path", opts.WorktreePath),
		slog.String("branch", opts.Branch),
		slog.Bool("branch_deleted", branchDeleted))
	return nil
}
