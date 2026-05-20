package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/spf13/cobra"
)

// reminderCmd returns the `thrum agent reminder` subcommand tree.
// Hosts `set` and `list` subcommands; positional `<id>` invokes the
// lookup view via the parent RunE. The positional form takes
// precedence as the canonical lookup UX (Q3.1 "forcing-function
// anchor") — cobra dispatches to a matching subcommand first, then
// falls through to the parent RunE for non-subcommand args.
// ORIGIN[thrum-8kxh]: moved from main.go:2103-2136
// Destination: reminder.go:25-58
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func reminderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reminder [id]",
		Short: "Manage reminders for an agent",
		Long: "Reminders are persistent records with a fire-or-defer lifecycle.\n\n" +
			"Set one with 'thrum agent reminder set --in 1h --body \"finish release notes\"'.\n" +
			"Look one up with 'thrum agent reminder <id>' — that's the forcing-function\n" +
			"anchor: the fire message tells you \"run `thrum agent reminder <id>`\" and\n" +
			"that lookup is where the full body + activity-since-raised banner lives.\n\n" +
			"From the lookup, take action with one of:\n" +
			"  --defer <duration>  snooze the next fire (e.g. --defer 30m)\n" +
			"  --clear             situation resolved; you're done with it\n" +
			"  --cancel            reminder set in error / no longer wanted\n\n" +
			"--clear vs --cancel:\n" +
			"  --clear: situation resolved, recipient saw it and is done with it.\n" +
			"           Example: stalled-agent alert fired, you investigated,\n" +
			"           agent is fine — clear it.\n" +
			"  --cancel: reminder set in error or no longer wanted; abort/withdraw.\n" +
			"            Example: you set 'thrum agent reminder set --in 1h --body \"ship the website\"'\n" +
			"            and then shipped early — cancel it.\n\n" +
			"The schema records the difference (cleared_at vs cancelled_at) so a\n" +
			"'thrum agent reminder list --state cleared' or '--state cancelled'\n" +
			"query later shows which is which.",
		Args: cobra.MaximumNArgs(1),
		RunE: runReminderLookup,
	}
	cmd.Flags().String("defer", "", "snooze the reminder by a duration (e.g. 30m, 1h, 2d)")
	cmd.Flags().Bool("clear", false, "mark the reminder as resolved")
	cmd.Flags().Bool("cancel", false, "withdraw the reminder (set in error / no longer wanted)")
	cmd.MarkFlagsMutuallyExclusive("defer", "clear", "cancel")
	cmd.AddCommand(reminderSetCmd())
	cmd.AddCommand(reminderListCmd())
	return cmd
}

// runReminderLookup handles `thrum agent reminder <id>` when the arg
// doesn't match a known subcommand (set / list). With zero args it
// prints help. With an id and no action flag, renders the lookup
// view. With --defer / --clear / --cancel (mutually exclusive — cobra
// enforces this at parse time), dispatches the corresponding RPC.
// ORIGIN[thrum-8kxh]: moved from main.go:2143-2195
// Destination: reminder.go:71-123
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func runReminderLookup(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	id := args[0]

	deferStr, _ := cmd.Flags().GetString("defer")
	clearFlag, _ := cmd.Flags().GetBool("clear")
	cancelFlag, _ := cmd.Flags().GetBool("cancel")

	client, err := getClient()
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	switch {
	case deferStr != "":
		d, err := parseFutureDuration(deferStr)
		if err != nil {
			return fmt.Errorf("--defer invalid: %w", err)
		}
		until := time.Now().UTC().Add(d)
		by, _ := resolveLocalAgentID() // best-effort; empty is fine for audit label
		if err := cli.ReminderDefer(client, id, until, by); err != nil {
			return err
		}
		fmt.Printf("Deferred to %s\n", until.Format(time.RFC3339))
		return nil
	case clearFlag:
		by, _ := resolveLocalAgentID()
		if err := cli.ReminderClear(client, id, by); err != nil {
			return err
		}
		fmt.Println("Cleared.")
		return nil
	case cancelFlag:
		by, _ := resolveLocalAgentID()
		if err := cli.ReminderCancel(client, id, by); err != nil {
			return err
		}
		fmt.Println("Cancelled.")
		return nil
	}

	// No action flag → lookup view.
	r, err := cli.ReminderGet(client, id)
	if err != nil {
		return err
	}
	fmt.Println(formatReminderLookup(*r, time.Now().UTC()))
	return nil
}

