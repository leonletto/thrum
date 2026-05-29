package nudge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

// A pane showing an active AskUserQuestion-style selection dialog (the danger
// case): the "❯" cursor on a numbered option means Enter selects.
const dialogPane = "\n Pick one\n ❯ 1. Yes\n   2. No\n"

// Plain safe output — no prompt, gate, or menu.
const safePane = "Running tests...\nPASS\nok\n"

// resetDeferredState clears the package-global queue + seams and restores them
// after the test. Internal-test access (package nudge) is the whole reason this
// file isn't in package nudge_test.
func resetDeferredState(t *testing.T) {
	t.Helper()
	deferredMu.Lock()
	deferredByS = map[string]deferredNudge{}
	deferredMu.Unlock()
	origNudge := nudgeFn
	origCapture := capturePaneFn
	origHasSession := hasSessionFn
	t.Cleanup(func() {
		nudgeFn = origNudge
		capturePaneFn = origCapture
		hasSessionFn = origHasSession
		deferredMu.Lock()
		deferredByS = map[string]deferredNudge{}
		deferredMu.Unlock()
	})
}

// nudgeRecorder is a thread-safe record of nudgeFn calls.
type nudgeRecorder struct {
	mu    sync.Mutex
	calls [][2]string // {target, sender}
}

func (r *nudgeRecorder) fn(target, sender string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [2]string{target, sender})
	return nil
}
func (r *nudgeRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}
func (r *nudgeRecorder) last() [2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return [2]string{}
	}
	return r.calls[len(r.calls)-1]
}

func TestRedeliverIfSafe_DeliversWhenPaneClears(t *testing.T) {
	resetDeferredState(t)
	rec := &nudgeRecorder{}
	nudgeFn = rec.fn

	DeferNudge("sess", "sess:0.0", "@alice")
	if !HasDeferred("sess") {
		t.Fatal("HasDeferred = false after DeferNudge; want true")
	}

	// Pane still showing the dialog → must NOT deliver.
	if RedeliverIfSafe("sess", "claude", dialogPane) {
		t.Error("RedeliverIfSafe delivered into an active dialog; want deferred")
	}
	if rec.count() != 0 {
		t.Errorf("nudgeFn called %d times while dialog up; want 0", rec.count())
	}
	if !HasDeferred("sess") {
		t.Error("deferred nudge dropped while dialog still up; want retained")
	}

	// Pane cleared → must deliver exactly once and clear the queue.
	if !RedeliverIfSafe("sess", "claude", safePane) {
		t.Error("RedeliverIfSafe = false on a cleared pane; want true")
	}
	if rec.count() != 1 || rec.last() != [2]string{"sess:0.0", "@alice"} {
		t.Errorf("nudgeFn calls = %v; want one (sess:0.0, @alice)", rec.calls)
	}
	if HasDeferred("sess") {
		t.Error("deferred nudge still present after delivery; want cleared")
	}
}

func TestRedeliverIfSafe_EmptyContentNeverDelivers(t *testing.T) {
	resetDeferredState(t)
	rec := &nudgeRecorder{}
	nudgeFn = rec.fn
	DeferNudge("sess", "sess:0.0", "@bob")

	// Empty capture = unknown pane state → must not blind-deliver.
	if RedeliverIfSafe("sess", "claude", "") {
		t.Error("RedeliverIfSafe delivered on empty content; want false")
	}
	if rec.count() != 0 || !HasDeferred("sess") {
		t.Error("empty-content path delivered or dropped the nudge; want retained, no send")
	}
}

func TestRedeliverIfSafe_NoDeferredIsNoop(t *testing.T) {
	resetDeferredState(t)
	rec := &nudgeRecorder{}
	nudgeFn = rec.fn
	if RedeliverIfSafe("sess", "claude", safePane) {
		t.Error("RedeliverIfSafe = true with nothing deferred; want false")
	}
	if rec.count() != 0 {
		t.Error("nudgeFn called with nothing deferred")
	}
}

func TestRedeliverIfSafe_ReDefersOnSendError(t *testing.T) {
	resetDeferredState(t)
	var calls int
	nudgeFn = func(_, _ string) error { calls++; return errTest }
	DeferNudge("sess", "sess:0.0", "@carol")

	if RedeliverIfSafe("sess", "claude", safePane) {
		t.Error("RedeliverIfSafe reported success despite send error; want false")
	}
	if calls != 1 {
		t.Errorf("nudgeFn called %d times; want 1", calls)
	}
	if !HasDeferred("sess") {
		t.Error("nudge dropped on transient send error; want re-deferred for retry")
	}
}

