package worktree

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

// EnforceOpts configures defense-in-depth checks for
// EnforceOneIdentityWith. The zero value yields legacy keeper-only
// behavior (equivalent to calling EnforceOneIdentity directly).
type EnforceOpts struct {
	// IsPIDAlive, when non-nil, is consulted before quarantining a
	// sibling identity file. If the sibling file has a non-zero
	// AgentPID and this callback returns true for it, quarantine is
	// refused and a warning is logged.
	//
	// This is the thrum-182j defense-in-depth invariant: never
	// quarantine a file whose owning agent is actively running, even
	// if the caller's keeper list did not include them. The keeper
	// list can be incomplete — peercred may mis-resolve the caller
	// (thrum-0pos), or the daemon's session_refs projection may be
	// stale.
	//
	// Best-effort, not atomic: there is a TOCTOU window between
	// readAgentPID (filesystem read) and IsPIDAlive (kernel probe).
	// On a busy kernel the original process may exit and the PID may
	// be reused in between, producing a false-positive "alive"
	// verdict that causes a legitimately stale file to survive one
	// enforcement cycle. The legitimately stale file will be cleaned
	// up on the next enforcement pass once the PID's next owner is
	// observed to differ or exit. macOS allocates PIDs sequentially
	// with a large ceiling (kern.maxproc default 99999), keeping
	// reuse rare; Linux systems with low pid_max (default 32768) and
	// high process churn are the realistic exposure. Pre-prime files
	// (AgentPID == 0) are not protected by this gate, matching G4's
	// pre-prime carveout.
	IsPIDAlive func(pid int) bool

	// CallerCwd is the caller's own working directory. When set, and
	// when AllowCrossWorktree is false, EnforceOneIdentityWith runs
	// a CWD-match check: both CallerCwd and worktreePath are resolved
	// to their git worktree root via `git rev-parse --show-toplevel`.
	// If the roots do not match, the whole enforcement call is
	// refused with a warning — no file is touched.
	//
	// This is the thrum-182j static invariant: a caller may only
	// enforce identity inside its own worktree. The liveness gate
	// (IsPIDAlive) has a temporal blind spot during agent restart
	// (old PID dead, new claude not yet written the new identity);
	// during that window a caller with an arbitrary worktreePath
	// could still quarantine an innocent file. The CWD-match closes
	// that window statically: by the time enforcement runs, the
	// caller's kernel-verified CWD must already point at the target
	// worktree.
	//
	// Empty CallerCwd means "no assertion"; CWD-match is skipped.
	// This preserves the legacy EnforceOneIdentity(path, keep...)
	// signature for callers that never opt in.
	CallerCwd string

	// AllowCrossWorktree, when true, disables the CWD-match check
	// even if CallerCwd is populated. Legitimate callers whose own
	// CWD differs from the target worktree by design (e.g. daemon
	// RPCs that register agents into fresh worktrees) set this to
	// true to bypass the guard.
	AllowCrossWorktree bool
}

// EnsureRedirects verifies and creates .thrum/ and .beads/ redirects
// in a worktree, pointing back to the main repo. Creates identities/ and
// context/ directories in the worktree's local .thrum/.
//
// MainRepo is the absolute path to the main repository root (the one with
// the real .git/ directory, not a .git file). Callers resolve mainRepo
// via cli.IsGitWorktree or from daemon context — this function validates
// both paths exist and sets up redirects.
func EnsureRedirects(worktreePath, mainRepo string) error {
	// Validate worktree path exists
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return fmt.Errorf("worktree does not exist at %s; run 'thrum worktree create <name>' first", worktreePath)
	}

	mainThrumDir := filepath.Join(mainRepo, ".thrum")
	if _, err := os.Stat(mainThrumDir); os.IsNotExist(err) {
		return fmt.Errorf("thrum not initialized in main repo %s; run 'thrum init' first", mainRepo)
	}

	wtThrumDir := filepath.Join(worktreePath, ".thrum")

	// Create .thrum/ directory
	if err := os.MkdirAll(wtThrumDir, 0750); err != nil {
		return fmt.Errorf("create .thrum dir: %w", err)
	}

	// Write or fix redirect
	redirectPath := filepath.Join(wtThrumDir, "redirect")
	redirectContent := mainThrumDir + "\n"
	if existing, err := os.ReadFile(redirectPath); err != nil || strings.TrimSpace(string(existing)) != mainThrumDir { //#nosec G304 -- redirectPath is constructed from known worktree path
		if err == nil && strings.TrimSpace(string(existing)) != mainThrumDir {
			fmt.Fprintf(os.Stderr, "⚠ Fixed .thrum/redirect (was pointing to %s)\n", strings.TrimSpace(string(existing)))
		}
		if err := os.WriteFile(redirectPath, []byte(redirectContent), 0600); err != nil {
			return fmt.Errorf("write thrum redirect: %w", err)
		}
	}

	// Create local directories
	for _, subdir := range []string{"identities", "context"} {
		if err := os.MkdirAll(filepath.Join(wtThrumDir, subdir), 0750); err != nil {
			return fmt.Errorf("create %s dir: %w", subdir, err)
		}
	}

	// Beads redirect (conditional)
	mainBeadsDir := filepath.Join(mainRepo, ".beads")
	if _, err := os.Stat(mainBeadsDir); err == nil {
		wtBeadsDir := filepath.Join(worktreePath, ".beads")
		if err := os.MkdirAll(wtBeadsDir, 0750); err != nil {
			return fmt.Errorf("create .beads dir: %w", err)
		}
		beadsRedirect := filepath.Join(wtBeadsDir, "redirect")
		beadsContent := mainBeadsDir + "\n"
		if existing, err := os.ReadFile(beadsRedirect); err != nil || strings.TrimSpace(string(existing)) != mainBeadsDir { //#nosec G304 -- beadsRedirect is constructed from known worktree path
			if err := os.WriteFile(beadsRedirect, []byte(beadsContent), 0600); err != nil {
				return fmt.Errorf("write beads redirect: %w", err)
			}
		}
	}

	return nil
}

