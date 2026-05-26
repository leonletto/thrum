package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// ----- request / response types -----

// MonitorStartRequest holds the parameters for monitor.start.
// Argv MUST come from cobra's post-'--' args — never from a shell-string-split.
type MonitorStartRequest struct {
	Name            string            `json:"name"`
	Argv            []string          `json:"argv"`
	Match           string            `json:"match"`
	Target          string            `json:"target"`
	Cwd             string            `json:"cwd"`
	Env             map[string]string `json:"env"`
	DebounceSeconds int               `json:"debounce_seconds"`
	// Schedule is an optional 5-field cron expression. Empty means
	// continuous mode with auto-restart (thrum-puhr.9).
	Schedule string `json:"schedule,omitempty"`
}

// MonitorStartResult is the response from monitor.start.
type MonitorStartResult struct {
	ID  string `json:"id"`
	PID int    `json:"pid,omitempty"` // 0 until the child has actually started
}

// MonitorJobView is the JSON shape returned by monitor.show and (per element)
// monitor.list. Env values are always redacted by the daemon.
type MonitorJobView struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Argv            []string          `json:"argv"`
	Match           string            `json:"match"`
	Target          string            `json:"target"`
	Cwd             string            `json:"cwd"`
	Env             map[string]string `json:"env"`
	DebounceSeconds int               `json:"debounce_seconds"`
	Status          string            `json:"status"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	PID             *int              `json:"pid,omitempty"` // nil if stopped/dead
	Schedule        string            `json:"schedule,omitempty"`
}

// ----- CLI helpers -----

// MonitorStart sends monitor.start over the local daemon socket.
// Req.Argv MUST come from cobra's post-'--' args — never from a shell-string-split.
func MonitorStart(client *Client, req MonitorStartRequest) (*MonitorStartResult, error) {
	var result MonitorStartResult
	if err := client.Call("monitor.start", req, &result); err != nil {
		return nil, fmt.Errorf("monitor start: %w", err)
	}
	return &result, nil
}

// MonitorList fetches monitors and writes a table to out.
// If includeAll is true, stopped/dead monitors younger than 1 week are
// also included (the daemon does the filtering). Default is running-only.
// Columns: ID / NAME / STATUS / TARGET / UPTIME / PID per design doc
// §'thrum monitor list' (review finding R2.3).
func MonitorList(client *Client, includeAll bool, out io.Writer) error {
	req := struct {
		IncludeAll bool `json:"include_all,omitempty"`
	}{IncludeAll: includeAll}

	var jobs []MonitorJobView
	if err := client.Call("monitor.list", req, &jobs); err != nil {
		return fmt.Errorf("monitor list: %w", err)
	}

	if len(jobs) == 0 {
		_, _ = fmt.Fprintln(out, "No monitors running.")
		return nil
	}

	_, _ = fmt.Fprintf(out, "%-28s %-20s %-10s %-20s %-10s %-8s %s\n",
		"ID", "NAME", "STATUS", "TARGET", "UPTIME", "PID", "SCHEDULE")
	for _, j := range jobs {
		uptime := "-"
		if j.Status == "running" && !j.CreatedAt.IsZero() {
			uptime = formatUptime(time.Since(j.CreatedAt))
		}
		pidStr := "-"
		if j.PID != nil {
			pidStr = fmt.Sprintf("%d", *j.PID)
		}
		sched := j.Schedule
		if sched == "" {
			sched = "-"
		}
		_, _ = fmt.Fprintf(out, "%-28s %-20s %-10s %-20s %-10s %-8s %s\n",
			j.ID, j.Name, j.Status, j.Target, uptime, pidStr, sched)
	}
	return nil
}

// formatUptime returns a compact human-readable duration for a monitor
// uptime cell: "3h42m", "15m", "2d4h", or "<1s" for sub-second values.
func formatUptime(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	minutes := int(d / time.Minute)
	d -= time.Duration(minutes) * time.Minute
	seconds := int(d / time.Second)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

// MonitorShow fetches and renders a single monitor's full spec to out.
// The identifier may be either a monitor ID (mon_<ULID>) or a name; names
// are resolved to IDs via monitor.list before the RPC is dispatched. Show
// is read-only inspection so resolution uses `includeAll=true` — operators
// inspecting historical/stopped monitors by name shouldn't hit a running-only
// filter (see thrum-09wl design notes; sibling of thrum-puhr.9.1's stop/logs
// and thrum-tv6z's restart).
// Env values are rendered as KEY=<redacted> — the daemon already redacts them;
// the CLI also ensures no raw secret values appear in its output.
func MonitorShow(client *Client, identifier string, out io.Writer) error {
	id, err := resolveMonitorIdentifier(client, identifier, true)
	if err != nil {
		return fmt.Errorf("monitor show: %w", err)
	}

	req := struct {
		ID string `json:"id"`
	}{ID: id}

	var job MonitorJobView
	if err := client.Call("monitor.show", req, &job); err != nil {
		return fmt.Errorf("monitor show: %w", err)
	}

	_, _ = fmt.Fprintf(out, "ID:       %s\n", job.ID)
	_, _ = fmt.Fprintf(out, "Name:     %s\n", job.Name)
	_, _ = fmt.Fprintf(out, "Status:   %s\n", job.Status)
	_, _ = fmt.Fprintf(out, "Match:    %s\n", job.Match)
	_, _ = fmt.Fprintf(out, "Target:   %s\n", job.Target)
	_, _ = fmt.Fprintf(out, "Cwd:      %s\n", job.Cwd)
	_, _ = fmt.Fprintf(out, "Debounce: %s\n", time.Duration(job.DebounceSeconds)*time.Second)
	if job.Schedule != "" {
		_, _ = fmt.Fprintf(out, "Schedule: %s\n", job.Schedule)
	}
	_, _ = fmt.Fprintf(out, "Argv:     %s\n", strings.Join(job.Argv, " "))
	_, _ = fmt.Fprintf(out, "Created:  %s\n", job.CreatedAt)
	_, _ = fmt.Fprintf(out, "Updated:  %s\n", job.UpdatedAt)

	// Render env as KEY=<redacted> — values are already redacted by the daemon.
	// This explicit rendering loop is a defense-in-depth: even if the daemon
	// returned a raw value, we only print the key side of each entry together with
	// the literal marker "<redacted>", never the value from the wire.
	if len(job.Env) > 0 {
		keys := make([]string, 0, len(job.Env))
		for k := range job.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		_, _ = fmt.Fprintln(out, "Env:")
		for _, k := range keys {
			_, _ = fmt.Fprintf(out, "  %s=<redacted>\n", k)
		}
	}

	return nil
}

// MonitorListJSON fetches the monitor list and returns the raw slice for
// the caller to emit through cli.EmitJSON. Returning the value (rather than
// printing it here) keeps the slog→hint bridge in the loop: EmitJSON drains
// the pushedHints buffer on the way out and grafts records into the response
// body. Bypassing it would silently discard slog records under --json mode.
func MonitorListJSON(client *Client, includeAll bool) ([]MonitorJobView, error) {
	req := struct {
		IncludeAll bool `json:"include_all,omitempty"`
	}{IncludeAll: includeAll}

	var jobs []MonitorJobView
	if err := client.Call("monitor.list", req, &jobs); err != nil {
		return nil, fmt.Errorf("monitor list: %w", err)
	}
	return jobs, nil
}

// MonitorShowJSON fetches a single monitor's details for emission through
// cli.EmitJSON. See MonitorListJSON for the rationale. The identifier
// resolution semantics match MonitorShow: name lookup uses includeAll=true
// since show is read-only inspection.
func MonitorShowJSON(client *Client, identifier string) (MonitorJobView, error) {
	id, err := resolveMonitorIdentifier(client, identifier, true)
	if err != nil {
		return MonitorJobView{}, fmt.Errorf("monitor show: %w", err)
	}

	req := struct {
		ID string `json:"id"`
	}{ID: id}

	var job MonitorJobView
	if err := client.Call("monitor.show", req, &job); err != nil {
		return MonitorJobView{}, fmt.Errorf("monitor show: %w", err)
	}
	return job, nil
}

// MonitorStop sends monitor.stop for the given monitor identifier and returns
// the resolved monitor ID so callers can surface the canonical reference even
// when the user supplied a name. The identifier may be either a monitor ID
// (mon_<ULID>) or a name; names are resolved to IDs via monitor.list before
// the RPC is dispatched. Stop only considers running monitors when resolving
// names — a stopped monitor's name returns "no running monitor named ..."
// rather than a silent no-op.
func MonitorStop(client *Client, identifier string) (string, error) {
	id, err := resolveMonitorIdentifier(client, identifier, false)
	if err != nil {
		return "", fmt.Errorf("monitor stop: %w", err)
	}

	req := struct {
		ID string `json:"id"`
	}{ID: id}

	var result map[string]string
	if err := client.Call("monitor.stop", req, &result); err != nil {
		return "", fmt.Errorf("monitor stop: %w", err)
	}
	return id, nil
}

// monitorIDPrefix is the canonical prefix for monitor IDs minted by
// internal/daemon/monitor.newMonitorID ("mon_<ULID>", 30 chars total).
const monitorIDPrefix = "mon_"

// isMonitorID reports whether s has the exact shape of a daemon-minted
// monitor ID: the "mon_" prefix followed by a 26-character ULID using
// Crockford-base32's uppercase alphanumeric subset. Validating the shape
// (not just the prefix) prevents a user-supplied name that happens to start
// with "mon_" — e.g., "mon_daily" — from being silently routed to the
// daemon as an ID lookup that returns "not found" with no hint.
func isMonitorID(s string) bool {
	const ulidLen = 26
	if !strings.HasPrefix(s, monitorIDPrefix) {
		return false
	}
	suffix := s[len(monitorIDPrefix):]
	if len(suffix) != ulidLen {
		return false
	}
	for _, r := range suffix {
		if !((r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

// resolveMonitorIdentifier maps a user-supplied identifier (ID or name) to a
// monitor ID. Identifiers matching the daemon's ID shape are returned
// unchanged; anything else is looked up by name via monitor.list.
// includeAll controls whether stopped/dead monitors are searched alongside
// running ones (true for `logs`, since logs are a historical query; false
// for `stop`, which only makes sense against a live monitor).
func resolveMonitorIdentifier(client *Client, identifier string, includeAll bool) (string, error) {
	if isMonitorID(identifier) {
		return identifier, nil
	}
	jobs, err := MonitorListJSON(client, includeAll)
	if err != nil {
		return "", fmt.Errorf("resolve name %q: %w", identifier, err)
	}
	for _, j := range jobs {
		if j.Name == identifier {
			return j.ID, nil
		}
	}
	scope := "running monitor"
	listFlag := ""
	if includeAll {
		scope = "monitor"
		listFlag = " --all"
	}
	return "", fmt.Errorf("no %s named %q (use `thrum monitor list%s` to see available monitors)",
		scope, identifier, listFlag)
}

// MonitorRestart sends monitor.restart for the given monitor identifier and
// returns the resolved monitor ID so callers can surface the canonical
// reference even when the user supplied a name. The identifier may be either
// a monitor ID (mon_<ULID>) or a name; names are resolved to IDs via
// monitor.list before the RPC is dispatched. Restart scopes the lookup to
// running monitors only — mirroring MonitorStop, since "restart"
// semantically operates on a live process. A stopped monitor's name returns
// "no running monitor named ..." rather than silently resurrecting whatever
// dead row happened to match.
//
// HandleRestart preserves the monitor ID (see internal/daemon/rpc/monitor.go),
// so the returned resolvedID is the same ID the daemon now owns. The
// daemon's MonitorStartResult is consumed but not surfaced — the existing
// caller has no use for the (today-equal) result.ID; expand the signature
// if a future caller needs it.
func MonitorRestart(client *Client, identifier string) (string, error) {
	id, err := resolveMonitorIdentifier(client, identifier, false)
	if err != nil {
		return "", fmt.Errorf("monitor restart: %w", err)
	}

	req := struct {
		ID string `json:"id"`
	}{ID: id}

	var result MonitorStartResult
	if err := client.Call("monitor.restart", req, &result); err != nil {
		return "", fmt.Errorf("monitor restart: %w", err)
	}
	return id, nil
}

// MonitorLogEntry matches the daemon's monitorLogEntry shape — one
// historical match record returned by monitor.logs.
type MonitorLogEntry struct {
	MessageID string    `json:"message_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// MonitorLogs fetches the last N recent monitor matches from the messages
// table and renders them as a newline-separated log. Default limit is
// whatever the daemon decides (20) unless the caller passes a non-zero
// value. The identifier may be either a monitor ID (mon_<ULID>) or a name;
// names are resolved to IDs via monitor.list (including stopped/dead
// monitors, since logs are a historical query) before the RPC is dispatched.
func MonitorLogs(client *Client, identifier string, limit int, out io.Writer) error {
	id, err := resolveMonitorIdentifier(client, identifier, true)
	if err != nil {
		return fmt.Errorf("monitor logs: %w", err)
	}

	req := struct {
		ID    string `json:"id"`
		Limit int    `json:"limit,omitempty"`
	}{ID: id, Limit: limit}

	var entries []MonitorLogEntry
	if err := client.Call("monitor.logs", req, &entries); err != nil {
		return fmt.Errorf("monitor logs: %w", err)
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(out, "No matches recorded.")
		return nil
	}
	// Daemon returns DESC (most recent first). Render oldest-first so the
	// log reads like a normal tail.
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		_, _ = fmt.Fprintf(out, "%s  %s\n", e.CreatedAt.Format(time.RFC3339), e.Content)
	}
	return nil
}
