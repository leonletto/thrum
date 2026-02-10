package sync

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Syncer coordinates sync operations (branch, merge, push).
type Syncer struct {
	repoPath      string
	syncDir       string // sync worktree directory (.git/thrum-sync/a-sync)
	localOnly     bool   // when true, skip all git push/fetch operations
	branchManager *BranchManager
	merger        *Merger
}

// NewSyncer creates a new Syncer for the given repository path.
// When localOnly is true, all remote git operations (push/fetch) are skipped.
func NewSyncer(repoPath string, syncDir string, localOnly bool) *Syncer {
	return &Syncer{
		repoPath:      repoPath,
		syncDir:       syncDir,
		localOnly:     localOnly,
		branchManager: NewBranchManager(repoPath, localOnly),
		merger:        NewMerger(repoPath, syncDir, localOnly),
	}
}

// CommitAndPush commits and pushes changes to the remote a-sync branch.
// Steps:
// 1. Stage all files in sync worktree (events.jsonl + messages/*.jsonl)
// 2. Commit with message "sync: <timestamp>"
// 3. Push to origin a-sync
// 4. Handle push rejection (remote ahead)
//
// Push rejection handling:
// - If push rejected, fetch + merge + retry
// - Max 3 retries before failing.
func (s *Syncer) CommitAndPush() error {
	const maxRetries = 3

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Check if there are changes to commit
		hasChanges, err := s.hasChanges()
		if err != nil {
			return fmt.Errorf("checking for changes: %w", err)
		}

		if !hasChanges {
			// No changes to push
			return nil
		}

		// Stage all JSONL files (events.jsonl + messages/*.jsonl)
		if err := s.stageChanges(); err != nil {
			return fmt.Errorf("staging changes: %w", err)
		}

		// Commit with timestamp
		timestamp := time.Now().UTC().Format(time.RFC3339)
		commitMsg := fmt.Sprintf("sync: %s", timestamp)
		if err := s.commitChanges(commitMsg); err != nil {
			return fmt.Errorf("committing changes: %w", err)
		}

		// Push to origin a-sync
		err = s.push()
		if err == nil {
			// Push succeeded
			return nil
		}

		// Check if it's a push rejection (remote ahead)
		if !isPushRejected(err) {
			// Some other error, not a rejection
			return fmt.Errorf("pushing: %w", err)
		}

		// Push rejected - remote is ahead
		if attempt == maxRetries {
			return fmt.Errorf("push rejected after %d retries: remote ahead", maxRetries)
		}

		// Fetch and merge, then retry
		if err := s.merger.Fetch(); err != nil {
			return fmt.Errorf("fetch after rejection (attempt %d): %w", attempt, err)
		}

		if _, err := s.merger.MergeAll(); err != nil {
			return fmt.Errorf("merge after rejection (attempt %d): %w", attempt, err)
		}

		// Loop will retry the commit and push
	}

	return fmt.Errorf("push failed after %d retries", maxRetries)
}

// hasChanges checks if there are uncommitted changes in the sync worktree.
// Uses git status --porcelain to detect any modifications.
func (s *Syncer) hasChanges() (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = s.syncDir
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("checking status: %w", err)
	}
	return strings.TrimSpace(string(output)) != "", nil
}

// stageChanges stages all changes in the sync worktree.
// The worktree only contains JSONL data, so we stage everything.
func (s *Syncer) stageChanges() error {
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = s.syncDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	return nil
}

// commitChanges creates a commit with the given message.
func (s *Syncer) commitChanges(message string) error {
	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = s.syncDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if the error is "nothing to commit" or "nothing added to commit"
		outputStr := strings.ToLower(string(output))
		if strings.Contains(outputStr, "nothing to commit") ||
			strings.Contains(outputStr, "nothing added to commit") {
			// Not really an error - no changes to commit
			return nil
		}
		return fmt.Errorf("git commit failed: %w (output: %s)", err, string(output))
	}
	return nil
}

// push pushes the a-sync branch to origin.
func (s *Syncer) push() error {
	if s.localOnly {
		return nil
	}

	// Check if remote exists
	cmd := exec.Command("git", "remote")
	cmd.Dir = s.syncDir
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("checking for remotes: %w", err)
	}

	remotes := strings.TrimSpace(string(output))
	if remotes == "" {
		// No remote configured - can't push
		return nil //nolint:nilerr // local-only mode is valid
	}

	// Push to origin a-sync
	cmd = exec.Command("git", "push", "origin", SyncBranchName)
	cmd.Dir = s.syncDir
	output, err = cmd.CombinedOutput()
	if err != nil {
		return &PushError{
			Err:    err,
			Output: string(output),
		}
	}

	return nil
}

// PushError wraps a push error with the git output.
type PushError struct {
	Err    error
	Output string
}

func (e *PushError) Error() string {
	return fmt.Sprintf("push failed: %v (output: %s)", e.Err, e.Output)
}

func (e *PushError) Unwrap() error {
	return e.Err
}

// isPushRejected checks if the error is due to push rejection (remote ahead).
func isPushRejected(err error) bool {
	if pushErr, ok := err.(*PushError); ok {
		output := strings.ToLower(pushErr.Output)
		// Common git push rejection messages
		return strings.Contains(output, "rejected") ||
			strings.Contains(output, "non-fast-forward") ||
			strings.Contains(output, "fetch first") ||
			strings.Contains(output, "updates were rejected")
	}
	return false
}

// GetSyncBranchRef returns the current commit ref of the a-sync branch.
// This is useful for tracking sync state.
func (s *Syncer) GetSyncBranchRef() (string, error) {
	return s.branchManager.GetSyncBranchRef()
}