// formatReminderLookup is the lookup-view renderer. Pure function so
// it's unit-testable without a daemon. Renders per plan §Task 14:
// ID + source + trigger_kind, trigger info, target, full body,
// pane_snapshot preview (for condition rows), defer history, and the
// Q3.4 "agent has been active for X since raised" banner for
// condition rows.
//
// `now` is the clock seam (deterministic test output for the
// activity-since-raised duration).
// ORIGIN[thrum-8kxh]: moved from main.go:2206-2281
// Destination: reminder.go:140-215
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func formatReminderLookup(r cli.ReminderRow, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Reminder %s\n", r.ID)
	fmt.Fprintf(&b, "  source:       %s\n", r.Source)
	fmt.Fprintf(&b, "  trigger_kind: %s\n", r.TriggerKind)
	fmt.Fprintf(&b, "  state:        %s\n", r.State)

	// Trigger info varies by kind.
	switch r.TriggerKind {
	case "time":
		if r.NextReminderAt != nil {
			fmt.Fprintf(&b, "  fires at:     %s\n", r.NextReminderAt.Format(time.RFC3339))
		} else if r.LastFiredAt != nil {
			fmt.Fprintf(&b, "  fired at:     %s\n", r.LastFiredAt.Format(time.RFC3339))
		}
	case "condition_pane_quiet":
		fmt.Fprintf(&b, "  condition:    pane_quiet\n")
		if r.LastFiredAt != nil {
			fmt.Fprintf(&b, "  last fired:   %s\n", r.LastFiredAt.Format(time.RFC3339))
		}
		if r.NextReminderAt != nil {
			fmt.Fprintf(&b, "  next rearm:   %s\n", r.NextReminderAt.Format(time.RFC3339))
		}
	}

	// Target: single agent or chain.
	if r.TargetAgent != "" {
		fmt.Fprintf(&b, "  target:       @%s\n", r.TargetAgent)
	}
	if len(r.TargetChain) > 0 {
		fmt.Fprintf(&b, "  chain:        %s\n", strings.Join(r.TargetChain, ", "))
	}

	// Raised-at + activity banner for condition rows.
	if !r.RaisedAt.IsZero() {
		fmt.Fprintf(&b, "  raised:       %s\n", r.RaisedAt.Format(time.RFC3339))
		if r.TriggerKind == "condition_pane_quiet" && r.RaisedAt.Before(now) {
			fmt.Fprintf(&b, "\n  Note: agent has been active for %s since this alert was raised.\n",
				formatLookupElapsed(now.Sub(r.RaisedAt)))
		}
	}

	if r.Body != "" {
		fmt.Fprintf(&b, "\n  body:\n    %s\n", strings.ReplaceAll(r.Body, "\n", "\n    "))
	}

	// Pane snapshot preview (condition rows).
	if r.TriggerKind == "condition_pane_quiet" && r.PaneSnapshot != "" {
		fmt.Fprintf(&b, "\n  pane snapshot (%d bytes):\n", len(r.PaneSnapshot))
		for _, line := range lastNLines(r.PaneSnapshot, 20) {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}

	// Defer history (plan §Task 14 AC: render each (deferred_by,
	// defer_to, when) triple). Oldest-first matches the slice's
	// insertion order from Store.Defer.
	if len(r.DeferHistory) > 0 {
		fmt.Fprintf(&b, "\n  defer history (%d entries):\n", len(r.DeferHistory))
		for _, d := range r.DeferHistory {
			fmt.Fprintf(&b, "    - by:        %s\n", d.DeferredBy)
			fmt.Fprintf(&b, "      deferred:  %s\n", d.When.Format(time.RFC3339))
			fmt.Fprintf(&b, "      until:     %s\n", d.DeferTo.Format(time.RFC3339))
		}
	}

	// Terminal-state timestamps.
	if r.ClearedAt != nil {
		fmt.Fprintf(&b, "\n  cleared at:   %s\n", r.ClearedAt.Format(time.RFC3339))
	}
	if r.CancelledAt != nil {
		fmt.Fprintf(&b, "\n  cancelled at: %s\n", r.CancelledAt.Format(time.RFC3339))
	}

	return b.String()
}

// formatLookupElapsed mirrors internal/cli/inbox.go's "Xm/Xh/Xd ago"
// idiom; reused for the Q3.4 activity-since-raised banner. No "ago"
// suffix since the caller phrases it as "active for X since ...".
// ORIGIN[thrum-8kxh]: moved from main.go:2286-2312
// Destination: reminder.go:226-252
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func formatLookupElapsed(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
}

// lastNLines returns the trailing n lines of s (or all lines if fewer
// than n exist). Preserves the original line ordering. Used for
// pane-snapshot preview rendering.
// ORIGIN[thrum-8kxh]: moved from main.go:2317-2323
// Destination: reminder.go:263-269
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func lastNLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// reminderListCmd implements `thrum agent reminder list`. Default
// behavior with no filter flags: target=<self> AND state=open. Filter
// flags override the defaults — e.g. `--state cleared` widens to all
// targets but only cleared rows.
// ORIGIN[thrum-8kxh]: moved from main.go:2329-2351
// Destination: reminder.go:281-303
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func reminderListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List reminders",
		Long: `List reminders matching the given filters.

With no flags: show open reminders targeted at self.

Examples:
  thrum agent reminder list                       # open reminders for self
  thrum agent reminder list --state cleared       # all cleared rows
  thrum agent reminder list --source daemon       # daemon-minted (idle sweep) rows
  thrum agent reminder list --target @other_agent # rows targeted at another agent
  thrum agent reminder list --limit 20            # cap output`,
		RunE: runReminderList,
	}
	cmd.Flags().String("source", "", "filter by source (agent/user/daemon)")
	cmd.Flags().String("state", "", "filter by state (open/fired/cleared/cancelled)")
	cmd.Flags().String("target", "", "filter by target agent (@name)")
	cmd.Flags().String("source-agent", "", "filter by source agent (@name)")
	cmd.Flags().Int("limit", 0, "maximum rows to return (0 = unlimited)")
	return cmd
}

