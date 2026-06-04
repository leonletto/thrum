package schema

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// migration_status.go — observable migration progress for the daemon start-wait
// path (thrum-vh2c). When a daemon boot must run a long schema migration (e.g.
// v24->v41 on a large DB), it writes a heartbeating status file next to the DB
// so the waiting CLI can (a) show a progress spinner and (b) distinguish "a
// migration is actively running" from "the daemon hung" — extending the
// start-wait while the heartbeat advances instead of false-timing-out, while
// still bounding the wait for a genuinely stuck daemon (heartbeat frozen).

// MigrationStatusFile is the name of the migration-progress status file written
// under the daemon's var directory while a schema migration is in progress.
const MigrationStatusFile = "migration.status"

// MigrationStatus is the on-disk shape of the migration-progress file. It is
// written by the migrating daemon and read by the waiting CLI start-wait loop.
type MigrationStatus struct {
	FromVersion int    `json:"from_version"`
	ToVersion   int    `json:"to_version"`
	PID         int    `json:"pid"`
	Phase       string `json:"phase"`      // "backup" | "migrating"
	StartedAt   string `json:"started_at"` // RFC3339Nano
	UpdatedAt   string `json:"updated_at"` // RFC3339Nano — bumped each heartbeat
	Heartbeat   int64  `json:"heartbeat"`  // monotonic counter, increments each tick
}

// Migration phase labels.
const (
	MigrationPhaseBackup    = "backup"
	MigrationPhaseMigrating = "migrating"
)

// reporterHeartbeatInterval is how often the migration reporter rewrites the
// status file with an advanced heartbeat. Kept short relative to the CLI's
// stall window so a live migration always looks "progressing" to the waiter.
const reporterHeartbeatInterval = 500 * time.Millisecond

// MigrationStatusPath returns the path to the migration status file under varDir.
func MigrationStatusPath(varDir string) string {
	return filepath.Join(varDir, MigrationStatusFile)
}

// ClearStaleMigrationStatus removes any leftover migration status file (and its
// temp sidecar) under varDir. The daemon calls this at boot start so a status
// file orphaned by a previously crashed migration — a frozen heartbeat from a
// dead process — can't make the waiting CLI extend its start-wait against a
// daemon that isn't actually migrating. A real migration re-creates the file
// with a live heartbeat. Best-effort: removal errors (including absent files)
// are ignored, and calling it on an already-clean dir is a safe no-op.
func ClearStaleMigrationStatus(varDir string) {
	path := MigrationStatusPath(varDir)
	_ = os.Remove(path)
	_ = os.Remove(path + ".tmp")
}

// ReadMigrationStatus reads and parses the migration status file under varDir.
// Returns (nil, nil) when the file does not exist (no migration in progress).
// Writes are atomic (temp + rename), so a reader only ever sees a complete prior
// or complete new file — never a partial one. A stale or corrupted file returns
// a non-nil error; callers treat that as "no new info" and retry on the next
// poll.
func ReadMigrationStatus(varDir string) (*MigrationStatus, error) {
	data, err := os.ReadFile(MigrationStatusPath(varDir)) // #nosec G304 -- path derived from daemon-owned var dir, not user input
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var st MigrationStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// migrationReporter writes the migration status file and advances its heartbeat
// on a background goroutine until Done is called. It is the daemon-side writer;
// the CLI start-wait loop is the reader. Writes are atomic (temp + rename) so a
// concurrent reader never sees a half-written file.
type migrationReporter struct {
	varDir string
	mu     sync.Mutex
	status MigrationStatus
	stop   chan struct{}
	done   chan struct{}
}

// startMigrationReporter writes an initial status file (phase=backup) for the
// from->to migration and starts the heartbeat goroutine. Returns nil if the
// initial write fails — migration progress reporting is best-effort and must
// never block or fail the migration itself.
func startMigrationReporter(varDir string, from, to int) *migrationReporter {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r := &migrationReporter{
		varDir: varDir,
		status: MigrationStatus{
			FromVersion: from,
			ToVersion:   to,
			PID:         os.Getpid(),
			Phase:       MigrationPhaseBackup,
			StartedAt:   now,
			UpdatedAt:   now,
			Heartbeat:   0,
		},
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	if err := r.write(); err != nil {
		// Best-effort: if we can't write the status file, skip reporting
		// rather than fail the boot. The CLI falls back to its fixed timeout.
		return nil
	}
	go r.loop()
	return r
}

// setPhase updates the reported migration phase (backup -> migrating).
func (r *migrationReporter) setPhase(phase string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.status.Phase = phase
	r.mu.Unlock()
	_ = r.tick()
}

// loop advances the heartbeat on a fixed interval until Done.
func (r *migrationReporter) loop() {
	defer close(r.done)
	ticker := time.NewTicker(reporterHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			_ = r.tick()
		}
	}
}

// tick bumps the heartbeat counter + UpdatedAt and rewrites the file.
func (r *migrationReporter) tick() error {
	r.mu.Lock()
	r.status.Heartbeat++
	r.status.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	r.mu.Unlock()
	return r.write()
}

// write atomically persists the current status to the status file.
func (r *migrationReporter) write() error {
	r.mu.Lock()
	data, err := json.Marshal(r.status)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	path := MigrationStatusPath(r.varDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Done stops the heartbeat goroutine and removes the status file. Safe to call
// on a nil reporter (no-op) so callers can `defer r.Done()` unconditionally.
func (r *migrationReporter) Done() {
	if r == nil {
		return
	}
	close(r.stop)
	<-r.done
	_ = os.Remove(MigrationStatusPath(r.varDir))
}
