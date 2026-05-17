package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
)

// skillsGitignoreSection is the comment header preceding the skills
// negation block in .gitignore. The block layout is:
//
//	<thrum-existing-section>
//	# Skills substrate (v0.11): .thrum/skills/ tracked; per-agent drafts stay local
//	!.thrum/skills/
//	.thrum/agents/*/proposed-skills/
//
// Order matters: !.thrum/skills/ must follow the blanket .thrum/ ignore
// for the negation to take effect.
const skillsGitignoreSection = "# Skills substrate (v0.11): .thrum/skills/ tracked; per-agent drafts stay local"

// skillsNegationLine un-ignores .thrum/skills/ so promoted skills are
// committed to the project's git history (canonical Q9 / spec §10.2).
const skillsNegationLine = "!.thrum/skills/"

// proposedSkillsLocalLine explicitly ignores per-agent draft
// proposed-skills. Redundant under blanket .thrum/ but kept for
// readability and forward-compat if a user removes the blanket.
const proposedSkillsLocalLine = ".thrum/agents/*/proposed-skills/"

// applySkillsBootstrap runs the C-B1 init-bootstrap steps (spec §10.2
// + §10.3): creates .thrum/skills/.gitkeep, adds the skills-aware
// entries to .gitignore (prompts on the v0.10.x → v0.11 upgrade path
// unless Yes or no Prompter), and stamps skills.pending_reminder_after
// into .thrum/config.json if absent.
//
// Idempotent: all steps detect existing state and no-op when the
// project is already up to date.
func applySkillsBootstrap(opts InitOptions) error {
	thrumDir := filepath.Join(opts.RepoPath, ".thrum")
	skillsDir := filepath.Join(thrumDir, "skills")

	if err := os.MkdirAll(skillsDir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", skillsDir, err)
	}
	gitkeep := filepath.Join(skillsDir, ".gitkeep")
	if _, err := os.Stat(gitkeep); os.IsNotExist(err) {
		if err := os.WriteFile(gitkeep, []byte{}, 0o600); err != nil {
			return fmt.Errorf("create %s: %w", gitkeep, err)
		}
	}

	if !opts.Stealth {
		if err := applySkillsGitignore(opts); err != nil {
			return fmt.Errorf("update .gitignore for skills: %w", err)
		}
	}

	if err := applySkillsConfigDefaults(thrumDir); err != nil {
		return fmt.Errorf("update config.json for skills: %w", err)
	}

	return nil
}

// applySkillsGitignore adds the !.thrum/skills/ negation (and the
// explicit proposed-skills ignore) to .gitignore. On an existing
// .gitignore that already carries the .thrum/ blanket but no negation
// (the v0.10.x → v0.11 upgrade case), it confirms via Prompter when
// Yes is false; absent a Prompter it auto-applies — matching the AC
// non-interactive default.
func applySkillsGitignore(opts InitOptions) error {
	path := filepath.Join(opts.RepoPath, ".gitignore")

	var existing string
	if data, err := os.ReadFile(path); err == nil { // #nosec G304 -- path is <repoRoot>/.gitignore
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}

	lines := strings.Split(existing, "\n")
	hasNegation := containsTrimmed(lines, skillsNegationLine)
	hasProposed := containsTrimmed(lines, proposedSkillsLocalLine)
	if hasNegation && hasProposed {
		return nil
	}

	hasBlanket := containsTrimmed(lines, ".thrum/")

	// Upgrade prompt: existing .thrum/ blanket but no negation. Skip
	// when Yes is set, when no Prompter is wired, or when a Prompter
	// reports the user declined.
	if hasBlanket && !hasNegation && !opts.Yes && opts.Prompter != nil {
		ok, err := opts.Prompter.Confirm(
			PromptSkillsGitignoreApply,
			"Add !.thrum/skills/ to .gitignore so promoted skills travel via git?",
			false,
		)
		if err != nil {
			return fmt.Errorf("skills gitignore confirm: %w", err)
		}
		if !ok {
			return nil
		}
	}

	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	if existing != "" {
		b.WriteString("\n")
	}
	b.WriteString(skillsGitignoreSection + "\n")
	if !hasNegation {
		b.WriteString(skillsNegationLine + "\n")
	}
	if !hasProposed {
		b.WriteString(proposedSkillsLocalLine + "\n")
	}

	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// applySkillsConfigDefaults inserts the skills.pending_reminder_after
// default into .thrum/config.json when the skills block is absent or
// the field is empty. Preserves any user-set value.
func applySkillsConfigDefaults(thrumDir string) error {
	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load thrum config: %w", err)
	}
	if cfg.Skills.PendingReminderAfter != "" {
		return nil
	}
	cfg.Skills.PendingReminderAfter = config.DefaultSkillsPendingReminderAfter
	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		return fmt.Errorf("save thrum config: %w", err)
	}
	return nil
}

func containsTrimmed(lines []string, target string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}

// SkillsBootstrapNeeded returns true when applySkillsBootstrap would
// add the !.thrum/skills/ negation to an existing .gitignore (the
// upgrade-prompt case). cmd/thrum/main.go uses this to decide whether
// to wire a Prompter into InitOptions on an interactive TTY before
// calling Init.
//
// The check is best-effort — a missing .gitignore returns false (Init
// will write it from scratch with the negation included). Errors
// reading the file are swallowed: if the read fails, init proceeds and
// will surface the error during its own write.
func SkillsBootstrapNeeded(repoPath string) bool {
	data, err := os.ReadFile(filepath.Join(repoPath, ".gitignore")) // #nosec G304 -- path is <repoRoot>/.gitignore
	if err != nil {
		return false
	}
	lines := strings.Split(string(data), "\n")
	hasBlanket := containsTrimmed(lines, ".thrum/")
	hasNegation := containsTrimmed(lines, skillsNegationLine)
	return hasBlanket && !hasNegation
}

