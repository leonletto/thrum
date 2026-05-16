package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/context/roleconfig"
)

// WizardConfig holds inputs to the wizard run. Constructed by cmd/thrum's
// init command from CLI flags + repo path. (CLI wiring lands in Epic D,
// thrum-75rw.1; until then RunWizard is reachable only via direct calls,
// currently exercised by tests.)
type WizardConfig struct {
	RepoPath      string
	Prompter      Prompter
	NameFlag      string
	RoleFlag      string
	ModuleFlag    string
	WorktreesRoot string
	RolesChoice   string // "enhanced" | "default" | "skip" | ""
	NoDaemon      bool
	Force         bool
	Stealth       bool
	Runtime       string

	// gitignoreSnapshot / excludeSnapshot capture the files Init() will
	// append to so rollback can restore them byte-for-byte. nil means
	// "file did not exist before Init"; in that case rollback removes the
	// file rather than restoring nil bytes. Populated by snapshotGitFiles
	// before Init runs.
	gitignoreSnapshot  []byte
	gitignoreExisted   bool
	gitExcludeSnapshot []byte
	gitExcludeExisted  bool
}

// WizardIdentity is the result of Step 3 (identity prompts).
type WizardIdentity struct {
	Name   string
	Role   string
	Module string
}

// stepIdentity runs Step 3 of the wizard: collect agent name, role, module.
// CLI flags take precedence over prompts, which fall back to repo-derived
// defaults when the user accepts (empty input).
func stepIdentity(cfg *WizardConfig) (WizardIdentity, error) {
	repoName := filepath.Base(cfg.RepoPath)
	defaultName := "coord_" + sanitize(repoName)
	defaultRole := "coordinator"
	defaultModule := DefaultModule(cfg.RepoPath)

	name := cfg.NameFlag
	if name == "" {
		v, err := cfg.Prompter.String(PromptAgentName, "Agent name", defaultName)
		if err != nil {
			return WizardIdentity{}, err
		}
		name = v
	}
	role := cfg.RoleFlag
	if role == "" {
		v, err := cfg.Prompter.String(PromptRole, "Role", defaultRole)
		if err != nil {
			return WizardIdentity{}, err
		}
		role = v
	}
	module := cfg.ModuleFlag
	if module == "" {
		v, err := cfg.Prompter.String(PromptModule, "Module", defaultModule)
		if err != nil {
			return WizardIdentity{}, err
		}
		module = v
	}
	return WizardIdentity{Name: name, Role: role, Module: module}, nil
}

var sanitizeRE = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// sanitize lowercases s and replaces any non-alphanumeric/underscore/dash
// character with a dash so a repo basename is safe to use inside a default
// agent name. Lowercasing matches identity.ValidateAgentName, which rejects
// capital letters — without it, a repo dir like "316Redesign" would yield a
// default of "coord_316Redesign" that the validator immediately rejects.
func sanitize(s string) string {
	return sanitizeRE.ReplaceAllString(strings.ToLower(s), "-")
}

// stepWorktreesRoot runs Step 4: pick the directory under which agent
// worktrees are created. Default is ~/.thrum/worktrees/<repo>. Validates
// that the chosen path is absolute and outside the repo, then creates it.
func stepWorktreesRoot(cfg *WizardConfig) (string, error) {
	home, _ := os.UserHomeDir()
	repoName := filepath.Base(cfg.RepoPath)
	defaultPath := filepath.Join(home, ".thrum", "worktrees", repoName)

	chosen := cfg.WorktreesRoot
	if chosen == "" {
		v, err := cfg.Prompter.String(PromptWorktreesRoot, "Where should agent worktrees live?", defaultPath)
		if err != nil {
			return "", err
		}
		chosen = v
	}
	chosen, err := expandTilde(chosen)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(chosen) {
		return "", fmt.Errorf("worktrees root must be an absolute path: %q", chosen)
	}
	repoAbs, _ := filepath.Abs(cfg.RepoPath)
	if strings.HasPrefix(chosen+string(filepath.Separator), repoAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("worktrees root must not be inside the repo: %q is inside %q", chosen, repoAbs)
	}
	if err := os.MkdirAll(chosen, 0o750); err != nil {
		return "", fmt.Errorf("failed to create worktrees root %s: %w", chosen, err)
	}
	return chosen, nil
}

// daemonAction is the operation chosen by Step 6 based on existing daemon
// state plus the wizard's --force flag.
type daemonAction int

const (
	daemonActionStart daemonAction = iota
	daemonActionRestart
	daemonActionSkip
)

// decideDaemonAction implements the Step 6 decision matrix:
//   - daemon not running          → start
//   - running, no --force         → skip (leave the existing daemon alone)
//   - running, --force            → restart (pick up fresh config)
func decideDaemonAction(running, force bool) daemonAction {
	if !running {
		return daemonActionStart
	}
	if force {
		return daemonActionRestart
	}
	return daemonActionSkip
}

