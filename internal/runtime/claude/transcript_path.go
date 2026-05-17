// Package claude carries helpers specific to the Claude Code runtime that
// are shared across daemon and tooling (scheduler stage-4 journal write,
// MB-1.S6 telemetry post-run parse, future agent tooling).
//
// Per canonical-ref §8.2, the transcript-path helper lives HERE rather
// than under internal/daemon/scheduler/handlers/. Downstream callers
// (B-B1's scheduled_agent handler, MB-1.S6 telemetry parser) must not be
// forced to depend on scheduler internals just to compute a path.
package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TranscriptDir returns the absolute path to the Claude Code transcript
// directory for a per-wake scheduled_agent run, per canonical §8.2.
//
// Claude Code stores transcripts at:
//
//	~/.claude/projects/<hashed-worktree-path>/
//
// The hash is "replace each '/' and '.' with '-'" applied to the absolute
// worktree path. (Verified against the real on-disk directories Claude
// Code produces; e.g. /Users/leon/.thrum/... → -Users-leon--thrum-...)
// The wake timestamp is appended so each per-wake worktree gets a
// distinct directory.
//
// agentName is currently unused — the directory hash is path-derived. The
// param is kept on the signature so callers that join agent+wake metadata
// elsewhere don't have to re-thread the worktree path; if a future Claude
// Code update incorporates the agent name into its hash, the
// implementation can adopt it without a callsite churn.
func TranscriptDir(worktreePath, agentName string, wakeTimestamp int64) string {
	_ = agentName

	home, _ := os.UserHomeDir()
	abs := strings.TrimSuffix(filepath.Clean(worktreePath), "/")
	hashed := strings.ReplaceAll(abs, "/", "-")
	hashed = strings.ReplaceAll(hashed, ".", "-")
	return fmt.Sprintf("%s/.claude/projects/%s-%d/", home, hashed, wakeTimestamp)
}
