package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureRedirects verifies and creates .thrum/ and .beads/ redirects
// in a worktree, pointing back to the main repo. Creates identities/ and
// context/ directories in the worktree's local .thrum/.
//
// mainRepo is the absolute path to the main repository root (the one with
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
	if existing, err := os.ReadFile(redirectPath); err != nil || strings.TrimSpace(string(existing)) != mainThrumDir {
		if err == nil && strings.TrimSpace(string(existing)) != mainThrumDir {
			fmt.Fprintf(os.Stderr, "⚠ Fixed .thrum/redirect (was pointing to %s)\n", strings.TrimSpace(string(existing)))
		}
		if err := os.WriteFile(redirectPath, []byte(redirectContent), 0644); err != nil {
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
		if existing, err := os.ReadFile(beadsRedirect); err != nil || strings.TrimSpace(string(existing)) != mainBeadsDir {
			if err := os.WriteFile(beadsRedirect, []byte(beadsContent), 0644); err != nil {
				return fmt.Errorf("write beads redirect: %w", err)
			}
		}
	}

	return nil
}

// EnforceOneIdentity removes all identity files from the worktree's
// .thrum/identities/ directory except the one matching newAgentName.
// Returns the names of deleted agents. Context files are preserved.
// Errors during deletion are logged but non-fatal.
func EnforceOneIdentity(worktreePath, newAgentName string) []string {
	idDir := filepath.Join(worktreePath, ".thrum", "identities")
	entries, err := os.ReadDir(idDir)
	if err != nil {
		return nil
	}

	keepFile := newAgentName + ".json"
	var deleted []string

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if entry.Name() == keepFile {
			continue
		}
		path := filepath.Join(idDir, entry.Name())
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not remove stale identity %s: %v\n", entry.Name(), err)
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		fmt.Fprintf(os.Stderr, "Removed stale identity: %s\n", name)
		deleted = append(deleted, name)
	}

	return deleted
}

// BuildQuickstartCmd constructs a shell-safe thrum quickstart command string
// for injection into a tmux pane. All values are single-quote wrapped.
// Single quotes within values are escaped as '\'' (end quote, escaped quote,
// start quote). --force is always included for idempotent re-registration.
func BuildQuickstartCmd(name, role, module, intent, runtime string) string {
	var parts []string
	parts = append(parts, "thrum", "quickstart")
	parts = append(parts, "--name", ShellQuote(name))
	parts = append(parts, "--role", ShellQuote(role))
	parts = append(parts, "--module", ShellQuote(module))

	if intent != "" {
		parts = append(parts, "--intent", ShellQuote(intent))
	}
	if runtime != "" {
		parts = append(parts, "--runtime", ShellQuote(runtime))
	}

	parts = append(parts, "--force")

	return strings.Join(parts, " ")
}

// ShellQuote wraps a value in single quotes, escaping any internal single
// quotes with the '\'' idiom (end quote, literal quote, restart quote).
func ShellQuote(s string) string {
	escaped := strings.ReplaceAll(s, "'", `'\''`)
	return "'" + escaped + "'"
}
