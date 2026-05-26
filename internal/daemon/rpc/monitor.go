package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/daemon/monitor"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
)

// MonitorHandler handles all monitor.* RPC methods. It is registered only on
// the local unix-socket server, never on the WebSocket (wsRegistry) transport.
// This trust boundary is enforced by the test in monitor_trust_boundary_test.go.
type MonitorHandler struct {
	supervisor *monitor.MonitorSupervisor
	store      *monitor.MonitorStore
	state      *state.State // for HandleLogs — queries the messages table
}

// NewMonitorHandler constructs a MonitorHandler. The state argument is used by
// HandleLogs to query recent monitor matches from the messages table.
func NewMonitorHandler(sup *monitor.MonitorSupervisor, store *monitor.MonitorStore, st *state.State) *MonitorHandler {
	return &MonitorHandler{supervisor: sup, store: store, state: st}
}

// ----- request / response types -----

// monitorStartParams is the JSON body for monitor.start.
type monitorStartParams struct {
	Name            string            `json:"name"`
	Argv            []string          `json:"argv"`
	Match           string            `json:"match"`
	Target          string            `json:"target"`
	Cwd             string            `json:"cwd"`
	Env             map[string]string `json:"env"`
	DebounceSeconds int               `json:"debounce_seconds"`
	// Schedule is an optional 5-field cron expression. When set, the
	// monitor's child is run one-shot per scheduled tick; when empty
	// (default), the monitor runs the child continuously with
	// exponential-backoff auto-restart (thrum-puhr.9).
	Schedule string `json:"schedule,omitempty"`
}

// monitorStartResponse is returned on success.
type monitorStartResponse struct {
	ID string `json:"id"`
	// PID is the child's OS process id once Runner.Run has started the
	// child. May be 0 immediately after Add returns if the onStart
	// callback has not yet fired; callers that want a guaranteed PID
	// should re-fetch via monitor.show. Review finding R2.5.
	PID int `json:"pid,omitempty"`
}

// monitorIDParams is the JSON body for stop/show/restart/logs.
type monitorIDParams struct {
	ID string `json:"id"`
}

