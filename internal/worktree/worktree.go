package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
// thrum-ajmd design: the original behaviour was os.Remove, which had no
// recourse when it fired on the wrong dir (a non-coordinator agent's
// refresh running with cwd resolving to the main repo path wiped
// coordinator_main.json as a "stale sibling"). Quarantine preserves the
// file so an operator can recover it. The quarantine dir is owned by
// the caller (0o750) and timestamped so repeated enforcement does not
// overwrite previous quarantined copies.
func EnforceOneIdentity(worktreePath, newAgentName string) []string {
	idDir := filepath.Join(worktreePath, ".thrum", "identities")
	entries, err := os.ReadDir(idDir)
	if err != nil {
		return nil
	}

	keepFile := newAgentName + ".json"
	var quarantined []string
	var quarantineDir string // lazily created on first quarantine
	ts := time.Now().UTC().Format("20060102T150405Z")

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if entry.Name() == keepFile {
			continue
		}
		src := filepath.Join(idDir, entry.Name())

		if quarantineDir == "" {
			quarantineDir = filepath.Join(idDir, ".quarantine")
			if err := os.MkdirAll(quarantineDir, 0o750); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not create identity quarantine dir: %v\n", err)
				continue
			}
		}
		dst := filepath.Join(quarantineDir, entry.Name()+"."+ts)
		if err := os.Rename(src, dst); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not quarantine stale identity %s: %v\n", entry.Name(), err)
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		fmt.Fprintf(os.Stderr, "Quarantined stale identity: %s → %s\n", name, dst)
		quarantined = append(quarantined, name)
	}

	return quarantined
}

// BuildQuickstartCmd constructs a shell-safe thrum quickstart command string
// for injection into a tmux pane. All values are single-quote wrapped.
// Single quotes within values are escaped as '\” (end quote, escaped quote,
// start quote). --force is always included for idempotent re-registration.
func BuildQuickstartCmd(name, role, module, intent, runtime string) string {
	var parts []string
	parts = append(parts, "thrum", "quickstart")
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

	return strings.Join(parts, " ")
}

// shellQuote wraps a value in single quotes, escaping any internal single
// quotes with the '\” idiom (end quote, literal quote, restart quote).
func shellQuote(s string) string {
	escaped := strings.ReplaceAll(s, "'", `'\''`)
	return "'" + escaped + "'"
}
