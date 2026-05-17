package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// skillCmd is the parent cobra command for the C-B1 skill registration
// surface. Subcommands wrap the skill.* JSON-RPC methods over the
// daemon's WebSocket channel. The body of each subcommand is a thin
// wrapper that builds an RPC request, dispatches via getClient(), and
// renders the response — production wiring lands at the per-verb
// implementation tasks (E10.2–E10.8) but the skeleton, flag set, and
// help text live here at E10.1 so subsequent tasks have a stable
// command tree to fill in.
//
// Per spec §8 the verb set is:
//
//	thrum skill list      [--pending] [--proposed-by <agent>]
//	thrum skill show      <name> [--raw]
//	thrum skill check     <path> [--wait]
//	thrum skill check status <check-id>
//	thrum skill promote   <path> [--force] [--allow-secret <regex>...]
//	thrum skill delete    <name> [--force]
//	thrum skill revise    <path> <body>
//	thrum skill sync      [<name>]
//	thrum skill validate  [<name>]
//
// Operator-mode equivalence: invoked from a TTY without --from-agent,
// the CLI authenticates as the local operator via the existing
// UDS-peercred pattern at `internal/daemon/peercred/` — no special
// handling required at this layer; getClient() does the right thing.
func skillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage per-project skills",
		Long: `Manage per-project skills under .thrum/skills/.

Subcommands wrap the daemon's skill.* JSON-RPC methods.
Coordinator-gated verbs (check, promote, delete, revise) require
the caller to be a coordinator-role agent or local operator.`,
	}
	cmd.AddCommand(skillListCmd())
	cmd.AddCommand(skillShowCmd())
	cmd.AddCommand(skillCheckCmd())
	cmd.AddCommand(skillPromoteCmd())
	cmd.AddCommand(skillDeleteCmd())
	cmd.AddCommand(skillReviseCmd())
	cmd.AddCommand(skillSyncCmd())
	cmd.AddCommand(skillValidateCmd())
	return cmd
}

// skillListCmd implements `thrum skill list [--pending]
// [--proposed-by <agent>]` per spec §7.1.
func skillListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List promoted skills (or pending proposals with --pending)",
		Long: `List promoted skills in .thrum/skills/.

With --pending, walks every .thrum/agents/*/proposed-skills/ and
returns the in-flight proposals — useful for coordinator triage.

With --proposed-by <agent>, restricts pending results to one author.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("skill.list RPC body lands at E10.2 (thrum-6qmf.2.9)")
		},
	}
	cmd.Flags().Bool("pending", false, "List pending proposals from .thrum/agents/*/proposed-skills/ instead of promoted skills")
	cmd.Flags().String("proposed-by", "", "Filter pending listings by author agent ID (only with --pending)")
	return cmd
}

// skillShowCmd implements `thrum skill show <name> [--raw]` per
// spec §7.2.
func skillShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Render a single SKILL.md with parsed frontmatter",
		Long: `Show the parsed frontmatter and body for a named skill.

With --raw, the raw file contents are appended after the parsed
view — used by the diff path of an edit-flow promote and by the
check-the-skill meta-skill (when C-B2 ships).`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("skill.show RPC body lands at E10.2 (thrum-6qmf.2.9)")
		},
	}
	cmd.Flags().Bool("raw", false, "Append raw SKILL.md contents to the parsed-view output")
	return cmd
}

// skillCheckCmd implements `thrum skill check <path> [--wait]` per
// spec §7.3 plus the nested `check status <check-id>` per §7.4. The
// v0.11 first-ship behavior is stub-and-ship-broken per canonical
// §8.3: returns ErrCheckSkillNotAvailable + suggests --force on
// promote. The CLI verb, the RPC plumbing, and the
// async/inbox-completion path are all wired in v0.11; only the
// meta-skill itself is the gap.
func skillCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check <path>",
		Short: "Submit a proposed SKILL.md to the check-the-skill meta-skill",
		Long: `Submit a proposed SKILL.md (under .thrum/agents/<author>/
proposed-skills/<name>/SKILL.md) to the check-the-skill meta-skill
for admission review.

v0.11 first-ship: this is a stub. The CLI verb + RPC plumbing +
async/inbox-completion path are all wired and tested, but the
meta-skill itself ships at C-B2. The current behavior returns
'check_the_skill_not_available' (exit code 2). Coordinators can
bypass the admission gate during the stub window via
'thrum skill promote --force <path>'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("skill.check RPC stub lands at E10.3 (thrum-6qmf.2.11)")
		},
	}
	cmd.Flags().Bool("wait", false, "Block up to 30s for short interactive runs; longer runs return pending + check-id for polling")
	cmd.AddCommand(skillCheckStatusCmd())
	return cmd
}

