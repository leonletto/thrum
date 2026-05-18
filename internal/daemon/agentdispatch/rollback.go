package agentdispatch

import (
	"context"

	"github.com/leonletto/thrum/internal/worktree"
)

// In-wake rollback dispatcher per spec §7.1 rollback table.
//
// | Stage that failed                     | Rollback action                                                              |
// | ------------------------------------- | ---------------------------------------------------------------------------- |
// | 0, 1, 2                               | None; no artifacts created                                                   |
// | 3a                                    | None; thrum-non7 §3.5 guarantees zero residue on non-cancel errors           |
// | 3b — context.Canceled                 | None; defer to E6.9 sweep per thrum-non7 §3.7                                |
// | 3b — ErrNullAdapter                   | N/A (treated as success per C-B1 §12.3.1)                                    |
// | 3b — other errors                     | worktree.Destroy(WorktreePath, Branch, Force: true) inline in handler        |
// | 4                                     | rollbackStage4Failure: worktree.Destroy                                      |
// | 5 / 6                                 | rollbackStage5Failure: tmux kill-session THEN rollbackStage4Failure          |
// | 7 errors that aren't completion paths | Stage 8 teardown runs normally; no inline rollback                           |
//
// All helpers use context.Background() so cleanup completes even when
// the parent context is already cancelled — daemon shutdown shouldn't
// strand a worktree or live tmux session just because the failing
// stage's context happened to be the one that got cancelled. Stage 3b
// non-cancel rollback inlines in handleStage3bMirror because that
// helper already runs the discriminator switch; lifting it here would
// double-document the same call.

// rollbackStage4Failure tears down the stage-3 worktree after a
// stage-4 tmux-create failure. Stage 4 leaves no live tmux session
// behind (TmuxCreate failed), so only worktree.Destroy fires.
func (h *ScheduledAgentHandler) rollbackStage4Failure(result *worktree.CreateResult) {
	_, _ = h.deps.Worktree.Destroy(context.Background(), worktree.DestroyOpts{
		RepoPath:     h.deps.RepoPath,
		WorktreePath: result.Path,
		Branch:       result.Branch,
		Force:        true,
	})
}

// rollbackStage5Failure tears down BOTH the live tmux session AND the
// stage-3 worktree after a stage-5 (tmux launch) or stage-6
// (wait-for-pane-ready) failure. Kill order is fixed: tmux first
// (so the runtime can't continue writing into the doomed worktree
// mid-destroy), then worktree. Reversing this order would surface
// "file in use" errors on the destroy path.
func (h *ScheduledAgentHandler) rollbackStage5Failure(target string, result *worktree.CreateResult) {
	_ = h.deps.Tmux.TmuxKillSession(context.Background(), target)
	h.rollbackStage4Failure(result)
}
