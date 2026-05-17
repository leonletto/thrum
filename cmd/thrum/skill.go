package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/daemon/rpc"
)

// osExit is a seam for tests — production code uses os.Exit; tests
// override to capture the exit-code request without terminating the
// test process.
var osExit = os.Exit

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
		RunE: func(cmd *cobra.Command, _ []string) error {
			pending, _ := cmd.Flags().GetBool("pending")
			proposedBy, _ := cmd.Flags().GetString("proposed-by")
			return runSkillList(cmd.OutOrStdout(), pending, proposedBy)
		},
	}
	cmd.Flags().Bool("pending", false, "List pending proposals from .thrum/agents/*/proposed-skills/ instead of promoted skills")
	cmd.Flags().String("proposed-by", "", "Filter pending listings by author agent ID (only with --pending)")
	return cmd
}

// runSkillList dispatches the skill.list RPC and renders the response.
// Factored out of the cobra RunE so future tests can drive the body
// without going through cobra parsing.
func runSkillList(out io.Writer, pending bool, proposedBy string) error {
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
	}
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	req := map[string]any{
		"caller_agent_id": agentID,
		"pending":         pending,
		"proposed_by":     proposedBy,
	}
	var resp struct {
		Skills json.RawMessage `json:"skills"`
	}
	if err := client.Call("skill.list", req, &resp); err != nil {
		return fmt.Errorf("skill.list RPC failed: %w", err)
	}
	if flagJSON {
		return cli.EmitJSON(resp)
	}
	return renderSkillList(out, resp.Skills, pending)
}

// renderSkillList writes the human-readable list table to out. Empty
// result writes a single hint line so operators see "no skills" rather
// than a blank screen. The body wraps `out` in a text/tabwriter so
// the tab-separated rows render as aligned columns; the final Flush
// happens via the tabwriter's own buffer when the wrapping function
// returns. Write errors are tracked via skillWriter so the first
// failure (e.g. closed stdout in a pipe) propagates to the caller.
func renderSkillList(out io.Writer, raw json.RawMessage, pending bool) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer func() { _ = tw.Flush() }()
	w := skillWriter{w: tw}
	if len(raw) == 0 || string(raw) == "null" {
		w.Fprintln("(no skills)")
		return w.err
	}
	if pending {
		var entries []rpc.ProposedSkillEntry
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("decode pending skills: %w", err)
		}
		if len(entries) == 0 {
			w.Fprintln("(no pending proposals)")
			return w.err
		}
		w.Fprintln("PROPOSED_BY\tNAME\tAGE\tDESCRIPTION")
		for _, e := range entries {
			w.Fprintf("%s\t%s\t%.1fh\t%s\n", e.ProposedBy, e.Name, e.AgeHours, e.Description)
		}
		return w.err
	}
	var entries []rpc.SkillListEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("decode promoted skills: %w", err)
	}
	if len(entries) == 0 {
		w.Fprintln("(no promoted skills)")
		return w.err
	}
	w.Fprintln("NAME\tVERSION\tDESCRIPTION")
	for _, e := range entries {
		w.Fprintf("%s\t%s\t%s\n", e.Name, e.Version, e.Description)
	}
	return w.err
}

// skillWriter wraps an io.Writer and short-circuits subsequent writes
// once the first error fires. Keeps the renderers free of per-call
// error-handling clutter; the caller checks .err once at the end.
type skillWriter struct {
	w   io.Writer
	err error
}

func (w *skillWriter) Fprintln(args ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprintln(w.w, args...)
	}
}

func (w *skillWriter) Fprintf(format string, args ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprintf(w.w, format, args...)
	}
}

func (w *skillWriter) Fprint(args ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprint(w.w, args...)
	}
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
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := cmd.Flags().GetBool("raw")
			return runSkillShow(cmd.OutOrStdout(), args[0], raw)
		},
	}
	cmd.Flags().Bool("raw", false, "Append raw SKILL.md contents to the parsed-view output")
	return cmd
}

