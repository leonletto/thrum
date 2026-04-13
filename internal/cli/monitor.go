package cli

import (
	"encoding/json"
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

	_, _ = fmt.Fprintf(out, "%-28s %-20s %-10s %-20s %-10s %s\n",
		"ID", "NAME", "STATUS", "TARGET", "UPTIME", "PID")
	for _, j := range jobs {
		uptime := "-"
		if j.Status == "running" && !j.CreatedAt.IsZero() {
			uptime = formatUptime(time.Since(j.CreatedAt))
		}
		pidStr := "-"
		if j.PID != nil {
			pidStr = fmt.Sprintf("%d", *j.PID)
		}
		_, _ = fmt.Fprintf(out, "%-28s %-20s %-10s %-20s %-10s %s\n",
			j.ID, j.Name, j.Status, j.Target, uptime, pidStr)
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
// Env values are rendered as KEY=<redacted> — the daemon already redacts them;
// the CLI also ensures no raw secret values appear in its output.
func MonitorShow(client *Client, id string, out io.Writer) error {
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

// MonitorListJSON outputs monitor list as JSON.
func MonitorListJSON(client *Client, includeAll bool, out io.Writer) error {
	req := struct {
		IncludeAll bool `json:"include_all,omitempty"`
	}{IncludeAll: includeAll}

	var jobs []MonitorJobView
	if err := client.Call("monitor.list", req, &jobs); err != nil {
		return fmt.Errorf("monitor list: %w", err)
	}

	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	_, _ = fmt.Fprintln(out, string(data))
	return nil
}

// MonitorShowJSON outputs a single monitor's details as JSON.
func MonitorShowJSON(client *Client, id string, out io.Writer) error {
	req := struct {
		ID string `json:"id"`
	}{ID: id}

	var job MonitorJobView
	if err := client.Call("monitor.show", req, &job); err != nil {
		return fmt.Errorf("monitor show: %w", err)
	}

	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	_, _ = fmt.Fprintln(out, string(data))
	return nil
}

// MonitorStop sends monitor.stop for the given monitor ID.
func MonitorStop(client *Client, id string) error {
	req := struct {
		ID string `json:"id"`
	}{ID: id}

	var result map[string]string
	if err := client.Call("monitor.stop", req, &result); err != nil {
		return fmt.Errorf("monitor stop: %w", err)
	}
	return nil
}

// MonitorRestart sends monitor.restart for the given monitor ID.
// Returns the new monitor ID assigned after the restart.
func MonitorRestart(client *Client, id string) (*MonitorStartResult, error) {
	req := struct {
		ID string `json:"id"`
	}{ID: id}

	var result MonitorStartResult
	if err := client.Call("monitor.restart", req, &result); err != nil {
		return nil, fmt.Errorf("monitor restart: %w", err)
	}
	return &result, nil
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
// value.
func MonitorLogs(client *Client, id string, limit int, out io.Writer) error {
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