// skillCheckStatusCmd implements `thrum skill check status <check-id>`
// per spec §7.4.
func skillCheckStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <check-id>",
		Short: "Poll a check-id for completion status",
		Long: `Poll the check-the-skill meta-skill for a previously-submitted
check. Returns 'pending', 'complete', or 'error'. v0.11 first-ship
always returns 'error: check_the_skill_not_available' per the stub
contract.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("skill.check_status RPC stub lands at E10.3 (thrum-6qmf.2.11)")
		},
	}
}

// skillPromoteCmd implements `thrum skill promote <path> [--force]
// [--allow-secret <regex>]` per spec §7.5. Coordinator-gated.
// Repeatable --allow-secret per AC line 660: each occurrence records
// an entry in review.secret_scan_overrides (audit trail).
func skillPromoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote <path>",
		Short: "Land a proposed SKILL.md into .thrum/skills/",
		Long: `Promote a proposed SKILL.md (under .thrum/agents/<author>/
proposed-skills/<name>/SKILL.md) into the canonical
.thrum/skills/<name>/SKILL.md.

Runs secret-scan against the proposal body. If any pattern fires,
the promote is blocked. Use --allow-secret <regex> (repeatable) to
record an audit-trail override for a specific pattern — typically
the literal fake-secret string in a test fixture.

In the C-B2 stub window, pass --force to bypass the check-the-skill
admission gate. Secret-scan still runs even with --force.

Stamps the daemon-side provenance fields (thrum.review.*) atomically
with the promote; emits an inbox notification to coordinator-role
agents in the repo; cancels the proposal's staleness reminder.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("skill.promote RPC body lands at E10.4 (thrum-6qmf.2.16)")
		},
	}
	cmd.Flags().Bool("force", false, "Bypass the check-the-skill admission gate (secret-scan still runs)")
	cmd.Flags().StringSlice("allow-secret", nil, "Audit-trail override for a secret-scan pattern (repeatable; use --allow-secret <regex>)")
	return cmd
}

// skillDeleteCmd implements `thrum skill delete <name> [--force]` per
// spec §7.6. Coordinator-gated. Triggers eager mirror cleanup
// across every active worktree.
func skillDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Remove a promoted skill + mirror cleanup",
		Long: `Remove a promoted skill (.thrum/skills/<name>/) and cascade
eager-cleanup to every active worktree's mirror destination per the
adapter table.

Without --force, prompts for confirmation. --force skips the
prompt + emits an audit log line.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("skill.delete RPC body lands at E10.6 (thrum-6qmf.2.19)")
		},
	}
	cmd.Flags().Bool("force", false, "Skip the confirmation prompt")
	return cmd
}

// skillReviseCmd implements `thrum skill revise <path> <body>` per
// spec §7.7. Coordinator-gated. Never writes into the submitter's
// proposed-skills/ — honors MB-1.S2 Q2 owner-write-only — instead
// sends a structured revision message to the proposing agent.
func skillReviseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revise <path> <body>",
		Short: "Send a structured revision message to the proposing agent",
		Long: `Send a structured revision message about a proposed SKILL.md back
to its author. The CLI does NOT write into the submitter's
proposed-skills/ — the message thread is the revision channel
(MB-1.S2 Q2 owner-write-only).

<path> is the proposal path; <body> is the revision request text.`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("skill.revise RPC body lands at E10.5 (thrum-6qmf.2.17)")
		},
	}
}

// skillSyncCmd implements `thrum skill sync [<name>]` per spec §7.8.
// Optional positional <name> restricts the resync to one skill;
// without arg, runs full Worker.Reconcile.
func skillSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync [<name>]",
		Short: "Manually re-trigger mirror reconcile",
		Long: `Force a manual mirror re-trigger. Useful when a runtime path
was edited externally and needs reset.

Without arguments, runs the full Worker.Reconcile pass against
every registered worktree × runtime. With <name>, restricts the
resync to a single skill.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("skill.sync RPC body lands at E10.7 (thrum-6qmf.2.20)")
		},
	}
}

// skillValidateCmd implements `thrum skill validate [<name>]` per
// spec §7.9. Optional positional <name> restricts to one skill;
// without arg, validates every promoted + proposed skill.
func skillValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [<name>]",
		Short: "Schema-conformance check for skill frontmatter",
		Long: `Run the E8.4 validator against a single named skill or
(without args) every promoted + proposed skill. Surfaces:
frontmatter_invalid / duplicate_field / missing_required /
name_mismatch / regex_violation findings.

Used post-merge as a defense against malformed frontmatter that
escaped propose-time validation (e.g. a git merge conflict that
duplicated the thrum: block).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("skill.validate RPC body lands at E10.8 (thrum-6qmf.2.21)")
		},
	}
}