// runSkillShow dispatches skill.show. nameOrPath is treated as a path
// when it contains a path separator OR ends in ".md" — heuristic that
// matches the spec §7.2 / §19 AC E10 #4 case where the operator pastes
// a proposed-SKILL.md path. Promoted skills are always referenced by
// bare directory name.
func runSkillShow(out io.Writer, nameOrPath string, includeRaw bool) error {
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w", err)
	}
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	req := map[string]any{
		"caller_agent_id": agentID,
		"include_raw":     includeRaw,
	}
	if looksLikePath(nameOrPath) {
		req["path"] = nameOrPath
	} else {
		req["name"] = nameOrPath
	}
	var resp rpc.SkillShowResponse
	if err := client.Call("skill.show", req, &resp); err != nil {
		return fmt.Errorf("skill.show RPC failed: %w", err)
	}
	if flagJSON {
		return cli.EmitJSON(resp)
	}
	return renderSkillShow(out, resp)
}

// looksLikePath reports whether the operator-supplied argument is a
// filesystem path (proposed-skill) vs a bare directory name (promoted
// skill). The spec §9.1 skill name regex forbids "/" and "." — either
// is a reliable path-discriminator.
func looksLikePath(s string) bool {
	for _, r := range s {
		if r == '/' || r == '.' {
			return true
		}
	}
	return false
}

// renderSkillShow writes the parsed view + optional raw block.
func renderSkillShow(out io.Writer, resp rpc.SkillShowResponse) error {
	w := skillWriter{w: out}
	fm := resp.Frontmatter
	w.Fprintf("NAME:         %s\n", fm.Name)
	if fm.Description != "" {
		w.Fprintf("DESCRIPTION:  %s\n", fm.Description)
	}
	if fm.Version != "" {
		w.Fprintf("VERSION:      %s\n", fm.Version)
	}
	if fm.Author != "" {
		w.Fprintf("AUTHOR:       %s\n", fm.Author)
	}
	if fm.License != "" {
		w.Fprintf("LICENSE:      %s\n", fm.License)
	}
	if len(fm.AllowedTools) > 0 {
		w.Fprintf("ALLOWED:      %v\n", fm.AllowedTools)
	}
	prov := fm.Thrum
	if prov.ProposedBy != "" || prov.PromotedBy != "" {
		w.Fprintln("PROVENANCE:")
		if prov.ProposedBy != "" {
			w.Fprintf("  proposed_by:    %s\n", prov.ProposedBy)
		}
		if prov.PromotedBy != "" {
			w.Fprintf("  promoted_by:    %s\n", prov.PromotedBy)
		}
		if !prov.CreatedAt.IsZero() {
			w.Fprintf("  created_at:     %s\n", prov.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
		}
	}
	w.Fprintln("---")
	w.Fprintln(resp.Body)
	if resp.Raw != "" {
		w.Fprintln("--- RAW ---")
		w.Fprint(resp.Raw)
	}
	return w.err
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
		RunE: func(cmd *cobra.Command, args []string) error {
			wait, _ := cmd.Flags().GetBool("wait")
			err := runSkillCheck(args[0], wait)
			// Stub-mode exit-code-2: the v0.11 first-ship path returns
			// ErrCheckTheSkillNotAvailable from the daemon. Print the
			// verbatim canonical §8.3 message to stderr and exit 2
			// (per spec §7.3); any other error falls through to cobra's
			// default exit-1 path.
			if code := classifySkillCheckError(err); code == 2 {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), rpc.CheckSkillNotAvailableMessage)
				osExit(2)
			}
			return err
		},
	}
	cmd.Flags().Bool("wait", false, "Block up to 30s for short interactive runs; longer runs return pending + check-id for polling")
	cmd.AddCommand(skillCheckStatusCmd())
	return cmd
}

// runSkillCheck dispatches skill.check. Always returns the daemon's
// error in the v0.11 stub window (HandleCheck returns
// ErrCheckTheSkillNotAvailable); the caller classifies via
// classifySkillCheckError to choose between exit-code-2 (stub error)
// and the default cobra exit-1 path (any other failure).
func runSkillCheck(path string, wait bool) error {
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w", err)
	}
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	req := map[string]any{
		"caller_agent_id": agentID,
		"path":            path,
		"wait":            wait,
	}
	// Stub mode never returns a populated result; the live form post-
	// C-B2 will populate {check_id, status, estimated_complete_at}.
	// Using map[string]any keeps the result handling agnostic to the
	// flip from stub to live.
	var result map[string]any
	return client.Call("skill.check", req, &result)
}

