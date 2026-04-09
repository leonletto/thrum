package cli

import (
	"fmt"
	"strings"
	"time"
)

// CLI-side types mirroring the RPC response types.

// TmuxCreateOptions contains options for the tmux create command.
type TmuxCreateOptions struct {
	Name string
	Cwd  string
}

// TmuxCreateResponse is the response from the tmux.create RPC.
type TmuxCreateResponse struct {
	Session  string `json:"session"`
	Identity any    `json:"identity,omitempty"`
}

// TmuxLaunchOptions contains options for the tmux launch command.
type TmuxLaunchOptions struct {
	Name    string
	Runtime string
}

// TmuxLaunchResponse is the response from the tmux.launch RPC.
type TmuxLaunchResponse struct {
	Session string `json:"session"`
	Runtime string `json:"runtime"`
}

// TmuxSessionInfo describes a managed tmux session.
type TmuxSessionInfo struct {
	Name    string `json:"name"`
	Agent   string `json:"agent,omitempty"`
	Role    string `json:"role,omitempty"`
	Module  string `json:"module,omitempty"`
	State   string `json:"state"`
	Runtime string `json:"runtime,omitempty"`
	Branch  string `json:"branch,omitempty"`
}

// TmuxStatusResponse contains all managed tmux sessions.
type TmuxStatusResponse struct {
	Sessions []TmuxSessionInfo `json:"sessions"`
}

// TmuxCaptureResponse contains captured pane content.
type TmuxCaptureResponse struct {
	Content string `json:"content"`
}

// RPC call functions

// TmuxCreate calls the tmux.create RPC to create a new managed session.
func TmuxCreate(client *Client, opts TmuxCreateOptions) (*TmuxCreateResponse, error) {
	req := map[string]string{"name": opts.Name, "cwd": opts.Cwd}
	var result TmuxCreateResponse
	if err := client.Call("tmux.create", req, &result); err != nil {
		return nil, fmt.Errorf("tmux.create: %w", err)
	}
	return &result, nil
}

// TmuxLaunch calls the tmux.launch RPC to start a runtime in a session.
func TmuxLaunch(client *Client, opts TmuxLaunchOptions) (*TmuxLaunchResponse, error) {
	req := map[string]string{"name": opts.Name, "runtime": opts.Runtime}
	var result TmuxLaunchResponse
	if err := client.Call("tmux.launch", req, &result); err != nil {
		return nil, fmt.Errorf("tmux.launch: %w", err)
	}
	return &result, nil
}

// TmuxStatus calls the tmux.status RPC to list managed sessions.
func TmuxStatus(client *Client) (*TmuxStatusResponse, error) {
	var result TmuxStatusResponse
	if err := client.Call("tmux.status", struct{}{}, &result); err != nil {
		return nil, fmt.Errorf("tmux.status: %w", err)
	}
	return &result, nil
}

// TmuxKill calls the tmux.kill RPC to destroy a session.
func TmuxKill(client *Client, name string) error {
	req := map[string]string{"name": name}
	if err := client.Call("tmux.kill", req, nil); err != nil {
		return fmt.Errorf("tmux.kill: %w", err)
	}
	return nil
}

// TmuxSend calls the tmux.send RPC to send text to a session.
func TmuxSend(client *Client, name, text string) error {
	req := map[string]string{"name": name, "text": text}
	if err := client.Call("tmux.send", req, nil); err != nil {
		return fmt.Errorf("tmux.send: %w", err)
	}
	return nil
}

// TmuxRestartResponse is the response from the tmux.restart RPC.
type TmuxRestartResponse struct {
	Session       string `json:"session"`
	SnapshotLines int    `json:"snapshot_lines"`
}

// TmuxCapture calls the tmux.capture RPC to capture pane content.
func TmuxCapture(client *Client, name string, lines int) (*TmuxCaptureResponse, error) {
	req := map[string]any{"name": name, "lines": lines}
	var result TmuxCaptureResponse
	if err := client.Call("tmux.capture", req, &result); err != nil {
		return nil, fmt.Errorf("tmux.capture: %w", err)
	}
	return &result, nil
}

// TmuxQueueOptions contains options for queuing a command in a tmux session.
type TmuxQueueOptions struct {
	Session          string
	Text             string
	TimeoutMs        int64
	SilenceMs        int64
	NotifyOnComplete *bool // nil = server default (true)
	Requester        string
}

// TmuxQueueResponse is the response from the tmux.queue RPC.
type TmuxQueueResponse struct {
	CommandID string `json:"command_id"`
	Position  int    `json:"position"`
}