func TestRedeliverIfSafe_FailureDoesNotClobberNewerArrival(t *testing.T) {
	// thrum-7phu IMPORTANT race guard: while RedeliverIfSafe is sending (after
	// takeDeferred removed the entry), a concurrent DispatchTmux may defer a
	// NEWER poke for the same session. If that send then fails, the failure
	// re-defer must NOT overwrite the newer sender.
	resetDeferredState(t)
	DeferNudge("sess", "sess:0.0", "@old")
	// nudgeFn simulates the race: a newer arrival lands mid-send, then the send fails.
	nudgeFn = func(_, _ string) error {
		DeferNudge("sess", "sess:0.0", "@newer")
		return errTest
	}
	if RedeliverIfSafe("sess", "claude", safePane) {
		t.Error("RedeliverIfSafe reported success despite send error; want false")
	}
	// The newer arrival must survive — verify by delivering on a clean pane.
	rec := &nudgeRecorder{}
	nudgeFn = rec.fn
	if !RedeliverIfSafe("sess", "claude", safePane) {
		t.Fatal("second RedeliverIfSafe = false; want true (newer arrival should deliver)")
	}
	if got := rec.last()[1]; got != "@newer" {
		t.Errorf("delivered sender = %q; want @newer (failure re-defer clobbered the newer arrival)", got)
	}
}

func TestDeferNudge_CollapsesPerSession(t *testing.T) {
	resetDeferredState(t)
	rec := &nudgeRecorder{}
	nudgeFn = rec.fn
	DeferNudge("sess", "sess:0.0", "@first")
	DeferNudge("sess", "sess:0.0", "@second") // newer arrival folds in
	RedeliverIfSafe("sess", "claude", safePane)
	if rec.count() != 1 {
		t.Fatalf("delivered %d nudges; want 1 (collapsed per session)", rec.count())
	}
	if got := rec.last()[1]; got != "@second" {
		t.Errorf("collapsed sender = %q; want @second (latest wins)", got)
	}
}

// TestDispatchTmux_DefersWhenDialogUp / _DeliversWhenSafe exercise the
// end-to-end seam: identity-file resolution → pane capture → safe-to-type
// decision → defer or nudge. DispatchTmux is fire-and-forget (goroutine per
// recipient), so we poll for the observable outcome with a bounded timeout.
func TestDispatchTmux_DefersWhenDialogUp(t *testing.T) {
	resetDeferredState(t)
	thrumDir := writeIdentityFixture(t, "agt", "agt:0.0", "claude")
	rec := &nudgeRecorder{}
	nudgeFn = rec.fn
	hasSessionFn = func(string) bool { return true }
	capturePaneFn = func(string, int) (string, error) { return dialogPane, nil }

	DispatchTmux(thrumDir, []string{"agt"}, "@sender")

	waitFor(t, func() bool { return HasDeferred("agt") })
	if rec.count() != 0 {
		t.Errorf("nudgeFn fired %d times into a dialog; want 0 (deferred)", rec.count())
	}
}

func TestDispatchTmux_DeliversWhenSafe(t *testing.T) {
	resetDeferredState(t)
	thrumDir := writeIdentityFixture(t, "agt", "agt:0.0", "claude")
	rec := &nudgeRecorder{}
	nudgeFn = rec.fn
	hasSessionFn = func(string) bool { return true }
	capturePaneFn = func(string, int) (string, error) { return safePane, nil }

	DispatchTmux(thrumDir, []string{"agt"}, "@sender")

	waitFor(t, func() bool { return rec.count() == 1 })
	if HasDeferred("agt") {
		t.Error("nudge deferred on a safe pane; want delivered")
	}
	if rec.last() != [2]string{"agt:0.0", "@sender"} {
		t.Errorf("delivered nudge = %v; want (agt:0.0, @sender)", rec.last())
	}
}

// writeIdentityFixture writes <thrumDir>/identities/<name>.json with the given
// tmux session + runtime and returns thrumDir.
func writeIdentityFixture(t *testing.T, name, tmuxSession, runtime string) string {
	t.Helper()
	thrumDir := t.TempDir()
	idDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}
	data, err := json.Marshal(config.IdentityFile{TmuxSession: tmuxSession, Runtime: runtime})
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(idDir, name+".json"), data, 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	return thrumDir
}

// waitFor polls cond up to 2s (DispatchTmux runs its work in a goroutine).
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

// errTest is a sentinel for the send-error re-defer test.
var errTest = errTestType("send failed")

type errTestType string

func (e errTestType) Error() string { return string(e) }
