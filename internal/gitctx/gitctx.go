package gitctx

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// WorkContext represents git-derived work state.
type WorkContext struct {
	Branch           string          `json:"branch"`
	WorktreePath     string          `json:"worktree_path"`
	UnmergedCommits  []CommitSummary `json:"unmerged_commits"`
	UncommittedFiles []string        `json:"uncommitted_files"`
	ChangedFiles     []string        `json:"changed_files"` // Kept for backward compatibility
	FileChanges      []FileChange    `json:"file_changes"`  // NEW: rich per-file data
	ExtractedAt      time.Time       `json:"extracted_at"`
}

// CommitSummary represents a single commit.
type CommitSummary struct {
	SHA     string   `json:"sha"`
	Message string   `json:"message"` // First line only
	Files   []string `json:"files"`
}

// FileChange represents detailed information about a changed file.
type FileChange struct {
	Path         string    `json:"path"`
	LastModified time.Time `json:"last_modified"`
	Additions    int       `json:"additions"`
	Deletions    int       `json:"deletions"`
	Status       string    `json:"status"` // "modified", "added", "deleted", "renamed"
}

// ExtractWorkContext extracts git state for a worktree.
// If the path is not a git repository, returns an empty context (not an error).
func ExtractWorkContext(worktreePath string) (*WorkContext, error) {
	ctx := &WorkContext{
		ExtractedAt:      time.Now().UTC(),
		UnmergedCommits:  []CommitSummary{},
		UncommittedFiles: []string{},
		ChangedFiles:     []string{},
		FileChanges:      []FileChange{},
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

		// Extract per-file changes with diffstat and timestamps
		fileChanges, err := extractFileChanges(worktreePath, baseBranch)
		if err == nil {
			ctx.FileChanges = fileChanges
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

// extractFileChanges extracts per-file metadata (diffstat, timestamps) for files changed
// between baseBranch and HEAD. Returns files sorted by most-recent-first.
func extractFileChanges(worktreePath, baseBranch string) ([]FileChange, error) {
	// Step 1: Get diffstat for all changed files (additions/deletions)
	diffstatOutput, err := runGitCommand(worktreePath, "diff", "--numstat", baseBranch+"...HEAD")
	if err != nil {
		return nil, fmt.Errorf("git diff --numstat: %w", err)
	}

	// Parse diffstat into a map: filename -> (additions, deletions)
	diffstatMap := make(map[string]struct{ additions, deletions int })
	for _, line := range parseLines(diffstatOutput) {
		// Format: "additions\tdeletions\tfilename"
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}

		additions := 0
		deletions := 0
		filename := parts[2]

		// Handle binary files (shows "-" for additions/deletions)
		if parts[0] != "-" {
			fmt.Sscanf(parts[0], "%d", &additions)
		}
		if parts[1] != "-" {
			fmt.Sscanf(parts[1], "%d", &deletions)
		}

		diffstatMap[filename] = struct{ additions, deletions int }{additions, deletions}
	}

	// Step 2: Get timestamps for each file (batch extraction)
	// Use git log to walk commits and find the most recent modification time per file
	logOutput, err := runGitCommand(worktreePath, "log", baseBranch+"...HEAD", "--format=%aI", "--name-only")
	if err != nil {
		return nil, fmt.Errorf("git log --name-only: %w", err)
	}

	// Parse log output to extract timestamps per file
	timestampMap := make(map[string]time.Time)
	lines := parseLines(logOutput)
	var currentTimestamp time.Time

	for _, line := range lines {
		// Try to parse as timestamp (ISO 8601 format)
		if t, err := time.Parse(time.RFC3339, line); err == nil {
			currentTimestamp = t
			continue
		}

		// Otherwise, it's a filename
		filename := line
		if filename == "" {
			continue
		}

		// Record timestamp for this file (only if not already recorded)
		// Since log is in reverse chronological order, first occurrence = most recent
		if _, exists := timestampMap[filename]; !exists {
			timestampMap[filename] = currentTimestamp
		}
	}

	// Step 3: Combine diffstat and timestamps into FileChange structs
	fileChanges := make([]FileChange, 0, len(diffstatMap))
	for filename, diffstat := range diffstatMap {
		timestamp := timestampMap[filename]
		if timestamp.IsZero() {
			// Fallback to current time if timestamp not found
			timestamp = time.Now().UTC()
		}

		// Determine status based on diffstat
		status := "modified"
		if diffstat.additions > 0 && diffstat.deletions == 0 {
			status = "added"
		} else if diffstat.additions == 0 && diffstat.deletions > 0 {
			status = "deleted"
		}

		fileChanges = append(fileChanges, FileChange{
			Path:         filename,
			LastModified: timestamp,
			Additions:    diffstat.additions,
			Deletions:    diffstat.deletions,
			Status:       status,
		})
	}

	// Step 4: Sort by LastModified descending (most recent first)
	sortFileChangesByTime(fileChanges)

	return fileChanges, nil
}

// sortFileChangesByTime sorts FileChange slice by LastModified descending (most recent first).
func sortFileChangesByTime(changes []FileChange) {
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].LastModified.After(changes[j].LastModified)
	})
}
