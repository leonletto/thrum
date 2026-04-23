import { test } from '@playwright/test';

// Coordinator-initiated restart flow (release-test Step 10.1 equivalent).
//
// Gated as test.fixme until thrum-x6e8.5 (tmux-exec helper migration) lands:
// on macOS, ~30-50 respawn-pane calls exhaust the pty pool and any test
// using the current tmux-exec respawn pattern flakes. Un-fixme once the
// helper migration is in place.
//
// Refs: thrum-nu16 (x6e8.6 + x6e8.2), thrum-x6e8.5.
test.fixme('coordinator-initiated restart: create + launch + snapshot + restart', async () => {
  // 1. thrum tmux create impl_x --name impl_x --role implementer --module testing --cwd <scratch-worktree>
  // 2. thrum tmux launch impl_x
  //    Assert identity: tmux_session populated, agent_pid == 0, worktree absolute.
  // 3. Drive a /thrum:prime in the pane.
  //    Assert identity: agent_pid == <runtime PID>.
  // 4. thrum tmux snapshot save (no --jsonl flag)
  //    Assert: succeeds (x6e8.2 symptom gone).
  // 5. thrum tmux restart impl_x
  //    Assert: new pane has the snapshot loaded, agent_pid reclaimed post-restart.
});