// reminderListFlags captures the parsed flag values. Extracted so
// buildReminderListOpts can be unit-tested without spinning up cobra.
// ORIGIN[thrum-8kxh]: moved from main.go:2355-2361
// Destination: reminder.go:313-319
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
type reminderListFlags struct {
	source      string
	state       string
	target      string
	sourceAgent string
	limit       int
}

// buildReminderListOpts translates parsed flags + the resolved self
// agent into ReminderListOpts. Default behavior: when no scoping
// flag is set, default to target=<self> AND state=open. Any explicit
// filter flag suppresses both defaults — operators asking for
// `--state cleared` want all cleared rows across targets, not "cleared
// rows targeted at me".
// ORIGIN[thrum-8kxh]: moved from main.go:2369-2383
// Destination: reminder.go:333-347
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func buildReminderListOpts(f reminderListFlags, self string) cli.ReminderListOpts {
	opts := cli.ReminderListOpts{
		Source:      f.source,
		State:       f.state,
		TargetAgent: strings.TrimPrefix(f.target, "@"),
		SourceAgent: strings.TrimPrefix(f.sourceAgent, "@"),
		Limit:       f.limit,
	}
	noFilters := f.source == "" && f.state == "" && f.target == "" && f.sourceAgent == ""
	if noFilters {
		opts.TargetAgent = self
		opts.State = "open"
	}
	return opts
}

// ORIGIN[thrum-8kxh]: moved from main.go:2385-2429
// Destination: reminder.go:355-399
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func runReminderList(cmd *cobra.Command, _ []string) error {
	source, _ := cmd.Flags().GetString("source")
	state, _ := cmd.Flags().GetString("state")
	target, _ := cmd.Flags().GetString("target")
	sourceAgent, _ := cmd.Flags().GetString("source-agent")
	limit, _ := cmd.Flags().GetInt("limit")

	if limit < 0 {
		return fmt.Errorf("--limit must be >= 0; got %d", limit)
	}

	self, err := resolveLocalAgentID()
	if err != nil {
		// Self-resolution failure isn't fatal for filtered queries — the
		// caller might be running --target=<other> from a non-agent
		// shell. Only fail if we need self for the default scope.
		if source == "" && state == "" && target == "" && sourceAgent == "" {
			return fmt.Errorf("resolve self for default scope: %w", err)
		}
		self = ""
	}

	opts := buildReminderListOpts(reminderListFlags{
		source:      source,
		state:       state,
		target:      target,
		sourceAgent: sourceAgent,
		limit:       limit,
	}, self)

	client, err := getClient()
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	rows, err := cli.ReminderList(client, opts)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fmt.Println(formatReminderListRow(r))
	}
	return nil
}

// formatReminderListRow renders one line per reminder. Body is
// truncated to 60 chars (full body lives in lookup). Layout per the
// plan §Output format example.
// ORIGIN[thrum-8kxh]: moved from main.go:2434-2446
// Destination: reminder.go:410-422
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func formatReminderListRow(r cli.ReminderRow) string {
	fireAt := "unscheduled"
	if r.NextReminderAt != nil {
		fireAt = r.NextReminderAt.Format(time.RFC3339)
	}
	body := truncateBody(r.Body, 60)
	target := r.TargetAgent
	if target == "" {
		target = "<chain>"
	}
	return fmt.Sprintf("%s   %s %s   target=%s   state=%s   %q",
		r.ID, fireStateLabel(r), fireAt, target, r.State, body)
}

// fireStateLabel collapses the (state, next_reminder_at) pair into a
// short prefix that reads naturally before the timestamp:
//   - state=open with a fire time → "fires"
//   - state=open without a fire time → "open"   (rare: stalled-sweep row pre-rearm)
//   - state=fired                   → "fired"
//   - state=cleared                 → "cleared"
//   - state=cancelled               → "cancelled"
//
// ORIGIN[thrum-8kxh]: moved from main.go:2455-2471
// Destination: reminder.go:437-453
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func fireStateLabel(r cli.ReminderRow) string {
	switch r.State {
	case "open":
		if r.NextReminderAt != nil {
			return "fires"
		}
		return "open"
	case "fired":
		return "fired"
	case "cleared":
		return "cleared"
	case "cancelled":
		return "cancelled"
	default:
		return r.State
	}
}