// isDaemonRunning is a thin wrapper over DaemonStatus. Any error is treated
// as "not running" so the wizard falls through to start the daemon rather
// than refusing to proceed when the status RPC is unreachable.
func isDaemonRunning(repoPath string) bool {
	status, err := DaemonStatus(repoPath)
	if err != nil || status == nil {
		return false
	}
	return status.Running
}

// stepDaemon runs Step 6: start, restart, or skip the per-repo daemon
// based on existing state and --force. Honors --no-daemon by returning nil
// without touching the daemon at all.
func stepDaemon(cfg *WizardConfig) error {
	if cfg.NoDaemon {
		return nil
	}
	switch decideDaemonAction(isDaemonRunning(cfg.RepoPath), cfg.Force) {
	case daemonActionStart:
		return DaemonStart(cfg.RepoPath, true, cfg.Force)
	case daemonActionRestart:
		return DaemonRestart(cfg.RepoPath, true, cfg.Force)
	case daemonActionSkip:
		return nil
	}
	return nil
}

// loadReInitDefaults pre-seeds cfg fields from existing .thrum/ state when
// running with --force (re-init mode). Each step's prompt fallback reads
// cfg.NameFlag / cfg.RoleFlag / cfg.ModuleFlag / cfg.WorktreesRoot, so
// pressing enter on every prompt becomes a no-op refresh of existing values.
//
// Errors loading existing state are intentionally swallowed: a partial or
// missing config is the same as a fresh repo for re-init purposes — the
// prompts will fall back to generic defaults instead of pre-seeded values.
// Per-flag CLI overrides win over re-init seeds (already-set fields are
// not overwritten). The signature returns no error to make the swallow
// contract explicit at the type level.
func loadReInitDefaults(cfg *WizardConfig) {
	if !cfg.Force {
		return
	}
	thrumDir := filepath.Join(cfg.RepoPath, ".thrum")
	if existingCfg, err := config.LoadThrumConfig(thrumDir); err == nil && existingCfg != nil {
		if cfg.WorktreesRoot == "" {
			cfg.WorktreesRoot = existingCfg.Worktrees.BasePath
		}
	} else if err != nil {
		slog.Debug("wizard.reinit: config load failed; using generic defaults", "err", err)
	}
	if id, _, err := config.LoadIdentityWithPath(cfg.RepoPath); err == nil && id != nil {
		if cfg.NameFlag == "" {
			cfg.NameFlag = id.Agent.Name
		}
		if cfg.RoleFlag == "" {
			cfg.RoleFlag = id.Agent.Role
		}
		if cfg.ModuleFlag == "" {
			cfg.ModuleFlag = id.Agent.Module
		}
	} else if err != nil {
		slog.Debug("wizard.reinit: identity load failed; using generic defaults", "err", err)
	}
}

// snapshotGitFiles captures .gitignore and .git/info/exclude before Init()
// modifies them so rollback can restore them byte-for-byte. Missing files
// are recorded as "did not exist", causing rollback to remove a file Init
// created rather than writing back nil bytes.
func snapshotGitFiles(cfg *WizardConfig) {
	gi := filepath.Join(cfg.RepoPath, ".gitignore")
	if data, err := os.ReadFile(gi); err == nil { // #nosec G304 -- gi is repoPath/.gitignore, controlled by wizard caller
		cfg.gitignoreSnapshot = data
		cfg.gitignoreExisted = true
	}
	ex := filepath.Join(cfg.RepoPath, ".git", "info", "exclude")
	if data, err := os.ReadFile(ex); err == nil { // #nosec G304 -- ex is repoPath/.git/info/exclude, controlled by wizard caller
		cfg.gitExcludeSnapshot = data
		cfg.gitExcludeExisted = true
	}
}

// restoreSnapshot rewrites a single file from a captured snapshot, removing
// it when the snapshot recorded "did not exist before Init".
func restoreSnapshot(path string, data []byte, existedBefore bool) {
	if existedBefore {
		_ = os.WriteFile(path, data, 0o600)
		return
	}
	_ = os.Remove(path)
}

