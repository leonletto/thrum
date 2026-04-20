package state

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// touchDebounceWindow caps the write rate for agent last_seen_at
// updates. Activity happening more frequently than this collapses to a
// single DB write; every RPC handler can call TouchAgentLastSeen without
// worrying about SQLite churn. 30s granularity is well under the
// RecipientStaleThreshold (30m) that consumes last_seen.
const touchDebounceWindow = 30 * time.Second

// touchCacheEvictAt triggers an opportunistic prune of stale entries
// in the touchTimes map. Bounds memory growth in long-running daemons
// with agent churn: every distinct agent_id that touches the daemon
// creates an entry, and nothing else evicts deregistered agents.
// Picked so the map stays cache-line-friendly for the common case of
// a small team while still giving headroom before eviction runs.
const touchCacheEvictAt = 256

// TouchAgentLastSeen advances the agent's last_seen_at column to the
// current wall clock, debounced to touchDebounceWindow per agent_id.
//
// Call this from RPC handlers that signal agent liveness (message.send,
// message.list, agent.whoami, team.list, etc.). Debouncing keeps the
// write rate bounded regardless of caller frequency. An empty agent_id
// or a miss on the agents table is a silent no-op so handlers can call
// TouchAgentLastSeen without extra pre-flight.
//
// Errors from the underlying UPDATE are returned but are generally
// safe to ignore at call sites — a failed touch only delays a hint's
// freshness signal, not message delivery.
func (s *State) TouchAgentLastSeen(ctx context.Context, agentID string) error {
	return s.touchAgentLastSeenAt(ctx, agentID, time.Now().UTC())
}

// touchAgentLastSeenAt is TouchAgentLastSeen with an injectable clock
// for tests. Separated so test cases can simulate debounce boundaries
// without real-time sleeps.
//
// Writes to BOTH tables:
//   - agents.last_seen_at (the agent row)
//   - sessions.last_seen_at (the agent's active session, if any)
//
// team.list sources the user-visible "last_seen" column from the
// sessions table (team.go joins agents → sessions and selects
// s.last_seen_at), and the rqkf Phase B send.recipient-stale hint
// consumes that same column. Touching only agents would leave the
// hint's input stale. Mirrors session.HandleHeartbeat's dual-update.
func (s *State) touchAgentLastSeenAt(ctx context.Context, agentID string, now time.Time) error {
	if agentID == "" {
		return nil
	}

	s.touchMu.Lock()
	if s.touchTimes == nil {
		s.touchTimes = map[string]time.Time{}
	}
	prev, seen := s.touchTimes[agentID]
	if seen && now.Sub(prev) < touchDebounceWindow {
		s.touchMu.Unlock()
		return nil
	}
	s.touchTimes[agentID] = now
	if len(s.touchTimes) > touchCacheEvictAt {
		// Opportunistic prune: drop entries older than the debounce
		// window. Any future touch for a pruned agent_id will be
		// treated as "first touch" and correctly write through, so
		// eviction is safe to run at any time.
		for id, t := range s.touchTimes {
			if now.Sub(t) >= touchDebounceWindow {
				delete(s.touchTimes, id)
			}
		}
	}
	s.touchMu.Unlock()

	timestamp := now.Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx,
		`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`,
		timestamp, agentID,
	); err != nil {
		return fmt.Errorf("touch agent last_seen: %w", err)
	}
	// Best-effort: also advance the agent's active session so team.list
	// and the send.recipient-stale hint see the fresh timestamp. A
	// no-op update (no active session for this agent) is not an error,
	// but a real DB failure is worth surfacing at debug level so a
	// future schema migration that drops sessions.last_seen_at doesn't
	// fail silently.
	if _, err := s.DB().ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ? WHERE agent_id = ? AND ended_at IS NULL`,
		timestamp, agentID,
	); err != nil {
		slog.Debug("touch session last_seen failed", "agent_id", agentID, "err", err)
	}
	return nil
}
