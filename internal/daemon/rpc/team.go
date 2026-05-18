package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/process"
	ttmux "github.com/leonletto/thrum/internal/tmux"
	"github.com/leonletto/thrum/internal/types"
)

// TeamListRequest represents the request for team.list RPC.
type TeamListRequest struct {
	IncludeOffline bool `json:"include_offline,omitempty"`

	// IncludeSystem, when true, surfaces identities marked
	// Reserved=true (e.g. @supervisor_<project>) that are hidden
	// from the default listing. Set via `thrum team --system`.
	IncludeSystem bool `json:"include_system,omitempty"`
}

// TeamListResponse represents the response from team.list RPC.
type TeamListResponse struct {
	Members        []TeamMember    `json:"members"`
	SharedMessages *SharedMessages `json:"shared_messages,omitempty"`
}

// SharedMessages contains team-wide message counts (broadcasts + groups).
type SharedMessages struct {
	BroadcastTotal int                 `json:"broadcast_total"`
	Groups         []GroupMessageCount `json:"groups,omitempty"`
}

// GroupMessageCount contains message counts for an agent group.
type GroupMessageCount struct {
	Name  string `json:"name"`
	Total int    `json:"total"`
}

// TeamMember represents a team member's full status.
type TeamMember struct {
	AgentID         string             `json:"agent_id"`
	Role            string             `json:"role"`
	Module          string             `json:"module"`
	Display         string             `json:"display,omitempty"`
	Hostname        string             `json:"hostname,omitempty"`
	OriginDaemon    string             `json:"origin_daemon,omitempty"`
	AgentPID        int                `json:"agent_pid,omitempty"`
	Runtime         string             `json:"runtime,omitempty"`
	WorktreePath    string             `json:"worktree,omitempty"`
	SessionID       string             `json:"session_id,omitempty"`
	SessionStart    string             `json:"session_start,omitempty"`
	LastSeen        string             `json:"last_seen,omitempty"`
	Intent          string             `json:"intent,omitempty"`
	CurrentTask     string             `json:"current_task,omitempty"`
	Branch          string             `json:"branch,omitempty"`
	UnmergedCommits int                `json:"unmerged_commits"`
	FileChanges     []types.FileChange `json:"file_changes,omitempty"`
	InboxTotal      int                `json:"inbox_total"`
	InboxUnread     int                `json:"inbox_unread"`
	Status          string             `json:"status"` // "active", "offline", or "reserved"
	TmuxSession     string             `json:"tmux_session,omitempty"`
	TmuxState       string             `json:"tmux_state,omitempty"` // alive, stale, dead, or empty

	// Reserved marks a daemon-internal pseudo-agent (e.g.
	// @supervisor_<project>) that is hidden from the default
	// `thrum team` output. Only surfaced when IncludeSystem is
	// set on the request.
	Reserved bool `json:"reserved,omitempty"`

	// IsLocal is true when the agent's OriginDaemon matches the local daemon
	// ID or is empty (legacy/fixture entries treated as local). Populated by
	// the team.list handler. Consumers (e.g. sendHints) gate heartbeat-
	// staleness checks on this field because heartbeats are DB-only and do
	// NOT propagate across peer daemons (thrum-iyrt).
	IsLocal bool `json:"is_local,omitempty"`

	// Reminders are the open reminder IDs for this agent (target_agent ==
	// agent_id, state == 'open'). Populated by HandleList when the team
	// handler has a remindersStore wired; nil/empty otherwise. Capped at
	// teamReminderCompactCap with a "... +N more" marker — full list
	// available via 'thrum agent reminder list --target @<name>'.
	Reminders []string `json:"reminders,omitempty"`

	// --- B-B1 E6.8 Task 56 fields (per spec §7.6 + §9.9.1) ---

	// Mode is the registration mode from the agents table (canonical
	// §3.3): "persistent" (long-lived registry row) or "ephemeral"
	// (per-wake; cleaned up at decommission). Empty when the agent
	// row has no mode column populated (pre-Migration-26 fixtures).
	Mode string `json:"mode,omitempty"`

	// Identity is the identity-lifecycle mode from the agents table
	// (canonical §3.3): "long_lived" (identity file persists across
	// wakes) or "ephemeral" (cleaned up alongside the worktree).
	// Empty for pre-Migration-26 rows.
	Identity string `json:"identity,omitempty"`

	// State is the canonical agent-state vocabulary per spec §6.2
	// (over_budget | dispatched | working | idle | crashed | alive).
	// Derived from scheduler_job_state.current_state + banner flags
	// per §6.2 precedence. "idle" is the default for scheduled agents
	// between wakes; "alive" is the default for personal agents
	// with healthy panes and no banners.
	State string `json:"state,omitempty"`

	// NextRun, LastRun, LastRunState come from scheduler_job_state for
	// the agent's associated scheduled_agent job. RFC3339 timestamp
	// strings (UTC); empty for personal agents with no scheduled job.
	// LastRunState is the canonical scheduler state vocabulary
	// (completed | failed | cancelled).
	NextRun      string `json:"next_run,omitempty"`
	LastRun      string `json:"last_run,omitempty"`
	LastRunState string `json:"last_run_state,omitempty"`

	// RecentTransitions are the last few entries from
	// agent_lifecycle_events (Migration 27 — B-B1 E6.7's lifecycle
	// table) for this agent, most recent first. Capped at
	// teamRecentTransitionsCap. Each entry is "<RFC3339> · <event_kind>
	// · <reason>" — pre-formatted so renderers don't need to know
	// the lifecycle schema.
	RecentTransitions []string `json:"recent_transitions,omitempty"`

	// Usage is today's telemetry summary (token + minute totals)
	// from MB-1.S6's daily_usage_summary table. Empty/nil when
	// MB-1.S6 substrate hasn't shipped (feature-detect probe) or
	// when the agent has no telemetry rows for today.
	Usage *TeamUsageSummary `json:"usage,omitempty"`

	// AutoRespawnDisabledAt is the unix-millisecond timestamp when
	// the auto-respawn loop guard tripped for this agent (zero
	// when not tripped). Surfaced via the §6.2 crashed-state banner
	// in Task 60.
	AutoRespawnDisabledAt int64 `json:"auto_respawn_disabled_at,omitempty"`

	// StateMdParseFailedAt is the unix-millisecond timestamp when
	// state.md last failed to parse (zero when clean). Surfaced via
	// the §6.2 state.md-corruption banner in Task 60.
	StateMdParseFailedAt int64 `json:"state_md_parse_failed_at,omitempty"`
}

