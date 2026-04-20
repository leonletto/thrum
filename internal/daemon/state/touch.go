package state

import (
	"context"
	"fmt"
	"time"
)

// touchDebounceWindow caps the write rate for agent last_seen_at
// updates. Activity happening more frequently than this collapses to a
// single DB write; every RPC handler can call TouchAgentLastSeen without
// worrying about SQLite churn. 30s granularity is well under the
// RecipientStaleThreshold (30m) that consumes last_seen.
const touchDebounceWindow = 30 * time.Second

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
	s.touchMu.Unlock()

	timestamp := now.Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx,
		`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`,
		timestamp, agentID,
	); err != nil {
		return fmt.Errorf("touch agent last_seen: %w", err)
	}
	return nil
}
