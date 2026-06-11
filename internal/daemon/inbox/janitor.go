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

// DefaultBackstopRetention is how long a synthetic backstop envelope is kept
// before the janitor prunes it (thrum-ist8). The backstop dispatcher writes a
// fresh "backstop-<min>.json" each tick (~15min) while unread persists, so the
// LIVE reminder is always recent; older snapshots are superseded. 1h is
// generous: > 2 backstop ticks AND an ample dead-pane window for the agent to
// consume the reminder on its next SessionStart. Once the underlying unread
// clears (or the recipient is non-resident — wo2z/saj4 stop the ticks), no
// new envelope is written and the last one ages past retention and is pruned,
// instead of accumulating forever (the 107-stale-envelope leak).
const DefaultBackstopRetention = 1 * time.Hour

// backstopTimeLayout matches the dispatcher's msg_id minute stamp
// ("backstop-" + time.Format("20060102T1504")).
const backstopTimeLayout = "20060102T1504"

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
	thrumDir          string
	localAgents       LocalAgentsFunc
	readState         MessageReadStateFunc
	interval          time.Duration
	backstopRetention time.Duration
	now               func() time.Time // injected for tests; defaults to time.Now
}

// NewSpoolJanitor constructs a janitor with the default hourly cadence.
// localAgents enumerates agent IDs owned by this daemon's host.
// readState queries the daemon's message store.
func NewSpoolJanitor(thrumDir string, localAgents LocalAgentsFunc, readState MessageReadStateFunc) *SpoolJanitor {
	return &SpoolJanitor{
		thrumDir:          thrumDir,
		localAgents:       localAgents,
		readState:         readState,
		interval:          DefaultJanitorInterval,
		backstopRetention: DefaultBackstopRetention,
		now:               time.Now,
	}
}

// SetInterval overrides the default cadence (for tests).
func (j *SpoolJanitor) SetInterval(d time.Duration) { j.interval = d }

// SetBackstopRetention overrides the backstop-envelope retention (for tests).
func (j *SpoolJanitor) SetBackstopRetention(d time.Duration) { j.backstopRetention = d }

// SetNow overrides the clock (for tests).
func (j *SpoolJanitor) SetNow(f func() time.Time) { j.now = f }

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
			// Backstop envelopes use a synthetic msg_id ("backstop-<min>")
			// that has no corresponding messages row, so readState would
			// resolve them to StateMissing and reap them prematurely
			// (thrum-7b84.3 E3) — they must NOT go through the readState
			// path below. But unconditionally skipping them leaked unbounded
			// (thrum-ist8): the dispatcher writes a fresh one each tick and
			// the janitor never pruned the superseded ones, so a persistently
			// nudged (or non-resident) agent accumulated one file per tick
			// forever. Prune backstop envelopes older than the retention
			// window; keep the recent/live reminder.
			if strings.HasPrefix(name, "backstop-") {
				if j.backstopEnvelopeStale(name, filepath.Join(spoolDir, name)) {
					if err := os.Remove(filepath.Join(spoolDir, name)); err != nil && !os.IsNotExist(err) {
						log.Printf("inbox_janitor: remove stale backstop %s: %v", name, err)
					}
				}
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

// backstopEnvelopeStale reports whether a "backstop-<min>.json" envelope is
// older than the retention window (thrum-ist8). Age is taken from the minute
// stamp embedded in the filename (the dispatcher's source of truth); if that
// can't be parsed (malformed/legacy name), it falls back to the file's
// modification time so a janitor can never leak an unparseable backstop file
// forever. A stat failure on the fallback path is treated as not-stale
// (conservative — a transient error must not delete a possibly-live reminder).
func (j *SpoolJanitor) backstopEnvelopeStale(name, path string) bool {
	cutoff := j.now().Add(-j.backstopRetention)
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, "backstop-"), ".json")
	if ts, err := time.ParseInLocation(backstopTimeLayout, stamp, time.UTC); err == nil {
		return ts.Before(cutoff)
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.ModTime().Before(cutoff)
}