// EnforceOneIdentity enforces the one-identity-per-worktree invariant
// by QUARANTINING sibling identity files to
// .thrum/identities/.quarantine/<name>.json.<RFC3339-ts> instead of
// deleting them. Returns the names of quarantined agents. Context files
// are preserved. Errors are logged but non-fatal.
//
// Accepts one or more agent names to preserve. The first is typically
// the agent being registered; additional names let callers keep the
// peercred-resolved caller's identity too so a bootstrap/test harness
// that registers a differently named agent does not quarantine its own
// identity file (thrum-dw06). Empty names in the keep list are
// silently ignored.
//
// Thrum-ajmd design: the original behavior was os.Remove, which had no
// recourse when it fired on the wrong dir (a non-coordinator agent's
// refresh running with cwd resolving to the main repo path wiped
// coordinator_main.json as a "stale sibling"). Quarantine preserves the
// file so an operator can recover it. The quarantine dir is owned by
// the caller (0o750) and timestamped so repeated enforcement does not
// overwrite previous quarantined copies.
func EnforceOneIdentity(worktreePath string, keep ...string) []string {
	return EnforceOneIdentityWith(worktreePath, EnforceOpts{}, keep...)
}

// EnforceOneIdentityWith is the explicit-options variant of
// EnforceOneIdentity. The zero-value EnforceOpts matches the legacy
// keeper-only behavior; non-nil opts.IsPIDAlive adds the thrum-182j
// defense-in-depth gate that refuses to quarantine a file whose
// owning agent is currently alive, and a non-empty opts.CallerCwd
// (with opts.AllowCrossWorktree == false) adds the static CWD-match
// invariant that refuses the whole call when the caller's worktree
// differs from the target.
func EnforceOneIdentityWith(worktreePath string, opts EnforceOpts, keep ...string) []string {
	// CWD-match gate (thrum-182j static invariant). Runs before any
	// filesystem read so a cross-worktree call is rejected outright —
	// no identity file is read, no .quarantine/ dir is created. The
	// legacy EnforceOneIdentity wrapper passes an empty CallerCwd and
	// skips this gate; production daemon callers populate CallerCwd
	// from peercred-verified state.
	if !opts.AllowCrossWorktree && opts.CallerCwd != "" {
		callerRoot, err := gitToplevel(opts.CallerCwd)
		if err != nil {
			slog.Warn("worktree.EnforceOneIdentity refused: cannot resolve caller cwd to git toplevel",
				slog.String("caller_cwd", opts.CallerCwd),
				slog.String("target", worktreePath),
				slog.String("error", err.Error()))
			return nil
		}
		targetRoot, err := gitToplevel(worktreePath)
		if err != nil {
			slog.Warn("worktree.EnforceOneIdentity refused: cannot resolve target path to git toplevel",
				slog.String("caller_cwd", opts.CallerCwd),
				slog.String("target", worktreePath),
				slog.String("error", err.Error()))
			return nil
		}
		if callerRoot != targetRoot {
			slog.Warn("worktree.EnforceOneIdentity refused: cross-worktree enforcement not permitted",
				slog.String("caller_cwd", opts.CallerCwd),
				slog.String("caller_root", callerRoot),
				slog.String("target", worktreePath),
				slog.String("target_root", targetRoot))
			return nil
		}
	}

	idDir := filepath.Join(worktreePath, ".thrum", "identities")
	entries, err := os.ReadDir(idDir)
	if err != nil {
		return nil
	}

	keepFiles := make(map[string]struct{}, len(keep))
	for _, name := range keep {
		if name == "" {
			continue
		}
		keepFiles[name+".json"] = struct{}{}
	}

	var quarantined []string
	var quarantineDir string // lazily created on first quarantine
	ts := time.Now().UTC().Format("20060102T150405Z")

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if _, ok := keepFiles[entry.Name()]; ok {
			continue
		}
		src := filepath.Join(idDir, entry.Name())

		// Defense-in-depth (thrum-182j): if the candidate file
		// asserts a live agent PID, refuse to quarantine it.
		// Pre-prime files (pid == 0) bypass this gate and are
		// treated as ordinary stale siblings.
		if opts.IsPIDAlive != nil {
			if pid := readAgentPID(src); pid > 0 && opts.IsPIDAlive(pid) {
				slog.Warn("worktree.EnforceOneIdentity refusing to quarantine live identity",
					slog.String("file", entry.Name()),
					slog.Int("pid", pid),
					slog.String("target", worktreePath))
				continue
			}
		}

		if quarantineDir == "" {
			quarantineDir = filepath.Join(idDir, ".quarantine")
			if err := os.MkdirAll(quarantineDir, 0o750); err != nil {
				slog.Warn("worktree.EnforceOneIdentity could not create quarantine dir",
					slog.String("target", worktreePath),
					slog.String("error", err.Error()))
				continue
			}
		}
		dst := filepath.Join(quarantineDir, entry.Name()+"."+ts)
		if err := os.Rename(src, dst); err != nil {
			slog.Warn("worktree.EnforceOneIdentity could not quarantine stale identity",
				slog.String("file", entry.Name()),
				slog.String("error", err.Error()))
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		slog.Info("worktree.EnforceOneIdentity quarantined stale identity",
			slog.String("agent", name),
			slog.String("dst", dst))
		quarantined = append(quarantined, name)
	}

	return quarantined
}

