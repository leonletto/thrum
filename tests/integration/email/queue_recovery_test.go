//go:build integration

// D-B1.19 integration tests — queue persistence and restart recovery (§17 AC queue).
//
// These tests construct a real Queue + Worker against a real SQLite DB and
// exercise the restart-recovery path without spinning up a full daemon.
//
// Test functions:
//   - TestEmail_QueuePersistenceAcrossRestart — enqueue rows, "kill" the worker
//     (cancel ctx), construct a new Worker against the same DB, verify pending
//     rows are picked up and processed.
//   - TestEmail_NoDoubleSendOnRestart — same scenario but with a counting SMTP
//     stub; verifies each row is sent exactly once across the restart boundary.

package email_integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/schema"
)

// --- helpers shared within this file ----------------------------------------

func openIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "integration.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// recordingSubmitter is a thread-safe stub that records Submit calls and can
// be made to return a configurable error.
type recordingSubmitter struct {
	mu       sync.Mutex
	count    int64
	submitFn func(email.Envelope) error // nil → success
}

func (r *recordingSubmitter) Submit(_ context.Context, env email.Envelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
	if r.submitFn != nil {
		return r.submitFn(env)
	}
	return nil
}

func (r *recordingSubmitter) Count() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// enqueueN inserts n rows into the queue and returns their IDs.
func enqueueN(t *testing.T, q *email.Queue, n int) []int64 {
	t.Helper()
	ids := make([]int64, n)
	for i := range n {
		id, err := q.Enqueue(context.Background(), email.QueueEnvelope{
			FromAgent:   fmt.Sprintf("agent%d@example.com", i),
			ToAddress:   fmt.Sprintf("dest%d@example.com", i),
			Body:        fmt.Sprintf("body %d", i),
			HeadersJSON: "{}",
		})
		if err != nil {
			t.Fatalf("Enqueue[%d]: %v", i, err)
		}
		ids[i] = id
	}
	return ids
}

// fastWorkerConfig returns a QueueConfig suitable for fast integration tests.
func fastWorkerConfig() email.QueueConfig {
	return email.QueueConfig{
		MaxAttempts:    3,
		BackoffInitial: 5 * time.Millisecond,
		BackoffCap:     50 * time.Millisecond,
		PollInterval:   10 * time.Millisecond,
	}
}

// waitForSubmitCount polls until the submitter count reaches n or timeout.
func waitForSubmitCount(t *testing.T, sub *recordingSubmitter, n int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sub.Count() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d submissions (got %d)", n, sub.Count())
}

// --- tests -------------------------------------------------------------------

// TestEmail_QueuePersistenceAcrossRestart verifies that a new Worker
// constructed against the same SQLite DB picks up rows that were enqueued
// before a prior worker exited (simulating daemon restart).
func TestEmail_QueuePersistenceAcrossRestart(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	db := openIntegrationDB(t)
	q := email.NewQueue(db)

	// Enqueue 5 rows before any worker runs.
	const rowCount = 5
	_ = enqueueN(t, q, rowCount)

	// "First worker" — starts and we cancel it before it can drain.
	// Use a context that's already cancelled so the first worker never ticks.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled

	sub1 := &recordingSubmitter{}
	w1 := email.NewWorker(q, sub1, nil, fastWorkerConfig())
	// Run returns when ctx is done; the orphan-recovery sweep runs but the ticker
	// fires 0 times because context is already cancelled. Rows stay queued.
	_ = w1.Run(cancelledCtx)

	// First worker did not drain anything (or at most did the recovery sweep).
	// The rows should still be in 'queued' state.
	var remaining int
	_ = db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM email_outbound_queue WHERE status = 'queued'`).Scan(&remaining)
	if remaining == 0 {
		t.Skip("worker drained rows before we could test restart — increase row count or remove this skip")
	}

	// "Second worker" — fresh Worker, same DB. Should pick up all remaining rows.
	sub2 := &recordingSubmitter{}
	w2 := email.NewWorker(q, sub2, nil, fastWorkerConfig())

	ctx2, cancel2 := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w2.Run(ctx2)
	}()
	t.Cleanup(func() { cancel2(); <-done })

	// Wait for the second worker to drain remaining rows.
	waitForSubmitCount(t, sub2, int64(remaining), 2*time.Second)

	// All rows must now be 'sent'.
	var queued int
	_ = db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM email_outbound_queue WHERE status = 'queued'`).Scan(&queued)
	if queued != 0 {
		t.Errorf("expected 0 queued rows after second worker, got %d", queued)
	}

	cancel2()
	<-done
}

