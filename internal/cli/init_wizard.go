package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

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