// classifySkillCheckError returns the exit code the CLI should use
// for a given skill.check error. 0 for nil, 2 for the v0.11 stub
// error (per spec §7.3), 1 for any other error. Pure function —
// the unit test in skill_test.go drives this directly rather than
// invoking the CLI as a subprocess.
func classifySkillCheckError(err error) int {
	if err == nil {
		return 0
	}
	if strings.Contains(err.Error(), rpc.CheckSkillNotAvailableMessage) {
		return 2
	}
	return 1
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillCheckStatus(cmd.OutOrStdout(), args[0])
		},
	}
}

// runSkillCheckStatus dispatches skill.check_status and renders the
// response. In the v0.11 stub window the response is always
// {status: error, error: check_the_skill_not_available} — render that
// to stderr and return a non-nil error so cobra exits non-zero. E10.3
// pins the specific exit code (2) and the verbatim-message constants
// for `thrum skill check` (not check_status); check_status here just
// surfaces whatever the daemon returns.
func runSkillCheckStatus(out io.Writer, checkID string) error {
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w", err)
	}
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	req := map[string]any{
		"caller_agent_id": agentID,
		"check_id":        checkID,
	}
	var resp rpc.SkillCheckStatusResponse
	if err := client.Call("skill.check_status", req, &resp); err != nil {
		return fmt.Errorf("skill.check_status RPC failed: %w", err)
	}
	if flagJSON {
		return cli.EmitJSON(resp)
	}
	w := skillWriter{w: out}
	w.Fprintf("status: %s\n", resp.Status)
	if resp.Error != "" {
		w.Fprintf("error:  %s\n", resp.Error)
	}
	if w.err != nil {
		return w.err
	}
	if resp.Status == "error" {
		return errors.New(resp.Error)
	}
	return nil
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
the literal fake-secret string in a test fixture. Pair each --allow-secret
with a matching --allow-secret-reason that explains why the pattern is
safe in this proposal.

In the C-B2 stub window, pass --force to bypass the check-the-skill
admission gate. Secret-scan still runs even with --force.

Stamps the daemon-side provenance fields (thrum.review.*) atomically
with the promote; emits an inbox notification to every non-supervisor
agent in the repo; cancels the proposal's staleness reminder.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			forceReason, _ := cmd.Flags().GetString("force-reason")
			patterns, _ := cmd.Flags().GetStringSlice("allow-secret")
			reasons, _ := cmd.Flags().GetStringSlice("allow-secret-reason")
			msgThreadID, _ := cmd.Flags().GetString("msg-thread-id")
			return runSkillPromote(cmd.OutOrStdout(), args[0], force, forceReason, patterns, reasons, msgThreadID)
		},
	}
	cmd.Flags().Bool("force", false, "Bypass the check-the-skill admission gate (secret-scan still runs)")
	cmd.Flags().String("force-reason", "", "Operator-supplied reason for --force; recorded in review.force_override (audit trail)")
	cmd.Flags().StringSlice("allow-secret", nil, "Audit-trail override for a secret-scan pattern (repeatable; pair with --allow-secret-reason)")
	cmd.Flags().StringSlice("allow-secret-reason", nil, "Reason for the corresponding --allow-secret entry (repeatable; position-paired with --allow-secret)")
	cmd.Flags().String("msg-thread-id", "", "Inbound revision message thread ID — recorded under review.revisions on edit-promote")
	return cmd
}

