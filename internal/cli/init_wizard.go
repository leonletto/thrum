package cli

import (
	"fmt"
	"io"
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
// init command from CLI flags + repo path. Tests construct it directly with
// a FakePrompter.
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
	gitignoreSnapshot    []byte
	gitignoreExisted     bool
	gitExcludeSnapshot   []byte
	gitExcludeExisted    bool
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

// sanitize replaces any non-alphanumeric/underscore/dash character with a
// dash so a repo basename is safe to use inside a default agent name.
func sanitize(s string) string { return sanitizeRE.ReplaceAllString(s, "-") }

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
// not overwritten).
func loadReInitDefaults(cfg *WizardConfig) error {
	if !cfg.Force {
		return nil
	}
	thrumDir := filepath.Join(cfg.RepoPath, ".thrum")
	if existingCfg, err := config.LoadThrumConfig(thrumDir); err == nil && existingCfg != nil {
		if cfg.WorktreesRoot == "" {
			cfg.WorktreesRoot = existingCfg.Worktrees.BasePath
		}
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
	}
	return nil
}

// snapshotGitFiles captures .gitignore and .git/info/exclude before Init()
// modifies them so rollback can restore them byte-for-byte. Missing files
// are recorded as "did not exist", causing rollback to remove a file Init
// created rather than writing back nil bytes.
func snapshotGitFiles(cfg *WizardConfig) {
	gi := filepath.Join(cfg.RepoPath, ".gitignore")
	if data, err := os.ReadFile(gi); err == nil {
		cfg.gitignoreSnapshot = data
		cfg.gitignoreExisted = true
	}
	ex := filepath.Join(cfg.RepoPath, ".git", "info", "exclude")
	if data, err := os.ReadFile(ex); err == nil {
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
	// SIGINT handler runs the same cleanup as a Step 3-5 error path so
	// Ctrl-C mid-wizard never leaves a half-scaffolded .thrum/ behind.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		if _, ok := <-sigCh; !ok {
			return
		}
		_ = rollback(cfg, fmt.Errorf("interrupted"))
		os.Exit(130)
	}()

	if err := tmuxGate(os.Stderr); err != nil {
		return err
	}

	// Re-init mode reads existing .thrum/ before Init's --force re-scaffold
	// can touch it, so prompt defaults reflect the user's prior choices.
	if err := loadReInitDefaults(cfg); err != nil {
		return err
	}

	snapshotGitFiles(cfg)

	if err := Init(InitOptions{
		RepoPath: cfg.RepoPath,
		Force:    cfg.Force,
		Stealth:  cfg.Stealth,
	}); err != nil {
		return err
	}

	id, err := stepIdentity(cfg)
	if err != nil {
		return rollback(cfg, err)
	}

	socketPath := DefaultSocketPath(cfg.RepoPath)
	qsClient, err := NewClient(socketPath)
	if err != nil {
		return rollback(cfg, err)
	}
	defer qsClient.Close()
	if _, err := Quickstart(qsClient, QuickstartOptions{
		Name:     id.Name,
		Role:     id.Role,
		Module:   id.Module,
		RepoPath: cfg.RepoPath,
		Runtime:  cfg.Runtime,
		Force:    cfg.Force,
	}); err != nil {
		return rollback(cfg, err)
	}

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

	if err := stepDaemon(cfg); err != nil {
		fmt.Fprintf(os.Stderr,
			"Daemon failed to start: %v\nConfiguration is complete. Start the daemon manually with: thrum daemon start\n",
			err)
		return nil
	}
	return nil
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
