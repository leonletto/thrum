package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
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
// e.g., "coordinator", "main" -> "Coordinator (main)".
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

// GitTopLevel returns the git toplevel directory, or empty string on error.
func GitTopLevel(repoPath string) string {
	out, err := safecmd.Git(context.Background(), repoPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GetRepoName returns the repository name from git toplevel basename.
// Falls back to "unknown" if not in a git repo.
func GetRepoName(repoPath string) string {
	topLevel := GitTopLevel(repoPath)
	if topLevel == "" {
		return "unknown"
	}
	parts := strings.Split(topLevel, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "unknown"
}

// GetCurrentBranch returns the current git branch.
// Falls back to "main" if not in a git repo.
func GetCurrentBranch(repoPath string) string {
	out, err := safecmd.Git(context.Background(), repoPath, "branch", "--show-current")
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
	out, err := safecmd.Git(context.Background(), repoPath, "remote", "get-url", "origin")
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

// DefaultModule returns the default module name for a repo, used as the
// default value for the wizard's module prompt. Resolution order:
//  1. git symbolic-ref refs/remotes/origin/HEAD (e.g. "main", "master")
//  2. git symbolic-ref HEAD (current local branch)
//  3. "main" (literal fallback for fresh repos with no commits)
func DefaultModule(repoPath string) string {
	ctx := context.Background()
	if out, err := safecmd.Git(ctx, repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			// Output is like "origin/main"; strip the "origin/" prefix.
			if _, after, ok := strings.Cut(name, "/"); ok {
				return after
			}
			return name
		}
	}
	if out, err := safecmd.Git(ctx, repoPath, "symbolic-ref", "--short", "HEAD"); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name
		}
	}
	return "main"
}

// GetWorktreeName returns the basename of the git worktree root.
// Falls back to the basename of repoPath.
func GetWorktreeName(repoPath string) string {
	topLevel := GitTopLevel(repoPath)
	if topLevel == "" {
		parts := strings.Split(repoPath, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
		return ""
	}
	parts := strings.Split(topLevel, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}
