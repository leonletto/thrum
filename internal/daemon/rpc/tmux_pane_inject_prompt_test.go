package rpc

import (
	"context"
	"testing"
	"time"
)

// TestPaneInjectPrompt_PreservesGap pins B-B1 E6.5 Task 42b Step 0:
// PaneInjectPrompt must preserve the canonical 200ms text→Enter gap
// (paneInputSubmitGap) by routing through sendKeysAndSubmit rather
// than copy-pasting the gap logic into the adapter. The 200ms gap is
// the load-bearing invariant from thrum-84xc + the
// feedback_byte_equality_pane_detection memory; splitting it across
// packages would defeat the single-source-of-truth guarantee.
//
// The test swaps the package-private sendKeysFn / sleepFn /
// sendSpecialKeyFn injection points to capture the sequence + the
// duration the function actually slept for. Asserts:
//
//  1. SendKeys fires with the prompt text.
//  2. Sleep is called with paneInputSubmitGap (200ms).
//  3. Enter fires AFTER the sleep.
//  4. The sequence is send → sleep → enter (not send → enter → sleep).
func TestPaneInjectPrompt_PreservesGap(t *testing.T) {
	prevSend := sendKeysFn
	prevSleep := sleepFn
	prevEnter := sendSpecialKeyFn
	t.Cleanup(func() {
		sendKeysFn = prevSend
		sleepFn = prevSleep
		sendSpecialKeyFn = prevEnter
	})

	type step struct {
		kind  string
		text  string
		sleep time.Duration
	}
	var seq []step

	sendKeysFn = func(_, text string) error {
		seq = append(seq, step{kind: "send", text: text})
		return nil
	}
	sleepFn = func(d time.Duration) {
		seq = append(seq, step{kind: "sleep", sleep: d})
	}
	sendSpecialKeyFn = func(_, key string) error {
		seq = append(seq, step{kind: "enter", text: key})
		return nil
	}

	h := &TmuxHandler{}
	if err := h.PaneInjectPrompt(context.Background(), "docs_bot", "hello agent"); err != nil {
		t.Fatalf("PaneInjectPrompt: %v", err)
	}

	// Sequence must be send → sleep → enter, exactly three steps.
	if len(seq) != 3 {
		t.Fatalf("expected 3 steps (send, sleep, enter); got %d: %+v", len(seq), seq)
	}
	if seq[0].kind != "send" || seq[0].text != "hello agent" {
		t.Errorf("step[0] = %+v; want send 'hello agent'", seq[0])
	}
	if seq[1].kind != "sleep" {
		t.Errorf("step[1] = %+v; want sleep", seq[1])
	}
	if seq[1].sleep != paneInputSubmitGap {
		t.Errorf("sleep duration = %v; want paneInputSubmitGap (%v) — gap must round-trip through PaneInjectPrompt",
			seq[1].sleep, paneInputSubmitGap)
	}
	if seq[2].kind != "enter" || seq[2].text != "Enter" {
		t.Errorf("step[2] = %+v; want enter 'Enter'", seq[2])
	}
}

// TestPaneInjectPrompt_PropagatesSendKeysError pins error propagation:
// if the underlying send-keys fails, PaneInjectPrompt returns the
// error verbatim so the adapter caller can decide what to do (E6.4's
// idle-nudge loop logs + continues; future callers may abort the
// stage).
func TestPaneInjectPrompt_PropagatesSendKeysError(t *testing.T) {
	prevSend := sendKeysFn
	prevSleep := sleepFn
	prevEnter := sendSpecialKeyFn
	t.Cleanup(func() {
		sendKeysFn = prevSend
		sleepFn = prevSleep
		sendSpecialKeyFn = prevEnter
	})

	sendErr := errSentinelForTest
	sendKeysFn = func(_, _ string) error { return sendErr }
	sleepFn = func(time.Duration) { t.Error("sleep should NOT fire when send-keys fails") }
	sendSpecialKeyFn = func(_, _ string) error {
		t.Error("Enter should NOT fire when send-keys fails")
		return nil
	}

	h := &TmuxHandler{}
	err := h.PaneInjectPrompt(context.Background(), "docs_bot", "hello")
	if err != sendErr {
		t.Errorf("PaneInjectPrompt err = %v; want sendErr verbatim", err)
	}
}

var errSentinelForTest = newTestErr("send-keys failure")

type testErr string

func (e testErr) Error() string { return string(e) }
func newTestErr(s string) error { return testErr(s) }
