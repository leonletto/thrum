package guard

import (
	"log/slog"
	"os"
	"path/filepath"
)

// G2 refuses auto-bootstrap of a .thrum/ directory outside a git repo.
// The footgun this closes: running `thrum daemon start` or `thrum
// init` from $HOME (or any ad-hoc directory) used to silently
// materialize a .thrum/ with nonsense supervisor slugs. Requiring a
// git anchor gives every identity file a repo-scoped parent so repo
// names / supervisor slugs are derivable from git state rather than
// from whatever the cwd happened to be called.
//
// Passing force=true lets operators override the check for deliberate
// non-anchored use (ephemeral tests, one-off tooling) — the override
// is intentionally an explicit flag, not an env var or config
// setting, so nothing unrelated can flip it.
func G2(mode Mode, dir string, force bool, warnLogger *slog.Logger) error {
	if mode == ModeOff {
		return nil
	}
	if isGitRepo(dir) {
		return nil
	}
	if force {
		return nil
	}
	e := &Error{
		Guard:       "non_git_bootstrap",
		Reason:      "not_a_git_repo",
		CallerCWD:   dir,
		Remediation: "run from a git-anchored directory, or pass --force for ephemeral non-anchored use",
	}
	if mode == ModeWarn {
		if warnLogger != nil {
			warnLogger.Warn("identity_guard_fire",
				"guard", e.Guard,
				"reason", e.Reason,
				"cwd", e.CallerCWD,
			)
		}
		return nil
	}
	return e
}

// isGitRepo walks from start toward the filesystem root looking for a
// .git entry. filepath.Dir terminates at "/" on unix and the volume
// root on windows; the explicit empty-string guard belt-and-braces
// against any edge case that returns "" instead.
func isGitRepo(start string) bool {
	cur := start
	for cur != "" && cur != "/" {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return true
		}
		next := filepath.Dir(cur)
		if next == cur {
			return false
		}
		cur = next
	}
	return false
}