// TeamUsageSummary is today's per-agent telemetry summary from the
// MB-1.S6 daily_usage_summary table. Optional in the team.list
// response — absent when the MB-1.S6 substrate hasn't shipped (the
// table doesn't exist) or when the agent has no telemetry rows for
// today.
type TeamUsageSummary struct {
	// TokensTotal is the sum of in + out tokens for the current
	// UTC day.
	TokensTotal int64 `json:"tokens_total"`

	// MinutesTotal is the sum of session-minute-counts for the
	// current UTC day. Useful for runtime-cost dashboards.
	MinutesTotal int64 `json:"minutes_total,omitempty"`
}

// teamRecentTransitionsCap is the maximum number of
// agent_lifecycle_events entries surfaced per agent in team.list
// (per spec §7.6 "last 5"). Larger history is available via
// `thrum team @<name> --journal` (Task 59).
const teamRecentTransitionsCap = 5

// teamReminderCompactCap is the maximum number of reminder IDs included
// per agent in a team.list response. Above this the slice's tail becomes
// a synthetic "... +N more" marker so the response stays bounded under
// many-reminders fixtures (which would otherwise balloon team.list to
// arbitrary size).
const teamReminderCompactCap = 10

// TeamHandler handles team-related RPC methods.
type TeamHandler struct {
	state              *state.State
	thrumDir           string
	supervisorIdentity *config.IdentityFile // synthesized virtual-supervisor identity; nil in tests
	remindersStore     reminders.Store      // optional; nil → skip reminder enrichment
	lifecycleStore     state.AgentLifecycleStore // optional; nil → skip lifecycle enrichment (E6.8 Task 56)
}

// NewTeamHandler creates a new team handler.
// SupervisorIdentity is the virtual-supervisor identity synthesized at
// daemon boot; it is wired in here now and consumed by ListAgents in a
// later task. Passing nil is safe — the injection path short-circuits.
//
// remindersStore is the A-B4 substrate Store used to decorate each
// agent with open-reminder IDs. Pass nil to disable enrichment (used
// by tests that don't care about reminders + by daemons running pre-
// A-B4 binaries).
func NewTeamHandler(state *state.State, thrumDir string, supervisorIdentity *config.IdentityFile, remindersStore reminders.Store) *TeamHandler {
	return &TeamHandler{
		state:              state,
		thrumDir:           thrumDir,
		supervisorIdentity: supervisorIdentity,
		remindersStore:     remindersStore,
	}
}

