package cli

import (
	"fmt"
	"strings"
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

// TmuxCapture calls the tmux.capture RPC to capture pane content.
func TmuxCapture(client *Client, name string, lines int) (*TmuxCaptureResponse, error) {
	req := map[string]any{"name": name, "lines": lines}
	var result TmuxCaptureResponse
	if err := client.Call("tmux.capture", req, &result); err != nil {
		return nil, fmt.Errorf("tmux.capture: %w", err)
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
		fmt.Fprintf(&out, "%-25s %-20s %-12s %-10s %s\n",
			s.Name, s.Agent, s.State, s.Runtime, s.Branch)
	}
	return out.String()
}

// FormatTmuxCreate formats a session creation success message.
func FormatTmuxCreate(resp *TmuxCreateResponse) string {
	return fmt.Sprintf("Session created: %s\n", resp.Session)
}
