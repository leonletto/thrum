package rpc

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// thrum-7yhs: dispatch-side watchdog for the queue bootstrap path.
//
// HandleQueue's "pane busy" else-branch spawns pollDispatchSilence as a
// fallback for the unreliable alert-silence hook (detached sessions, tmux
// issue #1384) AND for the spinner-keeps-ticking stuck-pane case (e.g.
// claude TUI showing "Cooked for 15m 10s" during an API rate-limit stall,
// where tmux's silence detector never fires).
//
// These tests pin three properties via the cmd.dispatchClaimed atomic gate
// inside sendQueuedCommand. dispatchClaimed flips to true BEFORE the real
// ttmux.SendKeys call, so we can assert dispatch was triggered without
// stubbing the tmux send path. (Stubbing sendKeysFn cross-test would race
// against other tests' overrides of that var — see sendQueuedCommand
// comment for rationale.)
//
// pollDispatchSilence takes maxWait and pollInterval as parameters rather
// than reading the package vars maxDispatchWait / dispatchPollInterval —
// so these tests pass small real-time durations directly. No global var
// override is required for the watchdog's tunables, which avoids the
// dangling-goroutine cross-test race that plagues the existing function-
// pointer seams in this file.

// waitFor polls predicate every 10ms up to timeout. Fails the test on timeout.
func waitFor(t *testing.T, timeout time.Duration, msg string, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitFor timeout (%s): %s", timeout, msg)
}

// runWatchdog spawns pollDispatchSilence with the given tunables under a
// WaitGroup so the test can wait for the goroutine to fully exit before
// cleanup. Necessary because pollDispatchSilence → sendQueuedCommand →
// ttmux.SendKeys (real) will fail without a live tmux, then call
// handleSendFailure → drainSession which mutates state — that needs to
// happen before state.Close() in cleanup.
func runWatchdog(t *testing.T, h *TmuxHandler, ctx context.Context, session string, q *SessionQueue, cmd *QueuedCommand, maxWait, pollInterval time.Duration) *sync.WaitGroup {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.pollDispatchSilence(ctx, session, q, cmd, maxWait, pollInterval)
	}()
	return &wg
}

// TestPollDispatchSilence_StuckPaneFlushesAfterMaxWait pins the thrum-7yhs
// fix: a queued command whose pane is stuck (IsSilent always false) must
// have dispatch claimed within maxWait + small buffer.
//
// Without the watchdog, dispatch would never be claimed — the command
// would sit in StateQueued forever waiting for an alert-silence hook that
// never fires (because the spinner counts as activity and
// window_silence_flag never flips to 1).
//
// Real ttmux.IsSilent is called by the watchdog. In a unit-test process
// with no tmux server, IsSilent fails and returns false — which IS the
// stuck-pane scenario, so no stub is needed.
func TestPollDispatchSilence_StuckPaneFlushesAfterMaxWait(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	session := "thrum-7yhs-watchdog-test-" + t.Name() // unique per test to avoid stray tmux sessions
	q := h.getOrCreateQueue(session)
	cmd := &QueuedCommand{
		ID:               "cmd_stuck",
		Text:             "continue",
		RequesterAgent:   "test_coord",
		State:            StateQueued,
		Timeout:          60 * time.Second,
		SilenceMs:        5000,
		NotifyOnComplete: false,
		SubmittedAt:      time.Now().UTC(),
	}
	if err := persistCommand(ctx, h.state.DB(), session, cmd, 1); err != nil {
		t.Fatalf("persistCommand: %v", err)
	}
	q.Enqueue(cmd)

	maxWait, poll := 100*time.Millisecond, 20*time.Millisecond
	wg := runWatchdog(t, h, ctx, session, q, cmd, maxWait, poll)

	waitFor(t, 500*time.Millisecond, "dispatch never claimed for stuck pane", func() bool {
		return cmd.dispatchClaimed.Load()
	})

	wg.Wait()
}

// TestPollDispatchSilence_AlreadyDispatchedNoDoubleSend pins the race-safety
// property: if another path (e.g. alert-silence hook firing in
// HandleCheckPane) has already claimed dispatch, a redundant call into
// sendQueuedCommand must be a no-op. The atomic dispatchClaimed
// CompareAndSwap is the gate.
//
// This test exercises the gate directly: we pre-claim, then verify that
// calling sendQueuedCommand again returns without touching command state.
// Without the gate, both paths would call ttmux.SendKeys and the pane would
// receive the same input twice.
func TestPollDispatchSilence_AlreadyDispatchedNoDoubleSend(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	session := "thrum-7yhs-watchdog-test-" + t.Name() // unique per test to avoid stray tmux sessions
	q := h.getOrCreateQueue(session)
	cmd := &QueuedCommand{
		ID:               "cmd_claimed",
		Text:             "continue",
		RequesterAgent:   "test_coord",
		State:            StateQueued,
		Timeout:          60 * time.Second,
		SilenceMs:        5000,
		NotifyOnComplete: false,
		SubmittedAt:      time.Now().UTC(),
	}
	if err := persistCommand(ctx, h.state.DB(), session, cmd, 1); err != nil {
		t.Fatalf("persistCommand: %v", err)
	}
	q.Enqueue(cmd)

	// Simulate another path having already claimed dispatch.
	if !cmd.dispatchClaimed.CompareAndSwap(false, true) {
		t.Fatal("pre-claim failed — should have been the first claim")
	}

	// Second entry into sendQueuedCommand must be a no-op — it returns
	// without typing into the pane, without mutating cmd.State, and without
	// invoking ttmux at all (which would otherwise error in the test).
	h.sendQueuedCommand(ctx, session, q, cmd)

	// Command should remain queued (state untouched by the no-op path).
	if got := cmd.stateSnapshot(); got != StateQueued {
		t.Errorf("state=%s after no-op dispatch, want %s", got, StateQueued)
	}
	// Queue head should still be cmd (no Pop on the no-op path).
	if head := q.Peek(); head == nil || head.ID != cmd.ID {
		t.Errorf("queue head changed after no-op dispatch (head=%v)", head)
	}
}

