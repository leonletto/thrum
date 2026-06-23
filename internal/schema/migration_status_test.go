package schema

import (
	"os"
	"testing"
	"time"
)

func TestReadMigrationStatus_AbsentReturnsNil(t *testing.T) {
	dir := t.TempDir()
	st, err := ReadMigrationStatus(dir)
	if err != nil {
		t.Fatalf("ReadMigrationStatus on absent file: unexpected error %v", err)
	}
	if st != nil {
		t.Fatalf("ReadMigrationStatus on absent file: expected nil, got %+v", st)
	}
}

// ClearStaleMigrationStatus is what NewState calls at boot to drop a status
// file orphaned by a crashed prior migration (the crash-recovery edge). This
// exercises that exact cleanup code path.
func TestClearStaleMigrationStatus_RemovesFileAndTmp(t *testing.T) {
	dir := t.TempDir()
	path := MigrationStatusPath(dir)
	if err := os.WriteFile(path, []byte(`{"from_version":24,"to_version":41}`), 0600); err != nil {
		t.Fatalf("seed stale status: %v", err)
	}
	if err := os.WriteFile(path+".tmp", []byte("partial-write-leftover"), 0600); err != nil {
		t.Fatalf("seed stale tmp sidecar: %v", err)
	}

	ClearStaleMigrationStatus(dir)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale status file not removed: stat err = %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("stale tmp sidecar not removed: stat err = %v", err)
	}

	// Safe / idempotent on an already-clean dir (must not panic or error).
	ClearStaleMigrationStatus(dir)
	if st, err := ReadMigrationStatus(dir); err != nil || st != nil {
		t.Fatalf("after clear, expected no status; got st=%+v err=%v", st, err)
	}
}

func TestMigrationReporter_WritesHeartbeatsAndRemoves(t *testing.T) {
	dir := t.TempDir()

	r := startMigrationReporter(dir, 24, 41)
	if r == nil {
		t.Fatal("startMigrationReporter returned nil")
	}

	// Status file appears with the right from/to + phase.
	st, err := ReadMigrationStatus(dir)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if st == nil {
		t.Fatal("expected migration status file to exist after arming reporter")
	}
	if st.FromVersion != 24 || st.ToVersion != 41 {
		t.Fatalf("from/to = %d->%d, want 24->41", st.FromVersion, st.ToVersion)
	}
	if st.Phase != MigrationPhaseBackup {
		t.Fatalf("initial phase = %q, want %q", st.Phase, MigrationPhaseBackup)
	}
	if st.PID != os.Getpid() {
		t.Fatalf("pid = %d, want %d", st.PID, os.Getpid())
	}
	firstBeat := st.Heartbeat

	// Phase change is reflected.
	r.setPhase(MigrationPhaseMigrating)

	// Heartbeat advances over time.
	var advanced bool
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(reporterHeartbeatInterval)
		cur, err := ReadMigrationStatus(dir)
		if err != nil || cur == nil {
			continue
		}
		if cur.Heartbeat > firstBeat && cur.Phase == MigrationPhaseMigrating {
			advanced = true
			break
		}
	}
	if !advanced {
		t.Fatal("heartbeat did not advance / phase did not update within timeout")
	}

	// Done removes the file.
	r.Done()
	st, err = ReadMigrationStatus(dir)
	if err != nil {
		t.Fatalf("read status after Done: %v", err)
	}
	if st != nil {
		t.Fatalf("expected status file removed after Done, got %+v", st)
	}
}