// SetLifecycleStore wires the B-B1 Migration 27 agent_lifecycle_events
// surface. When set, HandleList enriches each TeamMember with the
// new E6.8 Task 56 fields (mode, identity, banner flags, recent
// transitions) per spec §7.6. When nil, the new fields stay at
// their zero values — tests + pre-B-B1 daemons keep working
// without the enrichment overhead.
//
// Wired separately from NewTeamHandler (via setter) to keep the
// constructor signature stable across the many test call sites
// that don't care about lifecycle data.
func (h *TeamHandler) SetLifecycleStore(store state.AgentLifecycleStore) {
	h.lifecycleStore = store
}

// HandleList handles the team.list RPC method.
//
// Three-phase lock discipline:
//
//  1. Phase 1 acquires RLock, runs buildTeamListLocked (queries + enrichment),
//     and collects dead agents (active members whose agent_pid is no longer
//     running) into a local slice, then releases RLock.
//  2. Phase 2 runs with NO lock held and emits session.end events for each
//     dead agent via emitSessionEndForDeadAgent. Anti-pattern 1 forbids
//     holding a read lock across event emission because WriteEvent needs
//     its own write lock and nested RLock→Lock would deadlock.
//  3. Phase 3 rewrites the in-memory response to mark dead agents as
//     offline so the caller sees the self-healed state immediately.
func (h *TeamHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req TeamListRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	type deadAgent struct {
		SessionID string
		AgentID   string
		PID       int
	}

	// PHASE 1: build team list and collect dead-agent session IDs under RLock.
	h.state.RLock()
	members, shared, identityMap, err := h.buildTeamListLocked(ctx, req)
	if err != nil {
		h.state.RUnlock()
		return nil, err
	}

	var deadAgents []deadAgent
	localDaemonID := h.state.DaemonID()
	for _, m := range members {
		if m.Status != "active" ||
			m.AgentPID <= 0 ||
			process.IsRunning(m.AgentPID) ||
			m.SessionID == "" {
			continue
		}

		// Skip self-heal for cross-daemon agents. Their PID lives on a
		// remote host, so a local IsRunning check is meaningless and
		// would false-positive every synced agent into "offline".
		// Authoritative liveness for remote agents comes from sync
		// events, not local PID checks. See thrum-pxz.14.
		if m.OriginDaemon != "" && m.OriginDaemon != localDaemonID {
			continue
		}

		// Cross-check identity file: if the file reports a live PID that
		// differs from the DB's stored PID, the DB is stale but the agent
		// is actually alive. Skip the self-heal — the next
		// RefreshLocalIdentity call from that agent will reconcile the DB
		// via the always-on Fix C path into agent.register Fix A. Without
		// this guard, a fresh daemon (rebuilt from events) would emit
		// false-positive session.end events against every pre-existing
		// agent whose DB PID predates the refresh feature (thrum-pxz.14
		// Fix B).
		if idFile, ok := identityMap[m.AgentID]; ok && idFile != nil {
			if idFile.AgentPID > 0 && idFile.AgentPID != m.AgentPID && process.IsRunning(idFile.AgentPID) {
				log.Printf("team.list: stale DB PID but identity file reports live PID — skipping self-heal: agent=%s db_pid=%d file_pid=%d",
					m.AgentID, m.AgentPID, idFile.AgentPID)
				continue
			}
		}

		deadAgents = append(deadAgents, deadAgent{
			SessionID: m.SessionID,
			AgentID:   m.AgentID,
			PID:       m.AgentPID,
		})
	}
	h.state.RUnlock()

	// PHASE 2: emit session.end events without holding any lock.
	for _, d := range deadAgents {
		if emitErr := h.emitSessionEndForDeadAgent(ctx, d.SessionID); emitErr != nil {
			log.Printf("team.list: failed to emit session.end: agent=%s session=%s err=%v",
				d.AgentID, d.SessionID, emitErr)
			continue
		}
		log.Printf("team.list: marking dead agent offline: agent=%s pid=%d",
			d.AgentID, d.PID)
	}

	// PHASE 3: rewrite in-memory response so the caller sees status=offline.
	if len(deadAgents) > 0 {
		deadMap := make(map[string]bool, len(deadAgents))
		for _, d := range deadAgents {
			deadMap[d.SessionID] = true
		}
		for i := range members {
			if deadMap[members[i].SessionID] {
				members[i].Status = "offline"
			}
		}
	}

	if members == nil {
		members = []TeamMember{}
	}

	// Populate IsLocal: empty OriginDaemon is treated as local (legacy/fixture),
	// as is an OriginDaemon that matches this daemon's own ID. Any other value
	// means the agent lives on a remote peer daemon. This mirrors the self-heal
	// skip guard at the top of Phase 1 (thrum-iyrt).
	for i := range members {
		od := members[i].OriginDaemon
		members[i].IsLocal = od == "" || od == localDaemonID
	}

	var sharedPtr *SharedMessages
	if shared != nil && (shared.BroadcastTotal > 0 || len(shared.Groups) > 0) {
		sharedPtr = shared
	}

	// Decorate with open-reminder IDs. Runs outside the state lock since
	// the reminders Store is independent of state.State; nil-safe when
	// the handler was constructed without a reminders store.
	members = h.decorateWithReminders(ctx, members)

	// E6.8 Task 56: enrich with B-B1 lifecycle fields (mode, identity,
	// banner flags from the agents-table Migration 26 columns; recent
	// transitions from Migration 27's agent_lifecycle_events). Runs
	// outside the state RLock since the lifecycle store + the
	// supplementary agents-table query both use their own connections.
	// Nil-safe — pre-B-B1 daemons or tests without SetLifecycleStore
	// see the new fields stay at zero values.
	members = h.decorateWithLifecycle(ctx, members)

	return &TeamListResponse{Members: members, SharedMessages: sharedPtr}, nil
}

