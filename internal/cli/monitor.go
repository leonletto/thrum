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
}

// MonitorStartResult is the response from monitor.start.
type MonitorStartResult struct {
	ID string `json:"id"`
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
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
}

// ----- CLI helpers -----

// MonitorStart sends monitor.start over the local daemon socket.
// req.Argv MUST come from cobra's post-'--' args — never from a shell-string-split.
func MonitorStart(client *Client, req MonitorStartRequest) (*MonitorStartResult, error) {
	var result MonitorStartResult
	if err := client.Call("monitor.start", req, &result); err != nil {
		return nil, fmt.Errorf("monitor start: %w", err)
	}
	return &result, nil
}

// MonitorList fetches all monitors and writes a table to out.
func MonitorList(client *Client, out io.Writer) error {
	var jobs []MonitorJobView
	if err := client.Call("monitor.list", struct{}{}, &jobs); err != nil {
		return fmt.Errorf("monitor list: %w", err)
	}

	if len(jobs) == 0 {
		fmt.Fprintln(out, "No monitors running.")
		return nil
	}

	fmt.Fprintf(out, "%-28s %-20s %-10s %s\n", "ID", "NAME", "STATUS", "TARGET")
	for _, j := range jobs {
		fmt.Fprintf(out, "%-28s %-20s %-10s %s\n", j.ID, j.Name, j.Status, j.Target)
	}
	return nil
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

	fmt.Fprintf(out, "ID:       %s\n", job.ID)
	fmt.Fprintf(out, "Name:     %s\n", job.Name)
	fmt.Fprintf(out, "Status:   %s\n", job.Status)
	fmt.Fprintf(out, "Match:    %s\n", job.Match)
	fmt.Fprintf(out, "Target:   %s\n", job.Target)
	fmt.Fprintf(out, "Cwd:      %s\n", job.Cwd)
	fmt.Fprintf(out, "Debounce: %s\n", time.Duration(job.DebounceSeconds)*time.Second)
	fmt.Fprintf(out, "Argv:     %s\n", strings.Join(job.Argv, " "))
	fmt.Fprintf(out, "Created:  %s\n", job.CreatedAt)
	fmt.Fprintf(out, "Updated:  %s\n", job.UpdatedAt)

	// Render env as KEY=<redacted> — values are already redacted by the daemon.
	// This explicit rendering loop is a defence-in-depth: even if the daemon
	// returned a raw value, we only print the key side of each entry together with
	// the literal marker "<redacted>", never the value from the wire.
	if len(job.Env) > 0 {
		keys := make([]string, 0, len(job.Env))
		for k := range job.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintln(out, "Env:")
		for _, k := range keys {
			fmt.Fprintf(out, "  %s=<redacted>\n", k)
		}
	}

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

// MonitorLogs sends monitor.logs for the given monitor ID.
// v1: the daemon returns a not-implemented error; this surfaces it to the user.
func MonitorLogs(client *Client, id string, out io.Writer) error {
	req := struct {
		ID string `json:"id"`
	}{ID: id}

	var result map[string]any
	if err := client.Call("monitor.logs", req, &result); err != nil {
		return fmt.Errorf("monitor logs: %w", err)
	}
	fmt.Fprintln(out, result)
	return nil
}