// truncateBody returns s unchanged if len(s) <= limit; otherwise the
// first limit-3 runes plus "..." (so the total length stays at limit).
// Operates on bytes; reminder bodies are short and ASCII-dominant.
// ORIGIN[thrum-8kxh]: moved from main.go:2476-2484
// Destination: reminder.go:464-472
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func truncateBody(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	if limit <= 3 {
		return s[:limit]
	}
	return s[:limit-3] + "..."
}

// reminderSetCmd implements `thrum agent reminder set`. Flag set per
// brainstorm Q3.6: --at (RFC3339, XOR with --in) | --in (duration,
// XOR with --at) | --body (required) | --target (defaults to self).
// ORIGIN[thrum-8kxh]: moved from main.go:2489-2515
// Destination: reminder.go:483-509
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func reminderSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set a reminder",
		Long: `Set a time-triggered reminder. Fires once at the target time;
the fire message is terse — the body lives in 'thrum agent reminder <id>' lookup.

Examples:
  thrum agent reminder set --in 1h --body "finish release notes"
  thrum agent reminder set --at 2026-05-20T09:00:00Z --target @impl_billing --body "review billing PR"

After the reminder fires, dismiss it with one of:
  thrum agent reminder <id> --clear   (resolved; you saw it and acted on it)
  thrum agent reminder <id> --cancel  (set in error / no longer wanted)

The schema records the difference (cleared_at vs cancelled_at) so a
'thrum agent reminder list --state cleared' or '--state cancelled' query
later shows which is which.`,
		RunE: runReminderSet,
	}
	cmd.Flags().String("at", "", "absolute trigger time (RFC3339, e.g. 2026-05-20T09:00:00Z)")
	cmd.Flags().String("in", "", "relative duration from now (e.g. 1h, 30m, 2d)")
	cmd.Flags().String("body", "", "reminder body (shown in lookup; not in fire message)")
	cmd.Flags().String("target", "", "recipient agent (@name); defaults to self")
	_ = cmd.MarkFlagRequired("body")
	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:2517-2573
// Destination: reminder.go:517-573
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func runReminderSet(cmd *cobra.Command, _ []string) error {
	at, _ := cmd.Flags().GetString("at")
	in, _ := cmd.Flags().GetString("in")
	body, _ := cmd.Flags().GetString("body")
	target, _ := cmd.Flags().GetString("target")

	// XOR: exactly one of --at or --in must be set.
	if (at == "" && in == "") || (at != "" && in != "") {
		return fmt.Errorf("exactly one of --at or --in is required")
	}

	var trigger time.Time
	if at != "" {
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return fmt.Errorf("--at must be RFC3339 (e.g. 2026-05-20T09:00:00Z): %w", err)
		}
		trigger = t.UTC()
	} else {
		d, err := parseFutureDuration(in)
		if err != nil {
			return fmt.Errorf("--in invalid: %w", err)
		}
		trigger = time.Now().UTC().Add(d)
	}
	if trigger.Before(time.Now()) {
		return fmt.Errorf("trigger time %s is in the past", trigger.Format(time.RFC3339))
	}

	self, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("resolve self: %w", err)
	}
	if target == "" {
		target = self
	}
	target = strings.TrimPrefix(target, "@")

	client, err := getClient()
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	res, err := cli.ReminderSet(client, cli.ReminderSetOpts{
		Source:      "agent",
		SourceAgent: self,
		TriggerAt:   trigger,
		TargetAgent: target,
		Body:        body,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Set reminder %s — fires at %s\n", res.ID, trigger.Format(time.RFC3339))
	return nil
}

// parseFutureDuration accepts "<N>d" (days), Go duration strings (e.g.
// "1h", "30m", "2h15m"), and returns a positive duration suitable for
// time.Now().Add(d). Rejects empty input, leading-dash duration, and
// zero / non-positive durations.
//
// Lives here rather than in internal/timeparse because that package's
// existing ParseBefore is past-pointing (now.Add(-d)). If a second
// callsite emerges for future-pointing parsing, lift this to
// internal/timeparse as ParseAfter (dual-review BLOCKING #1 in the
// plan).
// ORIGIN[thrum-8kxh]: moved from main.go:2585-2607
// Destination: reminder.go:591-613
// Tests: cmd/thrum/main_reminder_test.go
// Commit: 0030e046a7
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func parseFutureDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("duration must be positive")
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid day count %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive, got %v", d)
	}
	return d, nil
}