// TmuxQueueWaitOptions contains options for waiting on a queued command.
type TmuxQueueWaitOptions struct {
	CommandID string
	TimeoutMs int64
}

// TmuxQueueWaitResponse is the response from the tmux.queue-wait RPC.
type TmuxQueueWaitResponse struct {
	CommandID string `json:"command_id"`
	State     string `json:"state"`
	Output    string `json:"output"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

// TmuxQueueStatusResponse is the response from the tmux.queue-status RPC.
type TmuxQueueStatusResponse struct {
	Session string            `json:"session"`
	Active  *TmuxQueuedView   `json:"active"`
	Queued  []TmuxQueuedView  `json:"queued"`
}

// TmuxQueuedView describes a single command in the queue.
type TmuxQueuedView struct {
	ID    string `json:"ID"`
	Text  string `json:"Text"`
	State string `json:"State"`
}

// TmuxCancelResponse is the response from the tmux.cancel RPC.
type TmuxCancelResponse struct {
	CommandID string `json:"command_id"`
	State     string `json:"state"`
	Output    string `json:"output"`
}

// TmuxQueue calls the tmux.queue RPC to submit a command to a session's queue.
func TmuxQueue(client *Client, opts TmuxQueueOptions) (*TmuxQueueResponse, error) {
	req := map[string]any{
		"session":    opts.Session,
		"text":       opts.Text,
		"timeout_ms": opts.TimeoutMs,
		"requester":  opts.Requester,
	}
	if opts.SilenceMs > 0 {
		req["silence_ms"] = opts.SilenceMs
	}
	if opts.NotifyOnComplete != nil {
		req["notify_on_complete"] = *opts.NotifyOnComplete
	}
	var result TmuxQueueResponse
	if err := client.Call("tmux.queue", req, &result); err != nil {
		return nil, fmt.Errorf("tmux.queue: %w", err)
	}
	return &result, nil
}

// TmuxQueueWait calls the tmux.queue-wait RPC (long-poll) and blocks until
// the command reaches a terminal state or the timeout expires.
func TmuxQueueWait(client *Client, opts TmuxQueueWaitOptions) (*TmuxQueueWaitResponse, error) {
	req := map[string]any{
		"command_id": opts.CommandID,
		"timeout_ms": opts.TimeoutMs,
	}
	var result TmuxQueueWaitResponse
	// Use CallWithTimeout so the socket deadline doesn't fire before the
	// queue's own timeout. Add a small buffer on top of the requested timeout.
	deadline := time.Duration(opts.TimeoutMs)*time.Millisecond + 15*time.Second
	if err := client.CallWithTimeout("tmux.queue-wait", req, &result, deadline); err != nil {
		return nil, fmt.Errorf("tmux.queue-wait: %w", err)
	}
	return &result, nil
}

// TmuxQueueStatus calls the tmux.queue-status RPC to show active and queued commands.
func TmuxQueueStatus(client *Client, session string) (*TmuxQueueStatusResponse, error) {
	req := map[string]string{"session": session}
	var result TmuxQueueStatusResponse
	if err := client.Call("tmux.queue-status", req, &result); err != nil {
		return nil, fmt.Errorf("tmux.queue-status: %w", err)
	}
	return &result, nil
}

// TmuxCancel calls the tmux.cancel RPC to cancel a queued or active command.
func TmuxCancel(client *Client, commandID string) (*TmuxCancelResponse, error) {
	req := map[string]string{"command_id": commandID}
	var result TmuxCancelResponse
	if err := client.Call("tmux.cancel", req, &result); err != nil {
		return nil, fmt.Errorf("tmux.cancel: %w", err)
	}
	return &result, nil
}

// Format functions

// FormatTmuxStatus formats managed sessions as a table.
func FormatTmuxStatus(resp *TmuxStatusResponse) string {
	if len(resp.Sessions) == 0 {
		return "No tmux-managed sessions\n"
	}

	var out strings.Builder
	fmt.Fprintf(&out, "%-25s %-20s %-12s %-10s %s\n",
		"SESSION", "AGENT", "STATE", "RUNTIME", "BRANCH")
	for _, s := range resp.Sessions {
		agentDisplay := s.Agent
		if agentDisplay != "" {
			agentDisplay = "@" + agentDisplay
		}
		fmt.Fprintf(&out, "%-25s %-20s %-12s %-10s %s\n",
			s.Name, agentDisplay, s.State, s.Runtime, s.Branch)
	}
	return out.String()
}

// FormatTmuxCreate formats a session creation success message.
func FormatTmuxCreate(resp *TmuxCreateResponse) string {
	return fmt.Sprintf("Session created: %s\n", resp.Session)
}
