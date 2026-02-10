package cleanup

import (
	"database/sql"
	"fmt"
	"time"
)

// CleanupStaleContexts removes stale work contexts from the database.
// Returns the number of contexts deleted.
func CleanupStaleContexts(db *sql.DB, now time.Time) (int, error) {
	nowStr := now.Format(time.RFC3339)
	cutoff24h := now.Add(-24 * time.Hour).Format(time.RFC3339)
	cutoff7d := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)

	result, err := db.Exec(`
		DELETE FROM agent_work_contexts
		WHERE
			-- Rule 1: Old (>24h) + no unmerged commits
			(
				git_updated_at IS NOT NULL
				AND git_updated_at < ?
				AND (unmerged_commits IS NULL OR unmerged_commits = '' OR unmerged_commits = '[]')
			)
			-- Rule 2: Session ended > 7 days ago
			OR session_id IN (
				SELECT session_id FROM sessions
				WHERE ended_at IS NOT NULL
				AND ended_at < ?
			)
			-- Rule 3: No git data ever collected
			OR (git_updated_at IS NULL AND ? > ?)
	`, cutoff24h, cutoff7d, nowStr, cutoff24h)

	if err != nil {
		return 0, fmt.Errorf("delete stale contexts: %w", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("get rows affected: %w", err)
	}

	return int(deleted), nil
}

// SessionWorkContext represents a work context for filtering.
type SessionWorkContext struct {
	SessionID       string
	GitUpdatedAt    *time.Time
	UnmergedCommits string // JSON string
	SessionEndedAt  *time.Time
}

// FilterStaleContexts removes stale contexts from a slice (for sync).
func FilterStaleContexts(contexts []SessionWorkContext, now time.Time) []SessionWorkContext {
	var kept []SessionWorkContext
	for _, ctx := range contexts {
		if !isStale(ctx, now) {
			kept = append(kept, ctx)
		}
	}
	return kept
}

// isStale determines if a context is stale based on cleanup rules.
func isStale(ctx SessionWorkContext, now time.Time) bool {
	cutoff24h := now.Add(-24 * time.Hour)
	cutoff7d := now.Add(-7 * 24 * time.Hour)

	// Rule 1: Old (>24h) + no unmerged commits
	if ctx.GitUpdatedAt != nil && ctx.GitUpdatedAt.Before(cutoff24h) {
		if ctx.UnmergedCommits == "" || ctx.UnmergedCommits == "[]" {
			return true
		}
	}

	// Rule 2: Session ended > 7 days ago
	if ctx.SessionEndedAt != nil && ctx.SessionEndedAt.Before(cutoff7d) {
		return true
	}

	// Rule 3: No git data ever collected (>24h since creation)
	// This is implicit in the DB query but for filtering we check if git_updated_at is nil
	if ctx.GitUpdatedAt == nil {
		return true
	}

	return false
}
