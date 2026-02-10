package gitctx

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// WorkContext represents git-derived work state.
type WorkContext struct {
	Branch           string          `json:"branch"`
	WorktreePath     string          `json:"worktree_path"`
	UnmergedCommits  []CommitSummary `json:"unmerged_commits"`
	UncommittedFiles []string        `json:"uncommitted_files"`
	ChangedFiles     []string        `json:"changed_files"`
	ExtractedAt      time.Time       `json:"extracted_at"`
}

// CommitSummary represents a single commit.
type CommitSummary struct {
	SHA     string   `json:"sha"`
	Message string   `json:"message"` // First line only
	Files   []string `json:"files"`
}

// ExtractWorkContext extracts git state for a worktree.
// If the path is not a git repository, returns an empty context (not an error).
func ExtractWorkContext(worktreePath string) (*WorkContext, error) {
	ctx := &WorkContext{
		ExtractedAt:      time.Now().UTC(),
		UnmergedCommits:  []CommitSummary{},
		UncommittedFiles: []string{},
		ChangedFiles:     []string{},
	}

	// Check if git is available
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git not installed: %w", err)
	}

	// Verify worktree path
	topLevel, err := runGitCommand(worktreePath, "rev-parse", "--show-toplevel")
	if err != nil {
		// Not a git repo - return empty context
		return ctx, nil //nolint:nilerr // intentional: not being a git repo is not an error
	}
	ctx.WorktreePath = strings.TrimSpace(topLevel)

	// Get current branch
	branch, err := runGitCommand(worktreePath, "branch", "--show-current")
	if err == nil {
		ctx.Branch = strings.TrimSpace(branch)
	}

	// Determine base branch (origin/main, origin/master, or HEAD~10)
	baseBranch := determineBaseBranch(worktreePath)

	// Get unmerged commits
	if baseBranch != "" {
		commits, err := extractUnmergedCommits(worktreePath, baseBranch)
		if err == nil {
			ctx.UnmergedCommits = commits
		}

		// Get changed files vs base branch
		changedFiles, err := runGitCommand(worktreePath, "diff", "--name-only", baseBranch+"...HEAD")
		if err == nil {
			ctx.ChangedFiles = parseLines(changedFiles)
		}
	}

	// Get uncommitted files (staged + modified)
	status, err := runGitCommand(worktreePath, "status", "--porcelain")
	if err == nil {
		ctx.UncommittedFiles = parseStatusOutput(status)
	}

	return ctx, nil
}

// runGitCommand executes a git command in the specified directory.
func runGitCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w (stderr: %s)", strings.Join(args, " "), err, stderr.String())
	}

	return stdout.String(), nil
}

// determineBaseBranch finds the best base branch to compare against.
func determineBaseBranch(worktreePath string) string {
	// Try origin/main
	if branchExists(worktreePath, "origin/main") {
		return "origin/main"
	}

	// Try origin/master
	if branchExists(worktreePath, "origin/master") {
		return "origin/master"
	}

	// Fall back to HEAD~10 (last 10 commits)
	return "HEAD~10"
}

// branchExists checks if a branch exists.
func branchExists(worktreePath, branch string) bool {
	_, err := runGitCommand(worktreePath, "rev-parse", "--verify", branch)
	return err == nil
}

// extractUnmergedCommits gets commits that exist on HEAD but not on baseBranch.
func extractUnmergedCommits(worktreePath, baseBranch string) ([]CommitSummary, error) {
	// Get commit SHAs and messages
	output, err := runGitCommand(worktreePath, "log", baseBranch+"..HEAD", "--format=%H %s")
	if err != nil {
		return nil, err
	}

	lines := parseLines(output)
	commits := make([]CommitSummary, 0, len(lines))

	for _, line := range lines {
		// Parse "SHA message"
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}

		sha := parts[0]
		message := parts[1]

		// Get files changed in this commit
		filesOutput, err := runGitCommand(worktreePath, "diff-tree", "--no-commit-id", "--name-only", "-r", sha)
		var files []string
		if err == nil {
			files = parseLines(filesOutput)
		}

		commits = append(commits, CommitSummary{
			SHA:     sha,
			Message: message,
			Files:   files,
		})
	}

	return commits, nil
}

// parseLines splits output into non-empty lines.
func parseLines(output string) []string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	result := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}

	return result
}

// parseStatusOutput parses `git status --porcelain` output to extract file paths.
func parseStatusOutput(output string) []string {
	lines := parseLines(output)
	files := make([]string, 0, len(lines))

	for _, line := range lines {
		// Porcelain format: "XY filename"
		// XY is 2-character status code, then space, then filename
		if len(line) < 4 {
			continue
		}
		filename := strings.TrimSpace(line[3:])
		if filename != "" {
			files = append(files, filename)
		}
	}

	return files
}
