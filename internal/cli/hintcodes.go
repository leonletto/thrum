package cli

import "time"

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
// Origin: R-15 steps 3–4, R-16 (kiro/auggie no auto-prime). Info severity.
const HintTmuxCreateNextLaunch = "tmux.create.next-launch"

// HintTmuxCreateIdentityReplaced — audit trail after --force stale-identity
// replacement. Fires only when --force was passed AND the pre-action state was
// IdentityStale. Info severity.
const HintTmuxCreateIdentityReplaced = "tmux.create.identity-replaced"

// HintSendRecipientStale — recipient's last activity is beyond the stale
// threshold. Origin: Cluster 4 lifecycle (empirical, no single rule). Info.
const HintSendRecipientStale = "send.recipient-stale"

// HintInitNextQuickstart — post-success next-step tip after thrum init when
// no agent identity is registered yet. Origin: R-15 step 2 + implicit
// init→quickstart sequence. Info.
const HintInitNextQuickstart = "init.next-quickstart"

// HintSnapshotSaveNoJSONL — thrum tmux snapshot save could not locate the
// Claude Code JSONL for the resolved agent PID. Covers both underlying
// restart.FindSessionJSONL failures: (a) missing ~/.claude/sessions/<pid>.json,
// (b) session file resolved but the per-CWD JSONL under ~/.claude/projects/
// is absent. Origin: release-test sweep 2026-04-21 — @impl_team_fix hit a
// save failure that /thrum:restart step 3 swallowed silently (no exit-code
// check), pre-fix by coordinator commit 27e84c39. Severity: warn; hard
// refusal (no --force recovery — the JSONL either exists or it doesn't).
const HintSnapshotSaveNoJSONL = "snapshot.save.no-jsonl"

// HintSnapshotSaveNoPID — identity file is missing agent_pid AND the daemon
// lookup returned no PID for the agent. Either the agent isn't registered
// yet or the identity file predates the AgentPID column. Origin: same
// release-test sweep as HintSnapshotSaveNoJSONL. Severity: warn; hard refusal.
const HintSnapshotSaveNoPID = "snapshot.save.no-pid"

// HintSnapshotSaveExtractFailed — JSONL located but restart.ExtractConversation
// failed to read/parse it. Usually a file-permission or corruption issue,
// or a file-not-found when a --jsonl override points at a path that exists
// but the reader can't parse (e.g. non-JSONL content).
// Origin: same release-test sweep as HintSnapshotSaveNoJSONL. Severity: warn;
// hard refusal.
const HintSnapshotSaveExtractFailed = "snapshot.save.extract-failed"

// HintSnapshotSaveJSONLNotFound — the --jsonl <path> supplied by the caller
// does not exist on disk. Distinguished from HintSnapshotSaveExtractFailed
// (which fires when the file DOES exist but can't be read/parsed) so the
// remediation can focus on the typo/path-resolution case instead of
// permissions or corruption. Origin: dual-review thrum-ufv5.7 finding #2.
// Severity: warn; hard refusal.
const HintSnapshotSaveJSONLNotFound = "snapshot.save.jsonl-not-found"

// HintRolesConfigMigration fires when .thrum/role_templates/<role>.md exists
// but .thrum/config.json has no role_config block — meaning the repo
// pre-dates configure-roles persistence. Severity: warn. Remediation: run
// /thrum:configure-roles to register the active settings.
const HintRolesConfigMigration = "roles.config.migration"

// HintRolesConfigSchemaBump fires when a shipped role template declares a
// higher schema_version than the saved role_config — the configure-roles
// skill has new questions to ask. Severity: warn. Remediation:
// /thrum:configure-roles.
const HintRolesConfigSchemaBump = "roles.config.schema-bump"

// HintRolesConfigBodyDiff fires when the saved rendered_hash for a role does
// not match the shipped template body_hash (schema unchanged) — answers
// remain valid but the rendered file is stale. Severity: warn. Remediation:
// thrum roles refresh.
const HintRolesConfigBodyDiff = "roles.config.body-diff"

// RecipientStaleThreshold is the send-side stale cutoff. Exported per
// spec §4 as a tunable; becomes a config key in Phase C if the pilot
// signals that 30 minutes is the wrong threshold.
const RecipientStaleThreshold = 30 * time.Minute

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
	HintSnapshotSaveNoJSONL,
	HintSnapshotSaveNoPID,
	HintSnapshotSaveExtractFailed,
	HintSnapshotSaveJSONLNotFound,
	HintRolesConfigMigration,
	HintRolesConfigSchemaBump,
	HintRolesConfigBodyDiff,
}
