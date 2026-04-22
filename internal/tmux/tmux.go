package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// SanitizeSessionName replaces unsafe characters with hyphens.
func SanitizeSessionName(name string) string {
	return unsafeChars.ReplaceAllString(name, "-")
}

// DefaultSessionName returns the deterministic session name for a role+module.
func DefaultSessionName(role, module string) string {
	return SanitizeSessionName(role + "-" + module)
}

// ParseTarget splits a tmux target "session:window.pane" into parts.
func ParseTarget(target string) (session, window, pane string) {
	session, rest, hasColon := strings.Cut(target, ":")
	if !hasColon {
		return
	}
	window, pane, _ = strings.Cut(rest, ".")
	return
}

// HasSession checks whether a tmux session exists.
func HasSession(name string) bool {
	return safecmd.TmuxRun(context.Background(), "has-session", "-t", name) == nil
}

// CreateSession creates a new detached tmux session with a clean environment.
func CreateSession(name, cwd string) error {
	name = SanitizeSessionName(name)
	args := []string{"new-session", "-d", "-s", name}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	_, err := safecmd.Tmux(context.Background(), args...)
	if err != nil {
		return fmt.Errorf("tmux new-session failed: %w", err)
	}
	return nil
}

// RenameWindow sets the window name for the first window in a session.
func RenameWindow(session, windowName string) error {
	target := session + ":0"
	return safecmd.TmuxRun(context.Background(), "rename-window", "-t", target, windowName)
}

// SetSessionTitle enables terminal title propagation for a session and sets
// the title format. This makes terminal tabs/windows show session-specific
// information instead of the generic "thrum" binary name.
func SetSessionTitle(session, title string) error {
	ctx := context.Background()
	// Enable title propagation for this session
	if err := safecmd.TmuxRun(ctx, "set-option", "-t", session, "set-titles", "on"); err != nil {
		return err
	}
	// Set a static title string for this session
	return safecmd.TmuxRun(ctx, "set-option", "-t", session, "set-titles-string", title)
}

// KillSession destroys a tmux session.
func KillSession(name string) error {
	_, err := safecmd.Tmux(context.Background(), "kill-session", "-t", name)
	if err != nil {
		return fmt.Errorf("tmux kill-session failed: %w", err)
	}
	return nil
}

// SendKeys sends literal text to a tmux target (session:window.pane).
func SendKeys(target, text string) error {
	_, err := safecmd.Tmux(context.Background(), "send-keys", "-t", target, "-l", text)
	if err != nil {
		return fmt.Errorf("tmux send-keys failed: %w", err)
	}
	return nil
}

// SendSpecialKey sends a named key (Enter, Escape, etc.) to a tmux target.
func SendSpecialKey(target, key string) error {
	_, err := safecmd.Tmux(context.Background(), "send-keys", "-t", target, key)
	if err != nil {
		return fmt.Errorf("tmux send-keys %s failed: %w", key, err)
	}
	return nil
}

// CapturePane captures the visible content of a tmux pane.
// Lines is a positive number specifying how many lines to capture from the bottom.
func CapturePane(target string, lines int) (string, error) {
	startLine := fmt.Sprintf("-%d", lines)
	out, err := safecmd.Tmux(context.Background(), "capture-pane", "-p", "-t", target, "-S", startLine)
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane failed: %w", err)
	}
	return string(out), nil
}

// SessionName returns the session name of the current tmux session (from inside).
// Uses TmuxLocal which preserves TMUX env — needed to identify the current session.
func SessionName() (string, error) {
	out, err := safecmd.TmuxLocal(context.Background(), "display-message", "-p", "#S")
	if err != nil {
		return "", fmt.Errorf("tmux display-message failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// PaneTarget returns the full target (session:window.pane) for the current pane.
// Uses TmuxLocal which preserves TMUX env — needed to identify the current pane.
func PaneTarget() (string, error) {
	out, err := safecmd.TmuxLocal(context.Background(), "display-message", "-p", "#{session_name}:#{window_index}.#{pane_index}")
	if err != nil {
		return "", fmt.Errorf("tmux display-message failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SetMonitorSilence configures the silence timeout and alert hook for a session.
// ThrumBin must be an absolute path — tmux run-shell has no $PATH guarantee.
// The thrumBin and repoPath are shell-quoted to prevent injection via paths with spaces.
func SetMonitorSilence(session string, seconds int, thrumBin, repoPath string) error {
	if !filepath.IsAbs(thrumBin) {
		return fmt.Errorf("thrumBin must be an absolute path, got: %q", thrumBin)
	}
	if err := safecmd.TmuxRun(context.Background(), "set-option", "-t", session,
		"monitor-silence", fmt.Sprintf("%d", seconds)); err != nil {
		return fmt.Errorf("set monitor-silence: %w", err)
	}
	hookCmd := fmt.Sprintf("run-shell %s",
		shellQuote(fmt.Sprintf("%s tmux check-pane %s --repo %s",
			shellQuote(thrumBin), shellQuote(session), shellQuote(repoPath))))
	if err := safecmd.TmuxRun(context.Background(), "set-hook", "-t", session,
		"alert-silence", hookCmd); err != nil {
		return fmt.Errorf("set alert-silence hook: %w", err)
	}
	return nil
}

// shellQuote wraps a string in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// IsSilent checks whether the window_silence_flag is set for a session.
// Returns true if the pane has been silent for at least monitor-silence seconds.
func IsSilent(session string) bool {
	out, err := safecmd.Tmux(context.Background(), "display-message", "-t", session, "-p", "#{window_silence_flag}")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

// InTmux returns true if the current process is running inside a tmux session.
func InTmux() bool {
	return os.Getenv("TMUX") != ""
}

// SetUserOption sets a tmux user-option (a name starting with "@") on a
// session. User-options are persisted in tmux's own state for the life
// of the session and can be read back with GetUserOption. Thrum uses
// this to tag sessions it created so they can be discovered from tmux
// state alone — no identity file required (needed for --no-agent
// sessions which have no registered agent).
func SetUserOption(session, key, value string) error {
	return safecmd.TmuxRun(context.Background(), "set-option", "-t", session, key, value)
}

// GetUserOption reads a tmux user-option from a session. Returns the
// raw value on success, and (string, error) on failure — callers
// typically treat any error as "unset" / "not tagged". Trailing
// whitespace is trimmed.
func GetUserOption(session, key string) (string, error) {
	out, err := safecmd.Tmux(context.Background(), "show-option", "-v", "-t", session, key)
	if err != nil {
		return "", fmt.Errorf("tmux show-option %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ListSessions returns the names of all tmux sessions visible on the
// current tmux socket. Empty list is a valid result (no sessions).
// Missing tmux server (exit 1 with "no server running") is returned as
// an empty list, not an error — callers can treat "nothing to list"
// uniformly.
func ListSessions() ([]string, error) {
	out, err := safecmd.Tmux(context.Background(), "list-sessions", "-F", "#{session_name}")
	if err != nil {
		// tmux returns non-zero when no server is running. Map that to
		// an empty list so callers don't need to string-match errors.
		if strings.Contains(err.Error(), "no server running") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}
