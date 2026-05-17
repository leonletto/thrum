package reminders

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Handler is the JSON-RPC entry point for the reminders namespace. Wraps
// a Store. Six methods land at registration: reminder.set, .get, .list,
// .defer, .clear, .cancel.
//
// Authorization (canonical §3.5): uniform "any identified caller" rule.
// The `by` field on Defer/Clear/Cancel is recorded for audit but not
// trust-gated — the caller's identity is verified by the JSON-RPC
// transport layer (peer-cred over Unix socket) before the handler is
// invoked. The handler treats `by` as a label, not as an authentication
// claim.
type Handler struct {
	store Store
}

// NewHandler wires the handler to a Store. In production cmd/thrum
// passes the SQLStore; tests substitute in-memory implementations.
func NewHandler(store Store) *Handler {
	return &Handler{store: store}
}

// --- reminder.set ---

// setParams is the wire format for reminder.set. trigger_at is unix
// seconds (matches canonical §3.5 INTEGER columns). target_chain is
// optional; agent/user time reminders use target_agent.
type setParams struct {
	Source      string `json:"source"`
	SourceAgent string `json:"source_agent,omitempty"`
	TriggerAt   int64  `json:"trigger_at"`
	TargetAgent string `json:"target_agent"`
	Body        string `json:"body"`
}

type setResponse struct {
	ID             string `json:"id"`
	RaisedAt       int64  `json:"raised_at"`
	NextReminderAt int64  `json:"next_reminder_at"`
}

// HandleSet mints an agent/time or user/time reminder. Daemon-source
// reminders are minted directly by the daemon (sweep handler, C-B1
// staleness pings) — not through this user-facing RPC.
func (h *Handler) HandleSet(ctx context.Context, params json.RawMessage) (any, error) {
	var p setParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("reminder.set: invalid params: %w", err)
	}
	src := Source(p.Source)
	if src != SourceAgent && src != SourceUser {
		return nil, fmt.Errorf("reminder.set: source must be 'agent' or 'user', got %q", p.Source)
	}
	if p.TriggerAt == 0 {
		return nil, fmt.Errorf("reminder.set: trigger_at required")
	}
	triggerAt := time.Unix(p.TriggerAt, 0).UTC()
	r := &Reminder{
		Source:      src,
		SourceAgent: p.SourceAgent,
		TriggerKind: TriggerTime,
		TriggerAt:   &triggerAt,
		TargetAgent: p.TargetAgent,
		Body:        p.Body,
	}
	if err := h.store.Mint(ctx, r); err != nil {
		return nil, fmt.Errorf("reminder.set: %w", err)
	}
	return setResponse{
		ID:             r.ID,
		RaisedAt:       r.RaisedAt.Unix(),
		NextReminderAt: triggerAt.Unix(),
	}, nil
}

// --- reminder.get ---

type getParams struct {
	ID string `json:"id"`
}

// HandleGet returns the full Reminder row. Surface a structured "not
// found" error rather than a raw sql.ErrNoRows so callers can branch.
func (h *Handler) HandleGet(ctx context.Context, params json.RawMessage) (any, error) {
	var p getParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("reminder.get: invalid params: %w", err)
	}
	if p.ID == "" {
		return nil, fmt.Errorf("reminder.get: id required")
	}
	r, err := h.store.Get(ctx, p.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("reminder.get: not found: %s", p.ID)
		}
		return nil, fmt.Errorf("reminder.get: %w", err)
	}
	return r, nil
}

// --- reminder.list ---

// listParams mirrors ListFilter at the wire layer. Pointer / empty-string
// rules: omit a field to skip that column entirely. Server-side
// translation copies into ListFilter exactly.
type listParams struct {
	Source      *string `json:"source,omitempty"`
	TriggerKind *string `json:"trigger_kind,omitempty"`
	State       *string `json:"state,omitempty"`
	TargetAgent string  `json:"target_agent,omitempty"`
	SourceAgent string  `json:"source_agent,omitempty"`
	Limit       int     `json:"limit,omitempty"`
}

// HandleList returns rows matching the filter. Returns an empty slice
// (not null) when there are no matches so callers can iterate
// unconditionally.
func (h *Handler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var p listParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("reminder.list: invalid params: %w", err)
		}
	}
	filter := ListFilter{
		TargetAgent: p.TargetAgent,
		SourceAgent: p.SourceAgent,
		Limit:       p.Limit,
	}
	if p.Source != nil {
		s := Source(*p.Source)
		filter.Source = &s
	}
	if p.TriggerKind != nil {
		k := TriggerKind(*p.TriggerKind)
		filter.TriggerKind = &k
	}
	if p.State != nil {
		st := State(*p.State)
		filter.State = &st
	}
	rows, err := h.store.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("reminder.list: %w", err)
	}
	if rows == nil {
		rows = []*Reminder{}
	}
	return rows, nil
}

// --- reminder.defer ---

type deferParams struct {
	ID    string `json:"id"`
	Until int64  `json:"until"`
	By    string `json:"by"`
}

type okResponse struct {
	OK bool `json:"ok"`
}

// HandleDefer pushes the next fire out. Requires state=open at the
// Store layer; terminal-row attempts surface as ErrTerminalState.
func (h *Handler) HandleDefer(ctx context.Context, params json.RawMessage) (any, error) {
	var p deferParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("reminder.defer: invalid params: %w", err)
	}
	if p.ID == "" {
		return nil, fmt.Errorf("reminder.defer: id required")
	}
	if p.Until == 0 {
		return nil, fmt.Errorf("reminder.defer: until required")
	}
	until := time.Unix(p.Until, 0).UTC()
	if err := h.store.Defer(ctx, p.ID, until, p.By); err != nil {
		return nil, fmt.Errorf("reminder.defer: %w", err)
	}
	return okResponse{OK: true}, nil
}

// --- reminder.clear ---

type byParams struct {
	ID string `json:"id"`
	By string `json:"by"`
}

// HandleClear transitions the row to cleared (terminal). Cleared rows
// stop firing; a future stall episode re-mints a fresh row via the
// sweep idempotency match-key.
func (h *Handler) HandleClear(ctx context.Context, params json.RawMessage) (any, error) {
	var p byParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("reminder.clear: invalid params: %w", err)
	}
	if p.ID == "" {
		return nil, fmt.Errorf("reminder.clear: id required")
	}
	if err := h.store.Clear(ctx, p.ID, p.By); err != nil {
		return nil, fmt.Errorf("reminder.clear: %w", err)
	}
	return okResponse{OK: true}, nil
}

// --- reminder.cancel ---

// HandleCancel transitions the row to cancelled (terminal). Distinct
// from clear so observability can tell operator-dismissal from
// "no-longer-relevant" later.
func (h *Handler) HandleCancel(ctx context.Context, params json.RawMessage) (any, error) {
	var p byParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("reminder.cancel: invalid params: %w", err)
	}
	if p.ID == "" {
		return nil, fmt.Errorf("reminder.cancel: id required")
	}
	if err := h.store.Cancel(ctx, p.ID, p.By); err != nil {
		return nil, fmt.Errorf("reminder.cancel: %w", err)
	}
	return okResponse{OK: true}, nil
}