// decorateWithReminders attaches the open-reminder IDs to each member.
// Runs once per HandleList call. Failures on individual agents are
// logged but don't abort the response — a transient SQL error in the
// reminders Store shouldn't blank out the entire team listing.
//
// Cap at teamReminderCompactCap; over the cap, the slice tail becomes
// a synthetic "... +N more" marker so response size stays bounded.
func (h *TeamHandler) decorateWithReminders(ctx context.Context, members []TeamMember) []TeamMember {
	if h.remindersStore == nil {
		return members
	}
	for i := range members {
		// Use AgentID as the target_agent key. This matches the
		// reminders schema: target_agent is the recipient's agent_name
		// (== AgentID for named agents per identity.GenerateAgentID).
		rows, err := h.remindersStore.OpenForAgent(ctx, members[i].AgentID)
		if err != nil {
			log.Printf("team.list: reminders.OpenForAgent(%s): %v", members[i].AgentID, err)
			continue
		}
		if len(rows) == 0 {
			continue
		}
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		members[i].Reminders = capReminderIDs(ids, teamReminderCompactCap)
	}
	return members
}

// decorateWithLifecycle enriches members with the B-B1 E6.8 Task 56
// fields:
//
//   - mode, identity, AutoRespawnDisabledAt, StateMdParseFailedAt
//     from a batched agents-table query (Migration 26 columns).
//   - RecentTransitions from AgentLifecycleStore.ListByAgent (last 5
//     per spec §7.6 "transitions" field).
//   - State derived per §6.2 mapping (banner-based crashed first,
//     then default per agent kind).
//
// Failures on the bulk agents-table query or per-agent lifecycle
// reads are logged but don't blank out the response — a transient
// SQL error shouldn't lose the entire team listing. Cross-daemon
// agents (OriginDaemon set + not local) are skipped: their lifecycle
// data lives on the source daemon, not here.
//
// Nil-safe when h.lifecycleStore is unset; in that case the
// agents-table enrichment still fires (mode/identity/banner flags
// come from a SELECT, not from the lifecycle store) but
// RecentTransitions stays nil.
func (h *TeamHandler) decorateWithLifecycle(ctx context.Context, members []TeamMember) []TeamMember {
	if len(members) == 0 {
		return members
	}

	// Bulk query the agents-table Migration 26 + E6.7 columns for
	// every member in one round-trip. Cheaper than N queries when
	// the listing has many members. Empty mode/identity strings
	// (rows pre-Migration-26) are preserved as empty so renderers
	// can fall back to defaults.
	agentIDs := make([]string, 0, len(members))
	for i := range members {
		agentIDs = append(agentIDs, members[i].AgentID)
	}
	type agentMeta struct {
		mode           string
		identity       string
		autoRespawnAt  int64
		stateMdFailAt  int64
	}
	metaByID := make(map[string]agentMeta, len(members))

	// Build the IN-clause placeholders. Bounded by len(members) so
	// this is safe against unbounded expansion.
	placeholders := make([]string, len(agentIDs))
	args := make([]any, len(agentIDs))
	for i, id := range agentIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `SELECT agent_id,
		COALESCE(mode, ''),
		COALESCE(identity, ''),
		COALESCE(auto_respawn_disabled_at, 0),
		COALESCE(state_md_parse_failed_at, 0)
		FROM agents WHERE agent_id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := h.state.DB().QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("team.list: lifecycle agents-table query: %v (skipping enrichment)", err)
		return members
	}
	for rows.Next() {
		var (
			id     string
			m      agentMeta
		)
		if err := rows.Scan(&id, &m.mode, &m.identity, &m.autoRespawnAt, &m.stateMdFailAt); err != nil {
			log.Printf("team.list: lifecycle scan: %v", err)
			continue
		}
		metaByID[id] = m
	}
	if err := rows.Err(); err != nil {
		log.Printf("team.list: lifecycle rows.Err: %v", err)
	}
	_ = rows.Close()

	// Per-agent enrichment: copy meta fields onto each TeamMember,
	// derive State, and (when lifecycle store is wired) pull recent
	// transitions.
	for i := range members {
		m := &members[i]
		if meta, ok := metaByID[m.AgentID]; ok {
			m.Mode = meta.mode
			m.Identity = meta.identity
			m.AutoRespawnDisabledAt = meta.autoRespawnAt
			m.StateMdParseFailedAt = meta.stateMdFailAt
		}
		m.State = deriveAgentState(m)
		if h.lifecycleStore != nil {
			events, err := h.lifecycleStore.ListByAgent(ctx, m.AgentID, teamRecentTransitionsCap)
			if err != nil {
				log.Printf("team.list: lifecycle.ListByAgent(%s): %v", m.AgentID, err)
				continue
			}
			if len(events) == 0 {
				continue
			}
			lines := make([]string, 0, len(events))
			for _, e := range events {
				lines = append(lines, fmt.Sprintf("%s · %s · %s",
					e.EventTime.UTC().Format(time.RFC3339), e.EventKind, e.Reason))
			}
			m.RecentTransitions = lines
		}
	}
	return members
}

// deriveAgentState implements the §6.2 state-vocabulary precedence
// for a TeamMember row:
//
//  1. AutoRespawnDisabledAt set → "crashed" (loop-guard banner).
//  2. StateMdParseFailedAt set → "crashed" (state.md banner).
//  3. Otherwise → "alive" for personal agents (Status == "active"
//     with no scheduler hooks) or "idle" for scheduled agents
//     (Mode == "ephemeral" or Identity != "long_lived").
//
// Cross-epic finer states (over_budget, dispatched, working, idle
// with nudge banner) require the scheduler_job_state join that
// Task 56's next_run/last_run fields plumb through — those land
// in batch 2 once the scheduler injection is wired. For now this
// derivation surfaces the banner-driven "crashed" cases (which is
// what AC §9.9.3 + §9.9.4 require) and falls back to alive/idle
// defaults for everyone else.
func deriveAgentState(m *TeamMember) string {
	if m.AutoRespawnDisabledAt > 0 {
		return "crashed"
	}
	if m.StateMdParseFailedAt > 0 {
		return "crashed"
	}
	if m.Status == "active" {
		// Scheduled agents are typically marked Identity="ephemeral";
		// personal agents are Identity="long_lived" (canonical §3.3).
		// Until the scheduler-state join lands, treat scheduled
		// agents between wakes as "idle" and personal agents as
		// "alive".
		if m.Identity == "ephemeral" {
			return "idle"
		}
		return "alive"
	}
	return "offline"
}

// capReminderIDs returns ids unchanged when len(ids) <= limit. Above
// the limit it returns the first (limit-1) IDs plus a synthetic
// "... +N more" marker so the slice length stays at limit exactly.
// The marker is a human-facing convenience; consumers parsing this
// slice should still fall back to a full lookup via
// `thrum agent reminder list` when they need the complete set.
//
// Parameter intentionally named `limit` rather than `cap` so it
// doesn't shadow the built-in `cap()` function — even though `cap`
// isn't used inside this body today, the shadow would surprise a
// future maintainer extending the function.
func capReminderIDs(ids []string, limit int) []string {
	if len(ids) <= limit {
		return ids
	}
	out := make([]string, 0, limit)
	out = append(out, ids[:limit-1]...)
	more := len(ids) - (limit - 1)
	out = append(out, fmt.Sprintf("... +%d more", more))
	return out
}

// buildTeamListLocked runs the three SQL queries and identity-file enrichment
// pass. The caller MUST hold h.state.RLock() (or Lock()) for the duration of
// this call. It does not acquire, release, upgrade, or downgrade any lock.
//
// Returns the enriched member list, the shared-messages summary, and the
// identity map used for enrichment so callers (HandleList) can cross-check
// file-vs-DB state without re-walking worktrees.
func (h *TeamHandler) buildTeamListLocked(ctx context.Context, req TeamListRequest) ([]TeamMember, *SharedMessages, map[string]*config.IdentityFile, error) {
	// Query 1: Agents + sessions + work contexts
	//
	// worktree_path comes from agent_work_contexts first, with a fallback to
	// the session's worktree session_ref. Without this fallback, agents whose
	// agent_work_contexts row is missing (e.g. dormant-resurrect paths, or
	// heartbeats that added a worktree ref without a subsequent git context
	// extraction) would drop the `worktree` field entirely via `omitempty`
	// on TeamMember.WorktreePath. thrum-naak.
	//
	// A scalar subquery is used for the session_refs fallback (rather than a
	// LEFT JOIN) because session_refs has PK (session_id, ref_type, ref_value):
	// a session can legitimately carry multiple 'worktree' rows with different
	// values, and a LEFT JOIN on that would multiply team-member rows.
	// ORDER BY added_at DESC picks the most recently added worktree so a
	// moved worktree beats a stale historical value.
	query := `SELECT
		a.agent_id, a.role, a.module, a.display, a.hostname, a.origin_daemon, a.agent_pid,
		s.session_id, s.started_at, s.last_seen_at,
		wc.branch,
		COALESCE(NULLIF(wc.worktree_path, ''), (
			SELECT ref_value FROM session_refs
			WHERE session_id = s.session_id AND ref_type = 'worktree'
			ORDER BY added_at DESC
			LIMIT 1
		)) AS worktree_path,
		wc.intent, wc.current_task,
		wc.unmerged_commits, wc.file_changes
	FROM agents a
	LEFT JOIN sessions s ON s.agent_id = a.agent_id AND s.ended_at IS NULL
	LEFT JOIN agent_work_contexts wc ON wc.session_id = s.session_id
	WHERE 1=1`

	if !req.IncludeOffline {
		query += " AND s.session_id IS NOT NULL"
	}

	query += " ORDER BY s.started_at DESC NULLS LAST"

	rows, err := h.state.DB().QueryContext(ctx, query)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query team members: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var members []TeamMember
	memberIndex := make(map[string]int) // agent_id → index in members

	for rows.Next() {
		var m TeamMember
		var display, hostname, originDaemon sql.NullString
		var sessionID, sessionStart, lastSeen sql.NullString
		var branch, worktreePath, intent, currentTask sql.NullString
		var unmergedCommitsJSON, fileChangesJSON sql.NullString

		if err := rows.Scan(
			&m.AgentID, &m.Role, &m.Module, &display, &hostname, &originDaemon, &m.AgentPID,
			&sessionID, &sessionStart, &lastSeen,
			&branch, &worktreePath, &intent, &currentTask,
			&unmergedCommitsJSON, &fileChangesJSON,
		); err != nil {
			return nil, nil, nil, fmt.Errorf("scan team member: %w", err)
		}

		if display.Valid {
			m.Display = display.String
		}
		if hostname.Valid {
			m.Hostname = hostname.String
		}
		if originDaemon.Valid {
			m.OriginDaemon = originDaemon.String
		}
		if sessionID.Valid {
			m.SessionID = sessionID.String
			m.Status = "active"
		} else {
			m.Status = "offline"
		}
		if sessionStart.Valid {
			m.SessionStart = sessionStart.String
		}
		if lastSeen.Valid {
			m.LastSeen = lastSeen.String
		}
		if branch.Valid {
			m.Branch = branch.String
		}
		if worktreePath.Valid {
			m.WorktreePath = worktreePath.String
		}
		if intent.Valid {
			m.Intent = intent.String
		}
		if currentTask.Valid {
			m.CurrentTask = currentTask.String
		}

		// Unmarshal unmerged commits to get count
		if unmergedCommitsJSON.Valid && unmergedCommitsJSON.String != "" {
			var commits []json.RawMessage
			if err := json.Unmarshal([]byte(unmergedCommitsJSON.String), &commits); err == nil {
				m.UnmergedCommits = len(commits)
			}
		}

		// Unmarshal file changes
		if fileChangesJSON.Valid && fileChangesJSON.String != "" {
			if err := json.Unmarshal([]byte(fileChangesJSON.String), &m.FileChanges); err != nil {
				m.FileChanges = nil
			}
		}

		memberIndex[m.AgentID] = len(members)
		members = append(members, m)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("iterate team members: %w", err)
	}

	// Enrich with identity file data from ALL worktrees. The identity file
	// is authoritative for runtime, tmux_session, and tmux_state; the DB is
	// authoritative for agent_pid. The identityMap is returned to the
	// caller so Phase 1's dead-agent cross-check can reuse it without a
	// second worktree scan.
	var identityMap map[string]*config.IdentityFile
	var identityPaths map[string]string
	if h.thrumDir != "" {
		identityMap, identityPaths = readIdentitiesAndPaths(ctx, h.thrumDir)
		for i := range members {
			m := &members[i]
			idFile := identityMap[m.AgentID]
			if idFile == nil {
				continue
			}

			m.Runtime = idFile.Runtime
			m.TmuxSession = idFile.TmuxSession
			m.Reserved = idFile.Reserved

			switch {
			case idFile.TmuxSession == "":
				m.TmuxState = ""
			case !ttmux.HasSession(parseSessionName(idFile.TmuxSession)):
				m.TmuxState = "dead"
				// thrum-51cg Option B: self-heal stale TmuxSession when
				// the bound tmux session no longer exists (external kill,
				// γ reset, pane exit). Idempotent; best-effort.
				if path := identityPaths[m.AgentID]; path != "" {
					if cerr := clearDeadTmuxSessionInIdentity(path); cerr == nil {
						m.TmuxSession = ""
						m.Runtime = ""
					}
				}
			case m.AgentPID > 0 && !process.IsRunning(m.AgentPID):
				m.TmuxState = "stale"
			default:
				m.TmuxState = "alive"
			}
		}

		// When IncludeSystem is set, synthesize TeamMember entries for
		// Reserved identities that are NOT in the agents table. The
		// permission supervisor pseudo-agent is the canonical case: it
		// exists only as a reply-capable sender for nudges, never
		// registers an agent.register event, and therefore never has
		// an agents row. Without this synthesis step, `thrum team
		// --system` would return nothing for it.
		//
		// Synthesized members get Status="reserved" (distinct from
		// "active" or "offline") to make them visually distinguishable
		// in the output, and their AgentID is the identity file's
		// Agent.Name so downstream listing code sees a stable ID.
		if req.IncludeSystem {
			for name, idFile := range identityMap {
				if !idFile.Reserved {
					continue
				}
				if _, exists := memberIndex[name]; exists {
					// Already in the list from the agents-table query; the
					// enrichment loop above already populated Reserved.
					continue
				}
				synthetic := TeamMember{
					AgentID:  name,
					Role:     idFile.Agent.Role,
					Module:   idFile.Agent.Module,
					Display:  idFile.Agent.Display,
					Runtime:  idFile.Runtime,
					Status:   "reserved",
					Reserved: true,
				}
				memberIndex[name] = len(members)
				members = append(members, synthetic)
			}
		}

		// Filter out Reserved entries when IncludeSystem is NOT set.
		// This covers both (a) future agents registered via
		// agent.register that happen to have Reserved=true in their
		// identity file, and (b) paranoid defense-in-depth: if a
		// reserved synthesis ever landed by mistake without the
		// IncludeSystem flag, the filter still hides it.
		if !req.IncludeSystem {
			filtered := members[:0]
			newIndex := make(map[string]int, len(members))
			for _, m := range members {
				if m.Reserved {
					continue
				}
				newIndex[m.AgentID] = len(filtered)
				filtered = append(filtered, m)
			}
			members = filtered
			memberIndex = newIndex
		}
	}
	// Inject the virtual supervisor pseudo-agent when IncludeSystem is
	// set. After Task 7 (thrum-kqna.3) removed the Reserved=true
	// identity file from disk, the file walk above cannot find a
	// supervisor entry; the daemon carries its synthesized identity
	// in-memory and injects it here. Injection runs outside the
	// `h.thrumDir != ""` block so it works even when the file walk is
	// disabled (e.g. unit-test fixtures).
	if req.IncludeSystem && h.supervisorIdentity != nil {
		name := h.supervisorIdentity.Agent.Name
		if _, exists := memberIndex[name]; !exists {
			// No memberIndex write here: the map is discarded below via
			// `_ = memberIndex` so nothing downstream would read the entry.
			members = append(members, TeamMember{
				AgentID:  name,
				Role:     h.supervisorIdentity.Agent.Role,
				Module:   h.supervisorIdentity.Agent.Module,
				Display:  h.supervisorIdentity.Agent.Display,
				Status:   "reserved",
				Reserved: true,
			})
		}
	}

	// memberIndex is used by downstream logic below and by the caller's
	// dead-agent self-heal in HandleList, which keys off agent_id
	// (still valid after the optional filter above).
	_ = memberIndex

	// Query 2: Per-agent directed message counts (mentions only, not broadcasts/groups)
	for i, m := range members {
		values := buildForAgentValues(m.AgentID, m.Role)
		if len(values) == 0 {
			continue
		}
		placeholders := strings.Repeat("?,", len(values))
		placeholders = placeholders[:len(placeholders)-1]

		mentionQuery := fmt.Sprintf(
			`SELECT COUNT(*) FROM messages m
			 WHERE m.deleted = 0 AND m.agent_id != ?
			 AND m.message_id IN (
				SELECT mr.message_id FROM message_refs mr
				WHERE mr.ref_type = 'mention' AND mr.ref_value IN (%s)
			 )`, placeholders)
		args := []any{m.AgentID}
		for _, v := range values {
			args = append(args, v)
		}
		_ = h.state.DB().QueryRowContext(ctx, mentionQuery, args...).Scan(&members[i].InboxTotal)

		// Unread: same filter, minus messages already read
		unreadQuery := mentionQuery + " AND m.message_id NOT IN (SELECT message_id FROM message_reads WHERE agent_id = ?)"
		unreadArgs := append(args, m.AgentID)
		_ = h.state.DB().QueryRowContext(ctx, unreadQuery, unreadArgs...).Scan(&members[i].InboxUnread)
	}

	// Query 3: Shared message counts (broadcasts + per-group)
	shared := &SharedMessages{}

	// Broadcasts: messages with no mention refs and no group scopes
	_ = h.state.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM messages m
		WHERE m.deleted = 0
		AND m.message_id NOT IN (SELECT mr.message_id FROM message_refs mr WHERE mr.ref_type = 'mention')
		AND m.message_id NOT IN (SELECT ms.message_id FROM message_scopes ms WHERE ms.scope_type = 'group')`).Scan(&shared.BroadcastTotal)

	// Per-group message counts
	groupRows, err := h.state.DB().QueryContext(ctx, `SELECT ms.scope_value, COUNT(DISTINCT m.message_id)
		FROM messages m
		JOIN message_scopes ms ON m.message_id = ms.message_id AND ms.scope_type = 'group'
		WHERE m.deleted = 0
		GROUP BY ms.scope_value
		ORDER BY COUNT(DISTINCT m.message_id) DESC`)
	if err == nil {
		defer func() { _ = groupRows.Close() }()
		for groupRows.Next() {
			var gc GroupMessageCount
			if err := groupRows.Scan(&gc.Name, &gc.Total); err == nil {
				shared.Groups = append(shared.Groups, gc)
			}
		}
	}

	if members == nil {
		members = []TeamMember{}
	}

	return members, shared, identityMap, nil
}

// emitSessionEndForDeadAgent writes an agent.session.end event to the
// daemon's event log and projector. The caller MUST NOT hold h.state's
// RLock or Lock when calling — this function acquires the write lock
// internally to coordinate with other event writers.
//
// Idempotence: applySessionEnd in the projector unconditionally updates
// sessions.ended_at. Successive calls within the same team.list request
// are prevented by Phase 1's collector check (Status == "active") — the
// second team.list query sees the session as ended and does not re-queue
// it. Duplicate emissions from concurrent callers are absorbed as a
// no-op write (same session_id, same end_reason).
func (h *TeamHandler) emitSessionEndForDeadAgent(ctx context.Context, sessionID string) error {
	h.state.Lock()
	defer h.state.Unlock()

	event := types.AgentSessionEndEvent{
		Type:      "agent.session.end",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: sessionID,
		Reason:    "dead_pid",
	}
	if err := h.state.WriteEvent(ctx, event); err != nil {
		return fmt.Errorf("write session.end event: %w", err)
	}
	return nil
}

// parseSessionName extracts the tmux session name portion from a
// "session:window.pane" target string.
func parseSessionName(target string) string {
	name, _, _ := ttmux.ParseTarget(target)
	return name
}
