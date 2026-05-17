package cli

import (
	"fmt"
	"time"
)

// reminderRPC is the RPC surface used by the reminder.* helpers. *Client
// satisfies it implicitly; tests substitute a fake to assert on
// marshalled params without spinning up a Unix socket.
//
// Naming: lowercase because callers from cmd/thrum/main.go pass *Client
// directly — they never need to reference the interface name.
type reminderRPC interface {
	Call(method string, params any, result any) error
}

// ReminderSetOpts are the inputs for `thrum agent reminder set`.
type ReminderSetOpts struct {
	Source      string    // "agent" or "user"
	SourceAgent string    // empty when Source != "agent"
	TriggerAt   time.Time // absolute fire time
	TargetAgent string    // recipient agent name; empty == self (caller resolves)
	Body        string    // reminder body (shown in lookup, NOT in fire message)
}

// ReminderSetResult is what the daemon returns to `reminder.set`.
type ReminderSetResult struct {
	ID             string
	RaisedAt       time.Time
	NextReminderAt time.Time
}

// ReminderSet calls reminder.set on the daemon. trigger_at goes over the
// wire as unix seconds (int64) to match the canonical §3.5 INTEGER
// columns; the helper handles the time.Time ↔ unix conversion so
// callers stay in Go time idioms.
func ReminderSet(client reminderRPC, opts ReminderSetOpts) (*ReminderSetResult, error) {
	params := map[string]any{
		"source":       opts.Source,
		"source_agent": opts.SourceAgent,
		"trigger_at":   opts.TriggerAt.Unix(),
		"target_agent": opts.TargetAgent,
		"body":         opts.Body,
	}
	var resp struct {
		ID             string `json:"id"`
		RaisedAt       int64  `json:"raised_at"`
		NextReminderAt int64  `json:"next_reminder_at"`
	}
	if err := client.Call("reminder.set", params, &resp); err != nil {
		return nil, fmt.Errorf("reminder.set RPC failed: %w", err)
	}
	return &ReminderSetResult{
		ID:             resp.ID,
		RaisedAt:       time.Unix(resp.RaisedAt, 0).UTC(),
		NextReminderAt: time.Unix(resp.NextReminderAt, 0).UTC(),
	}, nil
}

// ReminderGet calls reminder.get on the daemon. The daemon returns the
// full row with Go-default JSON marshaling; we decode into the wire-shape
// struct here rather than depend on the daemon's internal Reminder type
// so the CLI stays decoupled from the storage representation.
//
// Pointer fields mirror NULL columns: nil means the column was NULL,
// non-nil means it had a value.
type ReminderRow struct {
	ID             string
	Source         string
	SourceAgent    string
	TriggerKind    string
	TriggerAt      *time.Time
	TargetAgent    string
	TargetChain    []string
	Body           string
	RaisedAt       time.Time
	NextReminderAt *time.Time
	LastFiredAt    *time.Time
	State          string
	PaneSnapshot   string
	ClearedAt      *time.Time
	CancelledAt    *time.Time
}

// reminderWire is the JSON shape produced by the daemon's reminder.get /
// reminder.list handlers (Go-default marshaling of the daemon's
// reminders.Reminder struct). RFC3339 times for non-NULL columns; null
// for NULL columns.
type reminderWire struct {
	ID             string     `json:"ID"`
	Source         string     `json:"Source"`
	SourceAgent    string     `json:"SourceAgent"`
	TriggerKind    string     `json:"TriggerKind"`
	TriggerAt      *time.Time `json:"TriggerAt"`
	TargetAgent    string     `json:"TargetAgent"`
	TargetChain    []string   `json:"TargetChain"`
	Body           string     `json:"Body"`
	RaisedAt       time.Time  `json:"RaisedAt"`
	NextReminderAt *time.Time `json:"NextReminderAt"`
	LastFiredAt    *time.Time `json:"LastFiredAt"`
	State          string     `json:"State"`
	PaneSnapshot   string     `json:"PaneSnapshot"`
	ClearedAt      *time.Time `json:"ClearedAt"`
	CancelledAt    *time.Time `json:"CancelledAt"`
}

