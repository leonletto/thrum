package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRunRegistry_RegisterAndLookup(t *testing.T) {
	r := newRunRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := r.register("run-1", cancel)
	if sig == nil {
		t.Fatal("register returned nil signal channel")
	}

	sig2, ok := r.signal("run-1")
	if !ok {
		t.Fatal("signal: not found")
	}
	if sig2 != sig {
		t.Error("signal channel mismatch")
	}
	if !r.cancel("run-1") {
		t.Error("cancel: not found")
	}
	<-ctx.Done() // proves cancel was wired
}

func TestRunRegistry_DeregisterCleansBoth(t *testing.T) {
	r := newRunRegistry()
	_, cancel := context.WithCancel(context.Background())
	r.register("run-1", cancel)
	r.deregister("run-1")
	if _, ok := r.signal("run-1"); ok {
		t.Error("signal still present after deregister")
	}
	if r.cancel("run-1") {
		t.Error("cancel returned true after deregister")
	}
}

func TestRunRegistry_DeliverCompletion(t *testing.T) {
	r := newRunRegistry()
	_, cancel := context.WithCancel(context.Background())
	sig := r.register("run-1", cancel)

	err := r.deliverCompletion("run-1", &Completion{Reason: "agent reported done", Summary: "ok"})
	if err != nil {
		t.Errorf("deliverCompletion: %v", err)
	}

	select {
	case c := <-sig:
		if c.Reason != "agent reported done" {
			t.Errorf("reason = %q", c.Reason)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("completion not received")
	}
}

func TestRunRegistry_DeliverCompletion_Unknown(t *testing.T) {
	r := newRunRegistry()
	err := r.deliverCompletion("unknown-run", &Completion{})
	if !errors.Is(err, ErrUnknownRun) {
		t.Errorf("err = %v; want ErrUnknownRun", err)
	}
}

// TestRunRegistry_DeliverCompletion_Duplicate pins canonical §6.1 Alt-A:
// the signal channel is cap=1 so a second deliver returns
// ErrCompletionAlreadyDelivered rather than blocking or silently dropping.
func TestRunRegistry_DeliverCompletion_Duplicate(t *testing.T) {
	r := newRunRegistry()
	_, cancel := context.WithCancel(context.Background())
	r.register("run-1", cancel)
	if err := r.deliverCompletion("run-1", &Completion{}); err != nil {
		t.Fatalf("first deliver: %v", err)
	}
	err := r.deliverCompletion("run-1", &Completion{})
	if !errors.Is(err, ErrCompletionAlreadyDelivered) {
		t.Errorf("err = %v; want ErrCompletionAlreadyDelivered", err)
	}
}

// TestRunRegistry_ConcurrentAccess exercises register/signal/deliver/
// deregister across 50 goroutines under -race. SQLite is not in play here —
// this is pure Go-layer concurrency.
func TestRunRegistry_ConcurrentAccess(t *testing.T) {
	r := newRunRegistry()
	const goroutines = 50
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runID := fmt.Sprintf("run-%d", i)
			_, cancel := context.WithCancel(context.Background())
			r.register(runID, cancel)
			r.signal(runID)
			_ = r.deliverCompletion(runID, &Completion{})
			r.deregister(runID)
		}(i)
	}
	wg.Wait()
}
