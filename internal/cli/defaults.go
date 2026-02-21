package cli

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/leonletto/thrum/internal/identity"
)

// defaultIntents maps roles to their default intent templates.
// {repo} is replaced with the repository name.
var defaultIntents = map[string]string{
	"coordinator": "Coordinate agents and tasks in {repo}",
	"implementer": "Implement features and fixes in {repo}",
	"reviewer":    "Review code and PRs in {repo}",
	"planner":     "Plan architecture and design in {repo}",
	"tester":      "Test and validate changes in {repo}",
}

// DefaultIntent returns the default intent for a role and repo name.
func DefaultIntent(role, repoName string) string {
	tmpl, ok := defaultIntents[role]
	if !ok {
		tmpl = "Working in {repo}"
	}
	return strings.ReplaceAll(tmpl, "{repo}", repoName)
}

// AutoDisplay generates a display name from role and module.
// e.g., "coordinator", "main" -> "Coordinator (main)"
func AutoDisplay(role, module string) string {
	if role == "" {
		return ""
	}
	title := strings.ToUpper(role[:1]) + role[1:]
	if module != "" {
		return fmt.Sprintf("%s (%s)", title, module)
	}
	return title
}

// GetRepoName returns the repository name from git toplevel basename.
// Falls back to "unknown" if not in a git repo.
func GetRepoName(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	topLevel := strings.TrimSpace(string(out))
	parts := strings.Split(topLevel, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "unknown"
}

// GetCurrentBranch returns the current git branch.
// Falls back to "main" if not in a git repo.
func GetCurrentBranch(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		return "main"
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "main"
	}
	return branch
}

// GetRepoID returns the repo ID from git remote origin URL.
// Returns empty string if no remote or not a git repo.
func GetRepoID(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	originURL := strings.TrimSpace(string(out))
	if originURL == "" {
		return ""
	}
	repoID, err := identity.GenerateRepoID(originURL)
	if err != nil {
		return ""
	}
	return repoID
}

// GetWorktreeName returns the basename of the git worktree root.
// Falls back to the basename of repoPath.
func GetWorktreeName(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		parts := strings.Split(repoPath, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
		return ""
	}
	topLevel := strings.TrimSpace(string(out))
	parts := strings.Split(topLevel, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}
