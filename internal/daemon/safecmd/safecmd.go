package safecmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// gitConfigArgs are injected before every git command so that commits
// in the internal a-sync worktree succeed even when the host machine
// has no global user.name / user.email configured.
var gitConfigArgs = []string{"-c", "user.name=Thrum", "-c", "user.email=thrum@local"}

// Git runs a git command with a 5-second timeout.
// All daemon-side git operations must use this instead of exec.Command("git", ...).
func Git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	fullArgs := append(gitConfigArgs, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...) // #nosec G204 -- args are internal git subcommands from callers, not user input
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %v in %s: %w (output: %s)", args, dir, err, out)
	}
	return out, nil
}

// GitLong runs git commands that involve network I/O (push, fetch) with a 10-second timeout.
func GitLong(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fullArgs := append(gitConfigArgs, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...) // #nosec G204 -- args are internal git subcommands from callers, not user input
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %v in %s: %w (output: %s)", args, dir, err, out)
	}
	return out, nil
}

// WorktreePaths returns the absolute paths of all git worktrees for the repo at dir.
func WorktreePaths(ctx context.Context, dir string) []string {
	out, err := Git(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return []string{dir}
	}

	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return []string{dir}
	}
	return paths
}

// Tmux runs a tmux command with a 5-second timeout and clean environment
// (TMUX/TMUX_PANE stripped to avoid connecting to the wrong server).
// All tmux operations should use this instead of exec.Command("tmux", ...).
func Tmux(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", args...) // #nosec G204 -- args are internal tmux subcommands from callers, not user input
	cmd.Env = cleanTmuxEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("tmux %v: %w (output: %s)", args, err, out)
	}
	return out, nil
}

// TmuxRun runs a tmux command with a 5-second timeout, discarding output.
// Use for commands where only success/failure matters (has-session, set-option).
func TmuxRun(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", args...) // #nosec G204 -- args are internal tmux subcommands from callers, not user input
	cmd.Env = cleanTmuxEnv()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux %v: %w", args, err)
	}
	return nil
}

// cleanTmuxEnv returns the current environment with TMUX and TMUX_PANE removed.
// The daemon may inherit TMUX from its parent (e.g. tmux-exec), which causes
// tmux commands to connect to the wrong server.
func cleanTmuxEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") {
			continue
		}
		env = append(env, e)
	}
	return env
}

// TmuxLocal runs a tmux command that needs the current session's TMUX env
// vars (e.g. display-message from inside a pane). Unlike Tmux/TmuxRun, this
// does NOT strip TMUX/TMUX_PANE from the environment.
func TmuxLocal(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", args...) // #nosec G204 -- args are internal tmux subcommands from callers, not user input
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("tmux %v: %w (output: %s)", args, err, out)
	}
	return out, nil
}

// GitConfig runs "git config --get <key>" with a 5-second timeout.
//
// Unlike Git/GitLong, this wrapper intentionally does NOT inject the thrum
// user.name / user.email overrides, because those would mask the real
// user-level values for keys like "user.name" and "user.email". Use this
// when you need to read the effective git config rather than mutate it.
//
// If the key is not set (git exits with status 1 and no output), returns
// ("", nil) rather than an error — callers can distinguish "not set" from
// "lookup failed" by checking for a non-nil error.
func GitConfig(ctx context.Context, dir, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "config", "--get", key) // #nosec G204 -- hardcoded git binary; key is an internal config key name from callers
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// `git config --get` exits 1 when the key is not set; treat that as "" with no error.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 && len(out) == 0 {
			return "", nil
		}
		return "", fmt.Errorf("git config --get %s in %s: %w", key, dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}