// monitorJobView is the JSON shape returned by HandleShow and (per element) HandleList.
// Env values are ALWAYS redacted — the daemon never returns raw env values to callers.
type monitorJobView struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Argv            []string          `json:"argv"`
	Match           string            `json:"match"`
	Target          string            `json:"target"`
	Cwd             string            `json:"cwd"`
	Env             map[string]string `json:"env"`
	DebounceSeconds int               `json:"debounce_seconds"`
	Status          string            `json:"status"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	// PID of the running child (nil if stopped/dead). Added for R2.5 so
	// CLI list/show can render a pid column per design doc §'thrum monitor list'.
	PID *int `json:"pid,omitempty"`
	// Schedule is the 5-field cron expression for scheduled-mode
	// monitors. Empty for continuous monitors (thrum-puhr.9).
	Schedule string `json:"schedule,omitempty"`
}

// redactEnv returns a new map with the same keys as src but all values replaced
// with the literal string "<redacted>".  Keys remain visible so the caller can
// confirm which environment variables are configured; only the secret values are
// hidden.  Redaction is unconditional — no heuristic: every env value is redacted.
func redactEnv(src map[string]string) map[string]string {
	redacted := make(map[string]string, len(src))
	for k := range src {
		redacted[k] = "<redacted>"
	}
	return redacted
}

// jobToView converts a MonitorJob into a safe wire representation.
// ALL env values are replaced with "<redacted>" before serialization.
func jobToView(job *monitor.MonitorJob) monitorJobView {
	return monitorJobView{
		ID:              job.ID,
		Name:            job.Name,
		Argv:            job.Argv,
		Match:           job.MatchPattern,
		Target:          job.Target,
		Cwd:             job.Cwd,
		Env:             redactEnv(job.Env), // security-critical: redact before wire
		DebounceSeconds: job.DebounceSeconds,
		Status:          string(job.Status),
		CreatedAt:       job.CreatedAt,
		UpdatedAt:       job.UpdatedAt,
		PID:             job.PID,
		Schedule:        job.Schedule,
	}
}

// translateMonitorError maps typed sentinel errors from the supervisor into
// user-friendly RPC error strings.  Unknown errors are wrapped as "internal
// error: <details>" so they surface to the caller without leaking internals.
func translateMonitorError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, monitor.ErrCapExceeded):
		return fmt.Errorf("maximum concurrent monitors reached (%d)", monitor.MaxConcurrentMonitors)
	case errors.Is(err, monitor.ErrNameTaken):
		return fmt.Errorf("monitor name already in use")
	case errors.Is(err, monitor.ErrDebounceTooShort):
		return fmt.Errorf("debounce must be at least %d seconds", monitor.MinDebounceSeconds)
	case errors.Is(err, monitor.ErrInvalidRegex):
		return fmt.Errorf("invalid match pattern: %v", unwrapMessage(err))
	case errors.Is(err, monitor.ErrInvalidSchedule):
		return fmt.Errorf("invalid schedule: %v", unwrapMessage(err))
	case errors.Is(err, monitor.ErrNotFound):
		return fmt.Errorf("monitor not found")
	default:
		return fmt.Errorf("internal error: %v", err)
	}
}

// unwrapMessage returns the error message of err's deepest Unwrap, or the
// message of err itself when there is no cause.  Used to extract the
// regexp compilation detail from a wrapped ErrInvalidRegex.
func unwrapMessage(err error) string {
	u := errors.Unwrap(err)
	if u != nil {
		return u.Error()
	}
	return err.Error()
}

// ----- handlers -----

// ensureMonitorSender inserts (or updates) a synthetic agent row and an open
// session for "monitor:<name>" into the DB so that Delivery.Deliver can call
// MessageHandler.HandleSend without getting "no active session found".
//
// HandleSend resolves the caller's session via resolveAgentAndSession, which
// requires both an agents row AND an open sessions row (ended_at IS NULL) for
// the CallerAgentID.  Without these rows every delivery silently fails because
// the error is swallowed in the deliver closure inside supervisor.launch.
//
// The inserts use INSERT OR IGNORE / INSERT OR REPLACE semantics so this is
// safe to call multiple times (e.g. monitor.restart) and safe on daemon
// restart when the rows are already present from the initial HandleStart.
func (h *MonitorHandler) ensureMonitorSender(ctx context.Context, monitorName string) error {
	callerID := "monitor:" + monitorName
	now := time.Now().UTC().Format(time.RFC3339)

	// Upsert the synthetic agent row.  On conflict (agent already registered
	// from a previous run) just update last_seen_at so the row stays fresh.
	_, err := h.state.DB().ExecContext(ctx, `
		INSERT INTO agents (agent_id, kind, role, module, display, hostname, agent_pid, registered_at, last_seen_at)
		VALUES (?, 'monitor', 'monitor', 'monitor', ?, '', 0, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET last_seen_at = excluded.last_seen_at
	`, callerID, monitorName, now, now)
	if err != nil {
		return fmt.Errorf("ensure monitor agent row: %w", err)
	}

	// Insert a fresh open session.  A new session ID is generated on each
	// HandleStart call so a restarted monitor gets a clean session.  Any
	// previous open sessions for this callerID are closed first to avoid
	// accumulating stale rows.
	_, err = h.state.DB().ExecContext(ctx, `
		UPDATE sessions SET ended_at = ?, end_reason = 'superseded'
		WHERE agent_id = ? AND ended_at IS NULL
	`, now, callerID)
	if err != nil {
		return fmt.Errorf("close stale monitor sessions: %w", err)
	}

	sessionID := identity.GenerateSessionID()
	_, err = h.state.DB().ExecContext(ctx, `
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES (?, ?, ?, ?)
	`, sessionID, callerID, now, now)
	if err != nil {
		return fmt.Errorf("ensure monitor session row: %w", err)
	}

	return nil
}

// EnsureAllMonitorSenders registers (or refreshes) synthetic agent+session
// rows for every monitor currently in StatusRunning in the store.  Call this
// once after MonitorSupervisor.Start() returns (or just before it is called)
// so that monitors persisted from a previous daemon run — which predate the
// ensureMonitorSender fix — also get valid sender rows before their runners
// emit their first match.
func (h *MonitorHandler) EnsureAllMonitorSenders(ctx context.Context) {
	jobs, err := h.store.ListByStatus(ctx, monitor.StatusRunning)
	if err != nil {
		fmt.Printf("monitor: warn: could not list running monitors for sender setup: %v\n", err)
		return
	}
	for _, job := range jobs {
		if err := h.ensureMonitorSender(ctx, job.Name); err != nil {
			fmt.Printf("monitor: warn: could not ensure sender for %q: %v\n", job.Name, err)
		}
	}
}

// HandleStart handles monitor.start — validates and launches a new monitor.
func (h *MonitorHandler) HandleStart(ctx context.Context, params json.RawMessage) (any, error) {
	var req monitorStartParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.Env == nil {
		req.Env = make(map[string]string)
	}

	id, err := h.supervisor.Add(ctx, monitor.SubmitSpec{
		Name:            req.Name,
		Argv:            req.Argv,
		MatchPattern:    req.Match,
		Target:          req.Target,
		Cwd:             req.Cwd,
		Env:             req.Env,
		DebounceSeconds: req.DebounceSeconds,
		Schedule:        req.Schedule,
	})
	if err != nil {
		return nil, translateMonitorError(err)
	}

	// Register a synthetic agent + open session for "monitor:<name>" so that
	// Delivery.Deliver → MessageHandler.HandleSend can resolve the caller's
	// session.  Without these rows every match delivery silently fails because
	// resolveAgentAndSession returns an error that the deliver closure ignores.
	// This is the P0 bug: monitors run fine but no messages are ever delivered.
	if err := h.ensureMonitorSender(ctx, req.Name); err != nil {
		// Non-fatal: log and continue.  The monitor is running; delivery may
		// still fail but the operator can retry via monitor.restart.
		// We do NOT roll back the Add — the monitor spec is valid and the
		// child is already running.
		fmt.Printf("monitor: warn: could not register sender for %q: %v\n", req.Name, err)
	}

	// Include the child PID in the response so the CLI can echo
	// "Started monitor <name> (<id>) — pid <N>, target <target>" per
	// design doc §'thrum monitor add' (review finding R2.5). We re-fetch
	// the job via GetByID (which consults the live handle) because the
	// onStart callback may fire a few ms after Add returns — GetByID
	// reads whatever the handle has published by now.
	resp := monitorStartResponse{ID: id}
	if job, gerr := h.supervisor.GetByID(ctx, id); gerr == nil && job.PID != nil {
		resp.PID = *job.PID
	}
	return resp, nil
}

// HandleStop handles monitor.stop — signals the child and removes the row.
func (h *MonitorHandler) HandleStop(ctx context.Context, params json.RawMessage) (any, error) {
	var req monitorIDParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if err := h.supervisor.Stop(ctx, req.ID); err != nil {
		return nil, translateMonitorError(err)
	}
	return map[string]string{"status": "stopped"}, nil
}

// monitorListParams holds optional filters for monitor.list.
type monitorListParams struct {
	// IncludeAll, when true, also returns stopped/dead monitors. By
	// default only monitors with Status=running are returned. When true,
	// dead/stopped monitors older than 1 week are still hidden (review
	// finding R2.3, matches design doc §'thrum monitor list').
	IncludeAll bool `json:"include_all,omitempty"`
}

// HandleList handles monitor.list — returns monitors with env redacted.
// By default only running monitors are returned; pass include_all=true to
// include stopped/dead entries (younger than 1 week).
func (h *MonitorHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req monitorListParams
	if len(params) > 0 {
		_ = json.Unmarshal(params, &req) // optional params; errors ignored
	}
	jobs, err := h.supervisor.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("internal error: %v", err)
	}
	views := make([]monitorJobView, 0, len(jobs))
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, job := range jobs {
		// Default mode: running only.
		if !req.IncludeAll && job.Status != monitor.StatusRunning {
			continue
		}
		// --all mode: still hide dead/stopped older than a week.
		if req.IncludeAll && job.Status != monitor.StatusRunning {
			if job.UpdatedAt.Before(cutoff) {
				continue
			}
		}
		views = append(views, jobToView(job)) // env redacted inside jobToView
	}
	return views, nil
}

// HandleShow handles monitor.show — returns a single monitor's spec with env redacted.
func (h *MonitorHandler) HandleShow(ctx context.Context, params json.RawMessage) (any, error) {
	var req monitorIDParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	job, err := h.supervisor.GetByID(ctx, req.ID)
	if err != nil {
		return nil, translateMonitorError(err)
	}
	return jobToView(job), nil // env redacted inside jobToView
}

// HandleRestart handles monitor.restart — stops the existing child and
// re-launches it with the persisted spec, PRESERVING the monitor ID.
//
// Delegates to Supervisor.Restart, which atomically stops (without
// deleting the row), refreshes mutable fields, and re-launches using the
// supervisor's long-lived base context. See review finding R2.1.
func (h *MonitorHandler) HandleRestart(ctx context.Context, params json.RawMessage) (any, error) {
	var req monitorIDParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.ID == "" {
		return nil, fmt.Errorf("id is required")
	}

	if err := h.supervisor.Restart(ctx, req.ID); err != nil {
		return nil, translateMonitorError(err)
	}

	// Refresh the synthetic sender registration so the restarted monitor's
	// delivery calls see a valid open session.
	if job, gerr := h.supervisor.GetByID(ctx, req.ID); gerr == nil {
		if sErr := h.ensureMonitorSender(ctx, job.Name); sErr != nil {
			fmt.Printf("monitor: warn: could not refresh sender for %q: %v\n", job.Name, sErr)
		}
	}

	return monitorStartResponse{ID: req.ID}, nil
}

// monitorLogsParams holds the parameters for monitor.logs.
type monitorLogsParams struct {
	ID    string `json:"id"`
	Limit int    `json:"limit,omitempty"` // defaults to 20 if unset
}

// monitorLogEntry is one row in the HandleLogs response. It maps directly
// to a messages-table row for a monitor match.
type monitorLogEntry struct {
	MessageID string    `json:"message_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// HandleLogs returns the last N synthetic messages delivered by the monitor
// with the given ID. Queries the messages table on agent_id = "monitor:<name>"
// (the caller ID used by the Delivery helper) ordered by created_at DESC.
//
// Raw stdout streaming ('thrum monitor tail') is a separate feature deferred
// to thrum-86r.4; this handler is the historical-match lookup required by
// v1 design doc §'thrum monitor logs'. Review finding R2.2.
func (h *MonitorHandler) HandleLogs(ctx context.Context, params json.RawMessage) (any, error) {
	var req monitorLogsParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	// Resolve ID → monitor name so we can build the sender caller ID that
	// Delivery used ("monitor:<name>").
	job, err := h.store.GetByID(ctx, req.ID)
	if err != nil {
		return nil, translateMonitorError(err)
	}
	callerID := "monitor:" + job.Name

	rows, err := h.state.DB().QueryContext(ctx, `
		SELECT message_id, body_content, created_at
		FROM messages
		WHERE agent_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, callerID, req.Limit)
	if err != nil {
		return nil, fmt.Errorf("internal error: query monitor logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	entries := make([]monitorLogEntry, 0, req.Limit)
	for rows.Next() {
		var e monitorLogEntry
		var createdAt string
		if scanErr := rows.Scan(&e.MessageID, &e.Content, &createdAt); scanErr != nil {
			return nil, fmt.Errorf("internal error: scan monitor log row: %w", scanErr)
		}
		if t, parseErr := time.Parse(time.RFC3339, createdAt); parseErr == nil {
			e.CreatedAt = t
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("internal error: iterate monitor log rows: %w", err)
	}

	return entries, nil
}
