package cli

// Hint code constants. Each code is a stable dotted slug (command.subcommand.slug)
// used as the machine-readable identifier in Shape B (text) and Shape C (JSON)
// output. Doc comments cite the institutional-memory rule the code encodes so
// reviewers can trace the "why" back to source material.
//
// Keep this file the single source of truth for codes. L3 tests
// (hintcodes_test.go, Task 15) verify format + uniqueness + catalog size.

// HintTmuxCreateSessionExists — tmux session name collision.
// Origin: Cluster 4 / tmux gotcha (pilot analysis, 2026-04-19). AllowForce=true.
const HintTmuxCreateSessionExists = "tmux.create.session-exists"

// HintTmuxCreateNotAWorktree — --cwd is not a git worktree.
// Origin: CC1 (run thrum from correct directory), R-35 (worktree-only features).
// Principled refusal — AllowForce=false.
const HintTmuxCreateNotAWorktree = "tmux.create.not-a-worktree"

// HintTmuxCreateIdentityExistsAlive — worktree has a live registered agent.
// Origin: R-15 step 2 (identity file creation), D12 (never rename worktree-tied
// agent). Overwriting a live agent's identity orphans a running session; the
// correct recovery is coordination, not --force. AllowForce=false (hard refusal).
const HintTmuxCreateIdentityExistsAlive = "tmux.create.identity-exists-alive"

// HintTmuxCreateIdentityExistsStale — worktree has a stale identity (file
// exists, no live session). Origin: R-15 step 2. Recoverable via --force.
const HintTmuxCreateIdentityExistsStale = "tmux.create.identity-exists-stale"

// HintTmuxCreateNextLaunch — post-success next-step tip after tmux create.
// Origin: R-15 steps 3–4, R-16 (kiro/auggie no auto-prime). info severity.
const HintTmuxCreateNextLaunch = "tmux.create.next-launch"

// HintTmuxCreateIdentityReplaced — audit trail after --force stale-identity
// replacement. Fires only when --force was passed AND the pre-action state was
// IdentityStale. info severity.
const HintTmuxCreateIdentityReplaced = "tmux.create.identity-replaced"

// HintSendRecipientStale — recipient's last activity is beyond the stale
// threshold. Origin: Cluster 4 lifecycle (empirical, no single rule). info.
const HintSendRecipientStale = "send.recipient-stale"

// HintInitNextQuickstart — post-success next-step tip after thrum init when
// no agent identity is registered yet. Origin: R-15 step 2 + implicit
// init→quickstart sequence. info.
const HintInitNextQuickstart = "init.next-quickstart"

// RecipientStaleMinutes is the send-side stale cutoff in minutes. Tunable
// here; becomes a config key in Phase C if the pilot signals that 30 minutes
// is the wrong threshold.
const RecipientStaleMinutes = 30

// AllHintCodes is the canonical list of codes. L3 tests scan it for format
// and uniqueness; renderer tests use it as a reference set.
var AllHintCodes = []string{
	HintTmuxCreateSessionExists,
	HintTmuxCreateNotAWorktree,
	HintTmuxCreateIdentityExistsAlive,
	HintTmuxCreateIdentityExistsStale,
	HintTmuxCreateNextLaunch,
	HintTmuxCreateIdentityReplaced,
	HintSendRecipientStale,
	HintInitNextQuickstart,
}
