package safecmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
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
// (TMUX/TMUX_PANE and THRUM_* stripped — see cleanTmuxEnv for rationale).
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
// Uses the same clean environment as Tmux (TMUX/TMUX_PANE and THRUM_*
// stripped — see cleanTmuxEnv). Use for commands where only success/failure
// matters (has-session, set-option).
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

// cleanTmuxEnv returns the current environment with TMUX, TMUX_PANE, all
// THRUM_* variables, and CLAUDE_PROJECT_DIR removed.
//
// Three distinct hazards motivate the scrub:
//
//  1. TMUX / TMUX_PANE: the daemon may inherit these from its parent
//     (e.g. tmux-exec), which would cause tmux commands to connect to the
//     wrong server.
//
//  2. THRUM_*: the daemon process inherits its launcher shell's environ. If
//     the launcher had run `thrum prime` (or the user otherwise exported
//     THRUM_AGENT_ID/NAME/ROLE/MODULE/INTENT/HOME), those values poison every
//     tmux session the daemon creates. tmux propagates the client's environ
//     to the new session as default-environment, and panes spawned in that
//     session inherit it — so the pane's runtime CLI commands resolve
//     identity to the daemon-launcher's agent instead of the pane's intended
//     agent. Identity guards (cross_worktree, pid_mismatch,
//     quickstart_name_collision) then fire constantly. See thrum-8nro.4.
//
//  3. CLAUDE_PROJECT_DIR: same shared-tmux-server hazard as THRUM_* but for
//     Claude Code's hook-resolution path. Claude Code reads
//     CLAUDE_PROJECT_DIR to locate per-project hooks
//     (templates/claude/settings.json.tmpl points its hooks at
//     ${CLAUDE_PROJECT_DIR}/scripts/thrum-check-inbox.sh). If repo A starts
//     a tmux session first, repo B's later session inherits A's
//     CLAUDE_PROJECT_DIR, and Claude Code fires A's hook scripts while
//     running in B's pane. That manifested as the kfn3 self-echo phantom:
//     every outbound `thrum send` from a B-pane Claude session produced a
//     'New message from @<self>' system-reminder because A's hook was
//     resolving against the wrong agent identity. See thrum-jj0a.6.
//
//     Scope: ONLY CLAUDE_PROJECT_DIR. Other CLAUDE_* vars (CLAUDE_API_KEY,
//     CLAUDE_CONFIG_DIR, etc.) have no documented leak evidence and may be
//     auth-bearing or required-for-launch — over-scrubbing them could
//     quietly break claude in user environments we haven't tested. The
//     May-8 falcondev recurrence named CLAUDE_PROJECT_DIR specifically; the
//     scrub stays narrow to that evidence.
//
// Scrubbing THRUM_* and CLAUDE_PROJECT_DIR leaves panes to resolve identity
// and project-dir via the design-intended paths (peercred → cwd; harness
// auto-detection from the pane's actual cwd).
func cleanTmuxEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") {
			continue
		}
		if strings.HasPrefix(e, "THRUM_") {
			continue
		}
		if strings.HasPrefix(e, "CLAUDE_PROJECT_DIR=") {
			continue
		}
		env = append(env, e)
	}
	return env
}

// TmuxExec replaces the current process with tmux (via syscall.Exec).
// This is used for tmux attach — the thrum process becomes the tmux client,
// which allows the terminal to see tmux's session/window titles instead of
// "thrum" in tabs. This function never returns on success.
func TmuxExec(args ...string) error {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}
	argv := append([]string{"tmux"}, args...)
	return syscall.Exec(tmuxBin, argv, cleanTmuxEnv()) // #nosec G204 -- args are internal tmux subcommands
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

// buildProbeEnv constructs the environment for an ANONYMOUS git probe from a
// fixed ALLOWLIST — never os.Environ() — so no inherited credential, token, or
// config-injection variable (SSH agent, askpass, netrc via HOME, credential
// helper, GIT_HTTP_EXTRAHEADER bearer token, GIT_CONFIG_* injection) can
// authenticate the probe and make a PRIVATE repo answer like a public one.
// The denylist approach was caught incomplete twice in review; an allowlist is
// structurally complete. A clean empty HOME removes ~/.netrc and ~/.gitconfig
// (which carry credential.helper and url.insteadOf rewrites).
func buildProbeEnv() []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + probeEmptyHome(),
		// Explicit disable flags (belt-and-suspenders on top of the allowlist).
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"SSH_AUTH_SOCK=",
		"GIT_HTTP_EXTRAHEADER=",
		"GIT_SSH_COMMAND=",
	}
	// Carry TLS/cert operational vars ONLY if already set, so HTTPS verification
	// still works in minimal/container environments. These are not auth-bearing.
	for _, k := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE", "SystemRoot"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// probeEmptyHome returns a process-stable empty directory to use as HOME for
// the probe, so git finds no ~/.netrc or ~/.gitconfig. os.MkdirTemp is created
// once and reused; on failure we fall back to os.TempDir() (still not the real
// HOME). The temp dir is intentionally process-lived and never cleaned up — it
// stays empty (git never writes to it), so the leak is a single empty directory.
func probeEmptyHome() string {
	probeHomeOnce.Do(func() {
		d, err := os.MkdirTemp("", "thrum-probe-home-")
		if err != nil {
			probeHomeDir = os.TempDir()
			return
		}
		probeHomeDir = d
	})
	return probeHomeDir
}

var (
	probeHomeOnce sync.Once
	probeHomeDir  string
)

// GitProbeAnonymous runs `git ls-remote <probeURL>` with the hermetic
// allowlist env from buildProbeEnv(). The env allowlist (empty HOME, no system
// gitconfig) is the PRIMARY and complete defence — with an empty HOME git finds
// no ~/.gitconfig, so credential.helper and url.insteadOf rewrites simply do
// not exist to be applied. The inline `-c credential.helper=` is a harmless
// belt-and-suspenders clear. (The plan's `-c url.<probeURL>.insteadOf=` was
// REMOVED: an empty insteadOf value is an empty match-prefix that matches EVERY
// URL and prepends the base, mangling the probe URL — it set a catch-all
// rewrite rather than clearing one. The empty-HOME env already neutralises all
// insteadOf rules structurally.) GitProbeAnonymous is DISTINCT from Git/GitLong,
// which inherit the full environment (and would authenticate the probe). A
// 5-second timeout bounds boot latency (brainstorm §5: ≤5s). Returns the raw
// ls-remote output and the command error verbatim so the caller can classify by
// reachability.
func GitProbeAnonymous(ctx context.Context, probeURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{
		"-c", "credential.helper=",
		"ls-remote", probeURL,
	}
	cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 -- probeURL is a derived https URL; args are fixed git subcommands
	cmd.Env = buildProbeEnv()
	return cmd.CombinedOutput()
}