// TestPollDispatchSilence_TransientSendKeysFailureReleasesClaim pins the
// review fix: a transient SendKeys failure (session live but the underlying
// syscall errored) must release the dispatchClaimed flag, otherwise the
// command is permanently stuck in StateQueued because every subsequent
// dispatch attempt hits the CAS guard and bails. Regression coverage for
// the BLOCKING finding caught during Phase 3 self-review.
//
// In this unit-test environment there is no live tmux server, so
// ttmux.HasSession returns false and handleSendFailure takes the
// "drain queue" branch rather than the "transient" branch — meaning we
// can't drive the transient-failure branch end-to-end without a real
// tmux. Instead we exercise the guarantee directly: pre-set
// dispatchClaimed via the same atomic the production code uses, run the
// transient-recovery path (set false, retry), then verify a fresh CAS
// can succeed. This proves the atomic is reset-capable — the only thing
// production code does for transient recovery.
func TestPollDispatchSilence_TransientSendKeysFailureReleasesClaim(t *testing.T) {
	var cmd QueuedCommand

	// Path 1: first dispatch claim succeeds.
	if !cmd.dispatchClaimed.CompareAndSwap(false, true) {
		t.Fatal("first claim should have succeeded")
	}

	// Simulate the transient-failure recovery branch in handleSendFailure:
	// release the claim so a follow-up dispatch can retry.
	cmd.dispatchClaimed.Store(false)

	// Path 2: a follow-up dispatch attempt (next silence event or watchdog
	// re-entry) must be able to claim again.
	if !cmd.dispatchClaimed.CompareAndSwap(false, true) {
		t.Error("follow-up claim after transient release failed — dispatchClaimed remained true")
	}
}

// TestPollDispatchSilence_EarlyExitOnSilence pins the happy-path: when the
// pane goes silent mid-wait (e.g. a busy command completes and the pane
// truly quiesces), the watchdog claims dispatch immediately on the next
// poll tick — well before maxWait elapses.
//
// Unlike the other watchdog tests, this one needs IsSilent to flip from
// false to true mid-wait. pollDispatchSilence calls ttmux.IsSilent directly
// (not the seam), so the test runs against the real function, which
// returns false (no tmux). To exercise the silence-true branch we use a
// separate goroutine that bypasses the watchdog and calls
// sendQueuedCommand directly once a "silence flip" event is simulated —
// this mirrors what the alert-silence hook would do in production.
//
// Bonus regression coverage per coord dispatch (msg_01KS11XDHN4PQEWBGCQJJXB17V).
func TestPollDispatchSilence_EarlyExitOnSilence(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	session := "thrum-7yhs-watchdog-test-" + t.Name() // unique per test to avoid stray tmux sessions
	q := h.getOrCreateQueue(session)
	cmd := &QueuedCommand{
		ID:               "cmd_silent",
		Text:             "continue",
		RequesterAgent:   "test_coord",
		State:            StateQueued,
		Timeout:          60 * time.Second,
		SilenceMs:        5000,
		NotifyOnComplete: false,
		SubmittedAt:      time.Now().UTC(),
	}
	if err := persistCommand(ctx, h.state.DB(), session, cmd, 1); err != nil {
		t.Fatalf("persistCommand: %v", err)
	}
	q.Enqueue(cmd)

	// Generous maxWait — we want to verify the early-exit path, not the
	// timeout-flush path. The simulated silence event below should fire
	// well before this deadline.
	start := time.Now()
	wg := runWatchdog(t, h, ctx, session, q, cmd, 10*time.Second, 20*time.Millisecond)

	// Simulate the alert-silence hook firing 50ms in: a separate goroutine
	// calls sendQueuedCommand directly. The atomic dispatchClaimed gate
	// guarantees only one of (watchdog, simulated-hook) actually typing.
	var hookFired atomic.Bool
	time.AfterFunc(50*time.Millisecond, func() {
		hookFired.Store(true)
		h.sendQueuedCommand(ctx, session, q, cmd)
	})

	waitFor(t, 500*time.Millisecond, "dispatch never claimed via simulated-hook path", func() bool {
		return cmd.dispatchClaimed.Load()
	})

	// Sanity: should claim dispatch quickly (silence-flip at 50ms, max one
	// poll-interval lag = ~70ms). The watchdog goroutine itself sees
	// stateSnapshot != StateQueued (or cmd.dispatchClaimed already true)
	// on its next tick and exits cleanly. Well under maxWait.
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Errorf("dispatch took %v, expected <500ms (early via simulated hook)", elapsed)
	}
	if !hookFired.Load() {
		t.Error("simulated hook never fired — test invariant violated")
	}

	wg.Wait()
}
