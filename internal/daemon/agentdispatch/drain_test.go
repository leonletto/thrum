package agentdispatch

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- InflightTracker invariants ---

func TestInflightTracker_BeginEndCount(t *testing.T) {
	tr := NewInflightTracker()

	// Empty tracker — Count returns 0 for any agent.
	if c := tr.Count("docs_bot", listFilesMethods); c != 0 {
		t.Errorf("empty tracker Count = %d; want 0", c)
	}

	// Begin two RPCs against same agent.
	tr.Begin("docs_bot", "agent.listFiles")
	tr.Begin("docs_bot", "agent.getFile")
	if c := tr.Count("docs_bot", listFilesMethods); c != 2 {
		t.Errorf("after 2 Begin, Count = %d; want 2", c)
	}

	// End one — count drops to 1.
	tr.End("docs_bot", "agent.listFiles")
	if c := tr.Count("docs_bot", listFilesMethods); c != 1 {
		t.Errorf("after 1 End, Count = %d; want 1", c)
	}

	// End the other — count back to 0.
	tr.End("docs_bot", "agent.getFile")
	if c := tr.Count("docs_bot", listFilesMethods); c != 0 {
		t.Errorf("after both End, Count = %d; want 0", c)
	}

	// End past zero is clamped, not negative.
	tr.End("docs_bot", "agent.listFiles")
	if c := tr.Count("docs_bot", listFilesMethods); c != 0 {
		t.Errorf("End-past-zero Count = %d; want 0 (clamped)", c)
	}
}

// TestInflightTracker_AgentIsolation pins that Begin against one
// agent doesn't bleed into another agent's count.
func TestInflightTracker_AgentIsolation(t *testing.T) {
	tr := NewInflightTracker()
	tr.Begin("docs_bot", "agent.listFiles")
	tr.Begin("docs_bot", "agent.listFiles")
	tr.Begin("ops_bot", "agent.listFiles")

	if c := tr.Count("docs_bot", listFilesMethods); c != 2 {
		t.Errorf("docs_bot Count = %d; want 2", c)
	}
	if c := tr.Count("ops_bot", listFilesMethods); c != 1 {
		t.Errorf("ops_bot Count = %d; want 1", c)
	}
	if c := tr.Count("unknown_agent", listFilesMethods); c != 0 {
		t.Errorf("unknown agent Count = %d; want 0", c)
	}
}

// TestInflightTracker_MethodFiltering verifies Count only sums the
// requested method list. Begin against a method NOT in the watched
// set doesn't contribute to drain-decision count.
func TestInflightTracker_MethodFiltering(t *testing.T) {
	tr := NewInflightTracker()
	tr.Begin("docs_bot", "agent.listFiles")
	tr.Begin("docs_bot", "agent.unrelated")

	// listFilesMethods asks only for listFiles + getFile.
	if c := tr.Count("docs_bot", listFilesMethods); c != 1 {
		t.Errorf("listFilesMethods Count = %d; want 1 (unrelated method ignored)", c)
	}
	// Asking for the unrelated method directly returns 1.
	if c := tr.Count("docs_bot", []string{"agent.unrelated"}); c != 1 {
		t.Errorf("agent.unrelated Count = %d; want 1", c)
	}
}

// TestInflightTracker_SkipDrainShortCircuits pins the feature-detect
// path: when SetSkipDrain(true) is set, Count always returns 0
// regardless of Begin/End state. (Task 63 AC.)
func TestInflightTracker_SkipDrainShortCircuits(t *testing.T) {
	tr := NewInflightTracker()
	tr.SetSkipDrain(true)
	tr.Begin("docs_bot", "agent.listFiles")
	tr.Begin("docs_bot", "agent.getFile")

	if c := tr.Count("docs_bot", listFilesMethods); c != 0 {
		t.Errorf("skip-drain Count = %d; want 0 (RPC not implemented short-circuit)", c)
	}

	// Toggle off → original counts surface.
	tr.SetSkipDrain(false)
	if c := tr.Count("docs_bot", listFilesMethods); c != 2 {
		t.Errorf("after SetSkipDrain(false) Count = %d; want 2 (counts preserved)", c)
	}
}

// TestInflightTracker_ConcurrentBeginEnd exercises the mutex under
// race-detector load. Spawns 50 goroutines each calling Begin then
// End — final Count must be zero.
func TestInflightTracker_ConcurrentBeginEnd(t *testing.T) {
	tr := NewInflightTracker()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Begin("docs_bot", "agent.listFiles")
			tr.End("docs_bot", "agent.listFiles")
		}()
	}
	wg.Wait()
	if c := tr.Count("docs_bot", listFilesMethods); c != 0 {
		t.Errorf("after 50 paired Begin/End, Count = %d; want 0", c)
	}
}

// --- DrainListFilesRPCs ---

// TestDrainListFilesRPCs_WaitsForInflight pins the plan's headline
// scenario: two in-flight calls settle within the grace window;
// drain returns true. (Plan Step 1 fixture.)
func TestDrainListFilesRPCs_WaitsForInflight(t *testing.T) {
	tracker := NewInflightTracker()
	tracker.Begin("docs_bot", "agent.listFiles")
	tracker.Begin("docs_bot", "agent.getFile")

	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		tracker.End("docs_bot", "agent.listFiles")
		tracker.End("docs_bot", "agent.getFile")
		close(done)
	}()

	drained := DrainListFilesRPCs(tracker, "docs_bot", 2*time.Second)
	if !drained {
		t.Error("expected drain to succeed within grace")
	}
	<-done
}

