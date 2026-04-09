package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// cleanEnv returns the current environment with TMUX and TMUX_PANE removed.
// The daemon may inherit TMUX from its parent (e.g. tmux-exec), which causes
// tmux commands to connect to the wrong server. Stripping these vars ensures
// all commands use the default tmux server socket.
func cleanEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") {
			continue
		}
		env = append(env, e)
	}
	return env
}

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
	cmd := exec.Command("tmux", "has-session", "-t", name) // #nosec G204 -- name is sanitized session name, not user input
	cmd.Env = cleanEnv()
	return cmd.Run() == nil
}

// CreateSession creates a new detached tmux session with a clean environment.
func CreateSession(name, cwd string) error {
	name = SanitizeSessionName(name)
	args := []string{"new-session", "-d", "-s", name}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	cmd := exec.Command("tmux", args...) // #nosec G204 -- args are constructed from sanitized name and validated cwd path
	cmd.Env = cleanEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-session failed: %w: %s", err, out)
	}
	return nil
}

// KillSession destroys a tmux session.
func KillSession(name string) error {
	cmd := exec.Command("tmux", "kill-session", "-t", name) // #nosec G204 -- name is sanitized session name
	cmd.Env = cleanEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux kill-session failed: %w: %s", err, out)
	}
	return nil
}

// SendKeys sends literal text to a tmux target (session:window.pane).
func SendKeys(target, text string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", target, "-l", text) // #nosec G204 -- target is validated tmux target, text sent via -l literal mode
	cmd.Env = cleanEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys failed: %w: %s", err, out)
	}
	return nil
}

// SendSpecialKey sends a named key (Enter, Escape, etc.) to a tmux target.
func SendSpecialKey(target, key string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", target, key) // #nosec G204 -- target is validated, key is a named tmux key constant
	cmd.Env = cleanEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys %s failed: %w: %s", key, err, out)
	}
	return nil
}

// CapturePane captures the visible content of a tmux pane.
// Lines is a positive number specifying how many lines to capture from the bottom.
func CapturePane(target string, lines int) (string, error) {
	startLine := fmt.Sprintf("-%d", lines)
	cmd := exec.Command("tmux", "capture-pane", "-p", "-t", target, "-S", startLine) // #nosec G204 -- target is validated, startLine is numeric
	cmd.Env = cleanEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane failed: %w: %s", err, out)
	}
	return string(out), nil
}

// SessionName returns the session name of the current tmux session (from inside).
// Note: this intentionally does NOT strip TMUX env — it needs the current
// session's TMUX var to know which session it's in.
func SessionName() (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#S").CombinedOutput() // #nosec G204 -- no user input
	if err != nil {
		return "", fmt.Errorf("tmux display-message failed: %w: %s", err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// PaneTarget returns the full target (session:window.pane) for the current pane.
// Note: this intentionally does NOT strip TMUX env — it needs the current
// session's TMUX var to know which pane it's in.
func PaneTarget() (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}:#{window_index}.#{pane_index}").CombinedOutput() // #nosec G204 -- no user input
	if err != nil {
		return "", fmt.Errorf("tmux display-message failed: %w: %s", err, out)
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
	setOpt := exec.Command("tmux", "set-option", "-t", session, // #nosec G204 -- session is sanitized, seconds is numeric
		"monitor-silence", fmt.Sprintf("%d", seconds))
	setOpt.Env = cleanEnv()
	if err := setOpt.Run(); err != nil {
		return fmt.Errorf("set monitor-silence: %w", err)
	}
	hookCmd := fmt.Sprintf("run-shell %s",
		shellQuote(fmt.Sprintf("%s tmux check-pane %s --repo %s",
			shellQuote(thrumBin), shellQuote(session), shellQuote(repoPath))))
	setHook := exec.Command("tmux", "set-hook", "-t", session, // #nosec G204 -- session sanitized, hookCmd built with shellQuote
		"alert-silence", hookCmd)
	setHook.Env = cleanEnv()
	if err := setHook.Run(); err != nil {
		return fmt.Errorf("set alert-silence hook: %w", err)
	}
	return nil
}

// shellQuote wraps a string in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// InTmux returns true if the current process is running inside a tmux session.
func InTmux() bool {
	return os.Getenv("TMUX") != ""
}
