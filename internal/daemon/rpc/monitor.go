package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/daemon/monitor"
	"github.com/leonletto/thrum/internal/daemon/state"
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
}

// monitorStartResponse is returned on success.
type monitorStartResponse struct {
	ID string `json:"id"`
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
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
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
		CreatedAt:       job.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:       job.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
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
	})
	if err != nil {
		return nil, translateMonitorError(err)
	}
	return monitorStartResponse{ID: id}, nil
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

// HandleList handles monitor.list — returns all monitors with env redacted.
func (h *MonitorHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	jobs, err := h.supervisor.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("internal error: %v", err)
	}
	views := make([]monitorJobView, 0, len(jobs))
	for _, job := range jobs {
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