// rollback returns the repo to its pre-wizard state when Steps 3-5 fail:
// removes .thrum/ and restores .gitignore / .git/info/exclude from
// snapshots taken before Init() ran. Step 6 (daemon) failures are
// recoverable and do NOT trigger rollback.
func rollback(cfg *WizardConfig, cause error) error {
	thrumDir := filepath.Join(cfg.RepoPath, ".thrum")
	_ = os.RemoveAll(thrumDir)
	// Both files are restored unconditionally regardless of cfg.Stealth.
	// When Stealth is true Init only touches .git/info/exclude, so the
	// .gitignore restore is a no-op (snapshot equals current state). This
	// is intentional belt-and-suspenders: branching on cfg.Stealth here
	// would duplicate the stealth/non-stealth dispatch already in Init.
	restoreSnapshot(filepath.Join(cfg.RepoPath, ".gitignore"),
		cfg.gitignoreSnapshot, cfg.gitignoreExisted)
	restoreSnapshot(filepath.Join(cfg.RepoPath, ".git", "info", "exclude"),
		cfg.gitExcludeSnapshot, cfg.gitExcludeExisted)
	return fmt.Errorf("wizard failed: %w (rolled back .thrum/)", cause)
}

// persistWorktreesBasePath writes the chosen worktrees root into
// .thrum/config.json under the existing Worktrees.BasePath key.
func persistWorktreesBasePath(repoPath, basePath string) error {
	thrumDir := filepath.Join(repoPath, ".thrum")
	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return err
	}
	cfg.Worktrees.BasePath = basePath
	return config.SaveThrumConfig(thrumDir, cfg)
}

// RunWizard is the top-level wizard driver. Sequence: tmux gate → Init
// scaffold (.thrum/) → identity (Step 3) + register via Quickstart →
// worktrees root (Step 4) → role templates (Step 5) → daemon (Step 6).
// Failures in Steps 3-5 trigger rollback; daemon failure is reported but
// leaves a valid .thrum/ behind.
func RunWizard(cfg *WizardConfig) error {
	if err := tmuxGate(os.Stderr); err != nil {
		return err
	}

	// Re-init mode reads existing .thrum/ before Init's --force re-scaffold
	// can touch it, so prompt defaults reflect the user's prior choices.
	loadReInitDefaults(cfg)

	// Snapshot must happen BEFORE the SIGINT handler is installed so the
	// goroutine never sees zero snapshots if Ctrl-C arrives in the gap
	// between Notify and the snapshot call.
	snapshotGitFiles(cfg)

	// SIGINT handler runs the same cleanup as a Step 3-5 error path so
	// Ctrl-C mid-wizard never leaves a half-scaffolded .thrum/ behind. The
	// done channel terminates the goroutine on normal return — signal.Stop
	// alone does not close sigCh, which would leak the goroutine forever.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	done := make(chan struct{})
	defer func() {
		signal.Stop(sigCh)
		close(done)
	}()
	go func() {
		select {
		case <-sigCh:
			_ = rollback(cfg, fmt.Errorf("interrupted"))
			os.Exit(130)
		case <-done:
		}
	}()

	if err := Init(InitOptions{
		RepoPath: cfg.RepoPath,
		Force:    cfg.Force,
		Stealth:  cfg.Stealth,
	}); err != nil {
		return err
	}

	// Step 3: collect identity (defer Quickstart until the daemon is up).
	id, err := stepIdentity(cfg)
	if err != nil {
		return rollback(cfg, err)
	}

	// Step 4 + 5: persist worktrees-root and write role templates. Both are
	// pure filesystem writes and do not require a running daemon.
	wtRoot, err := stepWorktreesRoot(cfg)
	if err != nil {
		return rollback(cfg, err)
	}
	if err := persistWorktreesBasePath(cfg.RepoPath, wtRoot); err != nil {
		return rollback(cfg, err)
	}

	if err := stepRoleTemplates(cfg); err != nil {
		return rollback(cfg, err)
	}

	// Step 6: bring the daemon up so Step 6b's Quickstart RPC can land.
	// --no-daemon short-circuits both Step 6 and Step 6b: the wizard
	// returns a fully-scaffolded .thrum/ but no registered identity and
	// no running daemon. Operators in that mode register an agent and
	// start the daemon manually afterwards. This shape is also what the
	// E2E scenarios use to keep test fixtures clean.
	if cfg.NoDaemon {
		fmt.Fprintln(os.Stderr,
			"--no-daemon: skipping daemon start and identity registration. "+
				"Run `thrum daemon start` and `thrum quickstart` to finish setup.")
		return nil
	}

	if err := stepDaemon(cfg); err != nil {
		fmt.Fprintf(os.Stderr,
			"Daemon failed to start: %v\nConfiguration is complete. Start the daemon manually with: thrum daemon start, then `thrum quickstart` to register an agent.\n",
			err)
		return nil
	}

	// Step 6b: register the identity collected in Step 3 by re-invoking the
	// thrum binary's `quickstart` subcommand. We deliberately shell out
	// rather than calling cli.Quickstart directly because the library-level
	// function does only the daemon RPC — the cobra handler in
	// cmd/thrum/main.go owns the post-RPC identity-file write, tmux
	// backfill, context-file bootstrap, and preamble materialization. Those
	// are not exported as a single helper today, and inlining ~80 lines of
	// the handler into the wizard would duplicate subtle logic. Shelling
	// out keeps a single source of truth for "what a quickstart writes".
	if err := runQuickstartSubprocess(cfg, id); err != nil {
		fmt.Fprintf(os.Stderr,
			"Quickstart failed: %v\nRun `thrum quickstart --name %s --role %s --module %s` to retry identity registration.\n",
			err, id.Name, id.Role, id.Module)
		return nil
	}
	return nil
}

