package backup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const postHookTimeout = 60 * time.Second

// PostHookResult holds the outcome of a post-backup hook.
type PostHookResult struct {
	Command string
	Error   string // non-empty if command failed (non-fatal)
}

// RunPostBackup executes the post-backup hook command.
// Failure is non-fatal: the error is captured in the result, not returned.
func RunPostBackup(command, repoPath, backupDir, repoName, currentDir string) *PostHookResult {
	result := &PostHookResult{Command: command}

	ctx, cancel := context.WithTimeout(context.Background(), postHookTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // G204 - user-configured command
	cmd.Dir = repoPath
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("THRUM_BACKUP_DIR=%s", backupDir),
		fmt.Sprintf("THRUM_BACKUP_REPO=%s", repoName),
		fmt.Sprintf("THRUM_BACKUP_CURRENT=%s", currentDir),
	)

	if err := cmd.Run(); err != nil {
		errMsg := err.Error()
		if s := stderr.String(); s != "" {
			const maxStderr = 4096
			if len(s) > maxStderr {
				s = s[len(s)-maxStderr:]
			}
			errMsg += ": " + strings.TrimSpace(s)
		}
		result.Error = errMsg
	}

	return result
}