// runSkillPromote dispatches the skill.promote RPC and renders the
// response. Pairing of --allow-secret patterns with --allow-secret-reason
// is positional: the i-th pattern maps to the i-th reason. A pattern
// without a paired reason gets a default reason string with the
// caller's identity so the audit trail is never empty.
func runSkillPromote(out io.Writer, path string, force bool, forceReason string, patterns, reasons []string, msgThreadID string) error {
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w", err)
	}
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	overrides := make([]map[string]string, 0, len(patterns))
	for i, p := range patterns {
		reason := ""
		if i < len(reasons) {
			reason = reasons[i]
		}
		if reason == "" {
			reason = fmt.Sprintf("operator override by %s at promote time", agentID)
		}
		overrides = append(overrides, map[string]string{
			"pattern": p,
			"reason":  reason,
		})
	}

	req := map[string]any{
		"caller_agent_id":       agentID,
		"path":                  path,
		"force":                 force,
		"allow_secret_patterns": overrides,
	}
	if forceReason != "" {
		req["force_reason"] = forceReason
	}
	if msgThreadID != "" {
		req["msg_thread_id"] = msgThreadID
	}
	var resp rpc.SkillPromoteResponse
	if err := client.Call("skill.promote", req, &resp); err != nil {
		return fmt.Errorf("skill.promote RPC failed: %w", err)
	}
	if flagJSON {
		return cli.EmitJSON(resp)
	}
	return renderSkillPromote(out, resp)
}

// renderSkillPromote writes a human-readable summary of the promote
// response. On the success path emits {promoted_path, mode, reviewed_at}
// plus override summary; on the error path emits the error code and the
// per-finding detail.
func renderSkillPromote(out io.Writer, resp rpc.SkillPromoteResponse) error {
	w := skillWriter{w: out}
	if resp.Error != "" {
		w.Fprintf("PROMOTE BLOCKED: %s\n", resp.Error)
		for _, f := range resp.FrontmatterFindings {
			detail := f.Detail
			if detail == "" {
				detail = "(no detail)"
			}
			w.Fprintf("  frontmatter: %s @ %s — %s\n", f.Kind, f.Path, detail)
		}
		for _, f := range resp.SecretFindings {
			w.Fprintf("  secret-scan: %s @ %s:%d\n", f.PatternCategory, f.Path, f.Line)
		}
		if err := w.err; err != nil {
			return err
		}
		return errors.New(resp.Error)
	}
	w.Fprintf("PROMOTED: %s\n", resp.PromotedPath)
	w.Fprintf("  mode:        %s\n", resp.Mode)
	w.Fprintf("  promoted_at: %s\n", resp.PromotedAt.Format("2006-01-02T15:04:05Z07:00"))
	if resp.Review != nil {
		w.Fprintf("  reviewed_by: %s\n", resp.Review.ReviewedBy)
		w.Fprintf("  revisions:   %d\n", len(resp.Review.Revisions))
		if n := len(resp.Review.SecretScanOverrides); n > 0 {
			w.Fprintf("  overrides:   %d\n", n)
		}
	}
	return w.err
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
		Use:   "revise <path> <findings>",
		Short: "Send a structured revision message to the proposing agent",
		Long: `Send a structured revision message about a proposed SKILL.md back
to its author. The CLI does NOT write into the submitter's
proposed-skills/ — the message thread is the revision channel
(MB-1.S2 Q2 owner-write-only).

<path> is the proposal path; <findings> is the revision request text.
The composed message body is rendered with section headings and the
path's <author> segment is used as the routing recipient (canonical
per spec §17.2 — frontmatter mismatch logs a warning but proceeds).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillRevise(cmd.OutOrStdout(), args[0], args[1])
		},
	}
}

// runSkillRevise dispatches the skill.revise RPC and renders the
// response. On the logical-error path (proposal_not_found) it surfaces
// the error and returns non-nil so the cobra exit is non-zero.
func runSkillRevise(out io.Writer, path, findings string) error {
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w", err)
	}
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	req := map[string]any{
		"caller_agent_id": agentID,
		"path":            path,
		"findings":        findings,
	}
	var resp rpc.SkillReviseResponse
	if err := client.Call("skill.revise", req, &resp); err != nil {
		return fmt.Errorf("skill.revise RPC failed: %w", err)
	}
	if flagJSON {
		return cli.EmitJSON(resp)
	}
	w := skillWriter{w: out}
	if resp.Error != "" {
		w.Fprintf("REVISE BLOCKED: %s\n", resp.Error)
		if w.err != nil {
			return w.err
		}
		return errors.New(resp.Error)
	}
	w.Fprintf("REVISION SENT: %s\n", path)
	w.Fprintf("  message_id: %s\n", resp.MessageID)
	w.Fprintf("  thread_id:  %s\n", resp.ThreadID)
	return w.err
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