// runQuickstartSubprocess re-invokes the running thrum binary with
// `quickstart` so the cobra handler's identity-file-write logic fires.
// Stdout/stderr are forwarded so the user sees the canonical quickstart
// output as the wizard's final step.
func runQuickstartSubprocess(cfg *WizardConfig, id WizardIdentity) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate thrum binary: %w", err)
	}
	args := []string{
		"--repo", cfg.RepoPath,
		"quickstart",
		"--name", id.Name,
		"--role", id.Role,
		"--module", id.Module,
	}
	if cfg.Force {
		args = append(args, "--force")
	}
	if cfg.Runtime != "" {
		args = append(args, "--runtime", cfg.Runtime)
	}
	cmd := exec.Command(self, args...) // #nosec G204 -- self is os.Executable, args are wizard inputs already validated upstream
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// stepRoleTemplates runs Step 5: write per-role markdown templates under
// .thrum/role_templates/ based on a 3-way enhanced/default/skip choice.
// Existing files are preserved unless --force or the user confirms overwrite.
func stepRoleTemplates(cfg *WizardConfig) error {
	choice := cfg.RolesChoice
	if choice == "" {
		idx, err := cfg.Prompter.Choice(PromptRoleTemplates,
			"Apply role templates?",
			[]string{
				"enhanced (recommended): coordinator-autonomous, implementer-worktree-write-only, orchestrator",
				"default: stock runtime templates (coordinator-strict, implementer-strict, orchestrator)",
				"skip",
			}, 0)
		if err != nil {
			return err
		}
		choice = []string{"enhanced", "default", "skip"}[idx]
	}
	if choice == "skip" {
		return nil
	}
	var variants map[string]string
	switch choice {
	case "enhanced":
		variants = map[string]string{
			"coordinator":  "autonomous",
			"implementer":  "worktree-write-only",
			"orchestrator": "",
		}
	case "default":
		variants = map[string]string{
			"coordinator":  "strict",
			"implementer":  "strict",
			"orchestrator": "",
		}
	default:
		return fmt.Errorf("invalid roles choice: %q", choice)
	}
	destDir := filepath.Join(cfg.RepoPath, ".thrum", "role_templates")
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return err
	}
	for role, autonomy := range variants {
		dst := filepath.Join(destDir, role+".md")
		if _, err := os.Stat(dst); err == nil && !cfg.Force {
			ok, err := cfg.Prompter.Confirm(PromptOverwriteRoleTemplate,
				fmt.Sprintf("%s already exists; overwrite?", dst), false)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
		}
		data, err := roleconfig.ReadShippedTemplate(role, autonomy)
		if err != nil {
			return fmt.Errorf("read shipped template (%s, %s): %w", role, autonomy, err)
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// expandTilde expands a leading "~" or "~/" to the user's home directory.
// Other "~user" forms are returned unchanged (we don't resolve other users).
func expandTilde(p string) (string, error) {
	if !strings.HasPrefix(p, "~") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if p == "~" {
		return home, nil
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// tmuxGate verifies tmux is on PATH. Returns an error message with
// install suggestions when missing. Called as Step 0 of the wizard
// before any filesystem changes. Successful detection writes a
// confirmation line to out (typically os.Stderr).
func tmuxGate(out io.Writer) error {
	if path, err := exec.LookPath("tmux"); err == nil {
		_, _ = fmt.Fprintf(out, "  tmux: found at %s\n", path)
		return nil
	}
	var preferred string
	switch {
	case has("brew"):
		preferred = "brew install tmux         ← detected on your system"
	case has("port"):
		preferred = "sudo port install tmux    ← detected on your system"
	case has("apt-get"):
		preferred = "apt install tmux          ← detected on your system"
	}
	var b strings.Builder
	b.WriteString("tmux is required but not found on PATH.\n\nInstall with:\n")
	if preferred != "" {
		b.WriteString("  " + preferred + "\n\nOr one of:\n")
	}
	b.WriteString("  brew install tmux         # Homebrew\n")
	b.WriteString("  sudo port install tmux    # MacPorts\n")
	b.WriteString("  apt install tmux          # Debian/Ubuntu\n\n")
	b.WriteString("Then re-run: thrum init")
	return fmt.Errorf("%s", b.String())
}

func has(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}
