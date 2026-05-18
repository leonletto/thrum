// Package sessionarchive owns the agent-session-archive substrate:
// the .thrum/agents/<agent_id>/sessions/ folder layout, the archive
// move semantics, and the path-resolution helper that routes per
// agent mode.
//
// The canonical contract is dev-docs/specs/2026-05-17-session-archive-design.md:
//
//   - §5.1 / §5.2 — folder layout (persistent ↦ main-repo .thrum/,
//     ephemeral ↦ worktree .thrum/)
//   - §3 — session.archive RPC + daemon-internal invocation
//   - §4 — YAML frontmatter schema (agent, session_id, saved_at,
//     reason, machine_id)
//
// Q-Spec-5 resolution (2026-05-17): SessionsDir is a free function
// that takes both thrumDir roots as arguments and switches on
// agent.Mode internally. The Agent type is data-only in B-B1 E6.0;
// the daemon already carries both `h.thrumDir` (main repo) and a
// per-RPC `wtThrumDir` (worktree) at call sites, so passing them
// explicitly keeps the helper substrate-independent. See
// thrum-6qmf.15.4 commit body for the cross-talk transcript.
package sessionarchive