// gitToplevel resolves a directory to its canonical git worktree root
// via `git rev-parse --show-toplevel`. Used by the CWD-match gate so a
// caller passing a subdirectory still resolves to the worktree root
// that can be compared against the enforcement target.
//
// Routed through internal/daemon/safecmd.Git per the project-wide
// convention for daemon-reachable git invocations (5s timeout, injected
// user.name/user.email for commit paths — harmless for rev-parse,
// consistent shape for review). There is no import cycle: safecmd's
// own imports are stdlib-only and no file under internal/daemon/safecmd
// references internal/worktree.
//
// Returns an error if the path is not a git worktree, git is missing,
// or the command times out.
func gitToplevel(path string) (string, error) {
	out, err := safecmd.Git(context.Background(), path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", fmt.Errorf("git rev-parse returned empty toplevel")
	}
	// Canonicalize the same way state_query.canonWorktreePath does so
	// /tmp/... and /private/tmp/... aliases on macOS compare equal.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved, nil
	}
	return filepath.Clean(root), nil
}

// readAgentPID extracts the agent_pid field from an identity file
// without pulling in the config package (which would create an import
// cycle: config → worktree already exists in other directions). Returns
// 0 when the file is unreadable, malformed, or does not declare a PID.
// The caller treats a zero return as "no live assertion, fall through
// to normal quarantine".
func readAgentPID(path string) int {
	data, err := os.ReadFile(path) // #nosec G304 -- path is .thrum/identities/<name>.json under caller's worktree
	if err != nil {
		return 0
	}
	var probe struct {
		AgentPID int `json:"agent_pid"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return 0
	}
	return probe.AgentPID
}

// BuildQuickstartCmd constructs a shell-safe thrum quickstart command string
// for injection into a tmux pane. All values are single-quote wrapped.
// Single quotes within values are escaped as '\” (end quote, escaped quote,
// start quote). --force is always included for idempotent re-registration.
//
// noAgentPID, when true, appends --no-agent-pid so the inline quickstart
// persists agent_pid=0 instead of the caller's (short-lived subshell) PID.
// Required for the tmux-create inline invocation — without it, HandleLaunch
// trips G4 writer-liveness on a dead subshell PID (thrum-x6e8.6).
//
// repoPath, when non-empty, prepends `--repo <path>` so the inline quickstart
// resolves identity-write paths against the explicitly-supplied worktree
// instead of the daemon-inherited THRUM_HOME. Without this, panes spawned by
// the daemon inherit THRUM_HOME from the user's shell at daemon-start, and
// EffectiveRepoPath in the quickstart cobra handler hijacks flagRepo to
// THRUM_HOME — silently writing the new agent's identity into THRUM_HOME's
// .thrum/identities/ instead of the calling worktree (thrum-tc4w).
func BuildQuickstartCmd(repoPath, name, role, module, intent, runtime string, noAgentPID bool) string {
	var parts []string
	parts = append(parts, "thrum")
	if repoPath != "" {
		parts = append(parts, "--repo", shellQuote(repoPath))
	}
	parts = append(parts, "quickstart")
	parts = append(parts, "--name", shellQuote(name))
	parts = append(parts, "--role", shellQuote(role))
	parts = append(parts, "--module", shellQuote(module))

	if intent != "" {
		parts = append(parts, "--intent", shellQuote(intent))
	}
	if runtime != "" {
		parts = append(parts, "--runtime", shellQuote(runtime))
	}

	parts = append(parts, "--force")
	if noAgentPID {
		parts = append(parts, "--no-agent-pid")
	}

	return strings.Join(parts, " ")
}

// shellQuote wraps a value in single quotes, escaping any internal single
// quotes with the '\” idiom (end quote, literal quote, restart quote).
func shellQuote(s string) string {
	escaped := strings.ReplaceAll(s, "'", `'\''`)
	return "'" + escaped + "'"
}