// TestDrainListFilesRPCs_GraceExceeded_LogsAndProceeds pins the
// timeout path: an RPC that never ends causes the grace window to
// expire; drain returns false. (Plan Step 1 fixture + slog.Warn
// assertion.)
func TestDrainListFilesRPCs_GraceExceeded_LogsAndProceeds(t *testing.T) {
	// Capture slog output to verify the warn line fires.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	tracker := NewInflightTracker()
	tracker.Begin("docs_bot", "agent.listFiles") // never ends

	drained := DrainListFilesRPCs(tracker, "docs_bot", 100*time.Millisecond)
	if drained {
		t.Error("expected drain to fail (grace exceeded)")
	}

	logOut := buf.String()
	if !strings.Contains(logOut, "teardown drain") {
		t.Errorf("expected slog.Warn 'teardown drain' line; got %q", logOut)
	}
	if !strings.Contains(logOut, "docs_bot") {
		t.Errorf("expected agent name in log line; got %q", logOut)
	}
}

// TestDrainListFilesRPCs_SkipDrainReturnsImmediately pins Task 63's
// short-circuit: a tracker in skip-drain mode returns from drain
// instantly even if Begin was called. The grace window is
// effectively unused. 200ms guard chosen to survive race-detector
// + CI load while still rejecting accidental sleeps.
func TestDrainListFilesRPCs_SkipDrainReturnsImmediately(t *testing.T) {
	tracker := NewInflightTracker()
	tracker.SetSkipDrain(true)
	tracker.Begin("docs_bot", "agent.listFiles") // would normally block

	start := time.Now()
	drained := DrainListFilesRPCs(tracker, "docs_bot", 2*time.Second)
	elapsed := time.Since(start)

	if !drained {
		t.Error("skip-drain tracker should return true immediately")
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("skip-drain elapsed = %v; expected near-zero (no polling)", elapsed)
	}
}

// TestDrainListFilesRPCs_EmptyTrackerReturnsImmediately pins the
// no-traffic happy path: a tracker that's never seen Begin returns
// true on first Count check. The 200ms guard is loose enough to
// survive race-detector + CI load while still rejecting any code
// path that accidentally sleeps for the grace window.
func TestDrainListFilesRPCs_EmptyTrackerReturnsImmediately(t *testing.T) {
	tracker := NewInflightTracker()

	start := time.Now()
	drained := DrainListFilesRPCs(tracker, "docs_bot", 2*time.Second)
	elapsed := time.Since(start)

	if !drained {
		t.Error("empty tracker should drain immediately")
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("empty-tracker elapsed = %v; expected near-zero (no polling)", elapsed)
	}
}

// --- Drainer (RPCDrainer adapter) ---

// TestDrainer_SatisfiesInterface is a compile-time pin mirroring
// the var _ in drain.go.
func TestDrainer_SatisfiesInterface(t *testing.T) {
	var _ RPCDrainer = (*Drainer)(nil)
}

// TestDrainer_DrainListFiles_HappyPath verifies the Drainer wraps
// DrainListFilesRPCs and returns nil when drain succeeds.
func TestDrainer_DrainListFiles_HappyPath(t *testing.T) {
	tracker := NewInflightTracker() // empty
	drainer := NewDrainer(tracker)

	err := drainer.DrainListFiles(context.Background(), "docs_bot", 1*time.Second)
	if err != nil {
		t.Errorf("DrainListFiles on empty tracker = %v; want nil", err)
	}
}

// TestDrainer_DrainListFiles_GraceTimeoutReturnsError pins the
// error contract: timeout flips the bool into a wrapped error so
// callers that want to log have a message; teardownGracefully
// discards it per best-effort cleanup.
func TestDrainer_DrainListFiles_GraceTimeoutReturnsError(t *testing.T) {
	// Silence the slog.Warn from DrainListFilesRPCs.
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	defer slog.SetDefault(prev)

	tracker := NewInflightTracker()
	tracker.Begin("docs_bot", "agent.listFiles") // never ends
	drainer := NewDrainer(tracker)

	err := drainer.DrainListFiles(context.Background(), "docs_bot", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error on grace timeout")
	}
	if !strings.Contains(err.Error(), "drain timeout") {
		t.Errorf("error should mention drain timeout; got %v", err)
	}
	if !strings.Contains(err.Error(), "docs_bot") {
		t.Errorf("error should mention target; got %v", err)
	}
}

// TestDrainer_NilReceiverSafe pins the defensive guard: a Drainer
// constructed without a tracker (or a literal nil) returns nil from
// DrainListFiles rather than panicking. Belt-and-suspenders over
// the nil-Drainer guard in scheduled_agent.go.
func TestDrainer_NilReceiverSafe(t *testing.T) {
	var d *Drainer
	if err := d.DrainListFiles(context.Background(), "docs_bot", time.Second); err != nil {
		t.Errorf("nil receiver DrainListFiles = %v; want nil", err)
	}

	d2 := NewDrainer(nil)
	if err := d2.DrainListFiles(context.Background(), "docs_bot", time.Second); err != nil {
		t.Errorf("nil-tracker DrainListFiles = %v; want nil", err)
	}
}
