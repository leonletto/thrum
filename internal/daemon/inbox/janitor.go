// internal/daemon/inbox/janitor.go
package inbox

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReadState reports the daemon's view of a message's read-state.
type ReadState int

const (
	// StateUnread means the message exists and has not been marked read.
	StateUnread ReadState = iota
	// StateRead means the message exists and has been marked read.
	StateRead
	// StateMissing means the message is not in the daemon's store.
	StateMissing
)

// DefaultJanitorInterval is the cadence for the hourly reconcile.
const DefaultJanitorInterval = 1 * time.Hour

// LocalAgentsFunc returns the list of agent IDs owned by this daemon's host.
type LocalAgentsFunc func() []string

// MessageReadStateFunc reports the read-state of a message for a specific
// recipient agent. Implementations typically query the daemon's SQLite
// message store, joining the messages and message_deliveries tables.
type MessageReadStateFunc func(msgID, agentID string) ReadState

// SpoolJanitor reconciles <thrum_dir>/spool/<agent_id>/*.json entries
// against the daemon's SQLite read-state. Entries whose msg_id is
// marked read (or is entirely absent) are deleted. Unread entries are
// kept — they represent legitimate pending nudges.
type SpoolJanitor struct {
	thrumDir    string
	localAgents LocalAgentsFunc
	readState   MessageReadStateFunc
	interval    time.Duration
}

// NewSpoolJanitor constructs a janitor with the default hourly cadence.
// localAgents enumerates agent IDs owned by this daemon's host.
// readState queries the daemon's message store.
func NewSpoolJanitor(thrumDir string, localAgents LocalAgentsFunc, readState MessageReadStateFunc) *SpoolJanitor {
	return &SpoolJanitor{
		thrumDir:    thrumDir,
		localAgents: localAgents,
		readState:   readState,
		interval:    DefaultJanitorInterval,
	}
}

// SetInterval overrides the default cadence (for tests).
func (j *SpoolJanitor) SetInterval(d time.Duration) { j.interval = d }

// Start blocks until the context is canceled, running Reconcile once
// immediately and then on every tick.
func (j *SpoolJanitor) Start(ctx context.Context) {
	log.Printf("inbox_janitor: starting with interval=%s", j.interval)
	j.Reconcile()
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("inbox_janitor: stopping")
			return
		case <-ticker.C:
			j.Reconcile()
		}
	}
}

// Reconcile walks each local agent's spool dir and deletes files that
// correspond to read or missing messages. Safe to call concurrently
// with WriteSpool — missing-file races on delete resolve cleanly.
func (j *SpoolJanitor) Reconcile() {
	for _, agentID := range j.localAgents() {
		spoolDir := filepath.Join(j.thrumDir, "spool", agentID)
		entries, err := os.ReadDir(spoolDir)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("inbox_janitor: read %s: %v", spoolDir, err)
			}
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".tmp-") {
				continue
			}
			// thrum-7b84.3 E3: backstop envelopes use a synthetic msg_id
			// ("backstop-<min>") that has no corresponding messages row.
			// readState would return StateMissing and the janitor would
			// reap the file before its check-inbox.sh consumer could see
			// it. The backstop dispatcher writes a fresh envelope every
			// tick, so a per-minute file that survives until the next
			// tick is correct behavior; the janitor must not delete it.
			if strings.HasPrefix(name, "backstop-") {
				continue
			}
			msgID := strings.TrimSuffix(name, ".json")
			switch j.readState(msgID, agentID) {
			case StateRead, StateMissing:
				if err := os.Remove(filepath.Join(spoolDir, name)); err != nil && !os.IsNotExist(err) {
					log.Printf("inbox_janitor: remove %s: %v", name, err)
				}
			case StateUnread:
				// keep
			}
		}
	}
}
