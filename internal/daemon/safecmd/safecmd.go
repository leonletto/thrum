package safecmd

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Git runs a git command with a 5-second timeout.
// All daemon-side git operations must use this instead of exec.Command("git", ...).
func Git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %v in %s: %w (output: %s)", args, dir, err, out)
	}
	return out, nil
}

// GitLong runs git commands that involve network I/O (push, fetch) with a 10-second timeout.
func GitLong(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %v in %s: %w (output: %s)", args, dir, err, out)
	}
	return out, nil
}