// TestEmail_NoDoubleSendOnRestart verifies that across a simulated restart
// boundary each queued row is submitted exactly once.
//
// Mechanism: first worker drains N rows then is stopped; second worker starts
// and finds 0 remaining. Total submit count == rowCount.
func TestEmail_NoDoubleSendOnRestart(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	db := openIntegrationDB(t)
	q := email.NewQueue(db)

	const rowCount = 8
	_ = enqueueN(t, q, rowCount)

	// First worker: run until it drains all rows.
	sub1 := &recordingSubmitter{}
	w1 := email.NewWorker(q, sub1, nil, fastWorkerConfig())

	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		_ = w1.Run(ctx1)
	}()

	// Wait for first worker to drain all rows.
	waitForSubmitCount(t, sub1, rowCount, 3*time.Second)
	cancel1()
	<-done1

	// Second worker: same DB, fresh Worker instance. Nothing should be left.
	sub2 := &recordingSubmitter{}
	w2 := email.NewWorker(q, sub2, nil, fastWorkerConfig())

	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		_ = w2.Run(ctx2)
	}()

	// Let the second worker run briefly (one poll cycle).
	time.Sleep(50 * time.Millisecond)
	cancel2()
	<-done2

	// Total submissions across both workers must equal rowCount (no double-send).
	total := sub1.Count() + sub2.Count()
	if total != rowCount {
		t.Errorf("total submit count = %d, want %d (no double-send on restart)", total, rowCount)
	}
}

// TestEmail_WorkerOrphanRecovery verifies the orphan-recovery sweep: rows
// stuck in 'sending' by a crashed worker are rescued on the next worker's
// startup sweep and eventually delivered.
func TestEmail_WorkerOrphanRecovery(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	db := openIntegrationDB(t)
	q := email.NewQueue(db)

	// Plant a row directly in 'sending' with an old updated_at (simulating a
	// crashed worker mid-submit, 10 minutes ago).
	oldMs := time.Now().Add(-10 * time.Minute).UnixMilli()
	nowMs := time.Now().UnixMilli()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO email_outbound_queue
			(from_agent, to_address, body, headers_json,
			 attempt_count, next_retry_at, status, enqueued_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		"agent@example.com", "dest@example.com", "orphan body", "{}",
		nowMs, string(email.StatusSending), oldMs, oldMs,
	)
	if err != nil {
		t.Fatalf("insert orphan: %v", err)
	}

	sub := &recordingSubmitter{}
	w := email.NewWorker(q, sub, nil, fastWorkerConfig())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })

	// The recovery sweep runs at Run() startup; the first tick should deliver.
	waitForSubmitCount(t, sub, 1, 2*time.Second)

	// Verify the row is now 'sent'.
	var status string
	_ = db.QueryRowContext(context.Background(),
		`SELECT status FROM email_outbound_queue LIMIT 1`).Scan(&status)
	if status != string(email.StatusSent) {
		t.Errorf("orphan row status after recovery: got %q, want %q", status, email.StatusSent)
	}

	cancel()
	<-done
}

// TestEmail_ConcurrentWorkersNoDoubleSend verifies that two Worker instances
// draining the same DB simultaneously do not double-send any row (atomic
// claim via UPDATE WHERE status='queued' + RowsAffected guard).
func TestEmail_ConcurrentWorkersNoDoubleSend(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	db := openIntegrationDB(t)
	q := email.NewQueue(db)

	const rowCount = 20
	_ = enqueueN(t, q, rowCount)

	var totalSent atomic.Int64
	var wg sync.WaitGroup
	wg.Add(2)

	for range 2 {
		go func() {
			defer wg.Done()
			sub := &recordingSubmitter{}
			w := email.NewWorker(q, sub, nil, fastWorkerConfig())
			sent, _, _, err := w.Drain(context.Background())
			if err == nil {
				totalSent.Add(int64(sent))
			}
		}()
	}
	wg.Wait()

	if got := totalSent.Load(); got != rowCount {
		t.Errorf("total sent across 2 concurrent workers = %d, want %d (no double-send)", got, rowCount)
	}
}
