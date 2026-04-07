package daemon

import (
	"sync"
	"time"
)

const escalationInterval = 5 * time.Minute

type nudgeRecord struct {
	Reason    string
	Timestamp time.Time
}

// NudgeState tracks recent nudges per session to prevent duplicate notifications.
type NudgeState struct {
	mu      sync.Mutex
	records map[string]nudgeRecord
}

// NewNudgeState creates a new NudgeState.
func NewNudgeState() *NudgeState {
	return &NudgeState{records: make(map[string]nudgeRecord)}
}

// ShouldNudge returns true if a nudge should be sent for this session+reason.
func (ns *NudgeState) ShouldNudge(session, reason string) bool {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	rec, exists := ns.records[session]
	if !exists {
		return true
	}
	if rec.Reason != reason {
		return true
	}
	if time.Since(rec.Timestamp) > escalationInterval {
		return true
	}
	return false
}

// RecordNudge records that a nudge was sent.
func (ns *NudgeState) RecordNudge(session, reason string) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.records[session] = nudgeRecord{Reason: reason, Timestamp: time.Now()}
}

// Clear removes nudge state for a session (called when session produces output).
func (ns *NudgeState) Clear(session string) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	delete(ns.records, session)
}