func wireToRow(w reminderWire) ReminderRow {
	return ReminderRow{
		ID:             w.ID,
		Source:         w.Source,
		SourceAgent:    w.SourceAgent,
		TriggerKind:    w.TriggerKind,
		TriggerAt:      w.TriggerAt,
		TargetAgent:    w.TargetAgent,
		TargetChain:    w.TargetChain,
		Body:           w.Body,
		RaisedAt:       w.RaisedAt,
		NextReminderAt: w.NextReminderAt,
		LastFiredAt:    w.LastFiredAt,
		State:          w.State,
		PaneSnapshot:   w.PaneSnapshot,
		ClearedAt:      w.ClearedAt,
		CancelledAt:    w.CancelledAt,
	}
}

// ReminderGet calls reminder.get on the daemon and decodes the full row.
func ReminderGet(client reminderRPC, id string) (*ReminderRow, error) {
	params := map[string]any{"id": id}
	var w reminderWire
	if err := client.Call("reminder.get", params, &w); err != nil {
		return nil, fmt.Errorf("reminder.get RPC failed: %w", err)
	}
	r := wireToRow(w)
	return &r, nil
}

// ReminderListOpts mirrors the daemon's listParams. Empty pointer / zero
// string fields are omitted from the request — the daemon treats those
// as "don't filter on this column".
type ReminderListOpts struct {
	Source      string // "" → no filter; "agent" / "user" / "daemon"
	TriggerKind string // "" → no filter
	State       string // "" → no filter; "open" / "fired" / "cleared" / "cancelled"
	TargetAgent string
	SourceAgent string
	Limit       int
}

// ReminderList calls reminder.list and decodes the result slice.
func ReminderList(client reminderRPC, opts ReminderListOpts) ([]ReminderRow, error) {
	params := map[string]any{}
	if opts.Source != "" {
		params["source"] = opts.Source
	}
	if opts.TriggerKind != "" {
		params["trigger_kind"] = opts.TriggerKind
	}
	if opts.State != "" {
		params["state"] = opts.State
	}
	if opts.TargetAgent != "" {
		params["target_agent"] = opts.TargetAgent
	}
	if opts.SourceAgent != "" {
		params["source_agent"] = opts.SourceAgent
	}
	if opts.Limit > 0 {
		params["limit"] = opts.Limit
	}
	var wires []reminderWire
	if err := client.Call("reminder.list", params, &wires); err != nil {
		return nil, fmt.Errorf("reminder.list RPC failed: %w", err)
	}
	rows := make([]ReminderRow, 0, len(wires))
	for _, w := range wires {
		rows = append(rows, wireToRow(w))
	}
	return rows, nil
}

// ReminderDefer calls reminder.defer. `until` is the new fire time; `by`
// is the caller's identifier (recorded for audit).
func ReminderDefer(client reminderRPC, id string, until time.Time, by string) error {
	params := map[string]any{
		"id":    id,
		"until": until.Unix(),
		"by":    by,
	}
	var resp struct {
		OK bool `json:"ok"`
	}
	if err := client.Call("reminder.defer", params, &resp); err != nil {
		return fmt.Errorf("reminder.defer RPC failed: %w", err)
	}
	return nil
}

// ReminderClear calls reminder.clear.
func ReminderClear(client reminderRPC, id string, by string) error {
	params := map[string]any{"id": id, "by": by}
	var resp struct {
		OK bool `json:"ok"`
	}
	if err := client.Call("reminder.clear", params, &resp); err != nil {
		return fmt.Errorf("reminder.clear RPC failed: %w", err)
	}
	return nil
}

// ReminderCancel calls reminder.cancel.
func ReminderCancel(client reminderRPC, id string, by string) error {
	params := map[string]any{"id": id, "by": by}
	var resp struct {
		OK bool `json:"ok"`
	}
	if err := client.Call("reminder.cancel", params, &resp); err != nil {
		return fmt.Errorf("reminder.cancel RPC failed: %w", err)
	}
	return nil
}
