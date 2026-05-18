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
	}
}

// --- tests -------------------------------------------------------------------

// TestEmail_QueuePersistenceAcrossRestart verifies that a new Worker
// constructed against the same SQLite DB picks up rows that were enqueued
// before a prior worker exited (simulating daemon restart).
//
// Post thrum-6qmf.8 the substrate drives drains via
// `internal.email_queue_drain`; this test exercises the per-tick
// contract (Drain) directly without the substrate involved.
func TestEmail_QueuePersistenceAcrossRestart(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	db := openIntegrationDB(t)
	q := email.NewQueue(db)

	// Enqueue 5 rows before any worker runs.
	const rowCount = 5
	_ = enqueueN(t, q, rowCount)

	// "First worker" — constructed but never gets a chance to Drain.
	sub1 := &recordingSubmitter{}
	w1 := email.NewWorker(q, sub1, nil, fastWorkerConfig())
	_ = w1
	if sub1.Count() != 0 {
		t.Fatalf("first worker submitted %d rows without Drain being called", sub1.Count())
	}

	// Rows should still be in 'queued' state.
	var remaining int
	_ = db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM email_outbound_queue WHERE status = 'queued'`).Scan(&remaining)
	if remaining != rowCount {
		t.Fatalf("expected %d queued rows pre-restart, got %d", rowCount, remaining)
	}

	// "Second worker" — fresh Worker, same DB. Should pick up all remaining rows.
	sub2 := &recordingSubmitter{}
	w2 := email.NewWorker(q, sub2, nil, fastWorkerConfig())

	if err := w2.RecoverOrphans(context.Background()); err != nil {
		t.Fatalf("RecoverOrphans: %v", err)
	}
	if _, _, _, err := w2.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	if sub2.Count() != int64(remaining) {
		t.Errorf("second worker submitted %d rows; want %d", sub2.Count(), remaining)
	}

	// All rows must now be 'sent'.
	var queued int
	_ = db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM email_outbound_queue WHERE status = 'queued'`).Scan(&queued)
	if queued != 0 {
		t.Errorf("expected 0 queued rows after second worker, got %d", queued)
	}
}

// TestEmail_NoDoubleSendOnRestart verifies that across a simulated restart
// boundary each queued row is submitted exactly once.
//
// Mechanism: first worker drains N rows; second worker starts and finds
// 0 remaining. Total submit count == rowCount.
func TestEmail_NoDoubleSendOnRestart(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	db := openIntegrationDB(t)
	q := email.NewQueue(db)

	const rowCount = 8
	_ = enqueueN(t, q, rowCount)

	// First worker: drain all rows.
	sub1 := &recordingSubmitter{}
	w1 := email.NewWorker(q, sub1, nil, fastWorkerConfig())
	if err := w1.RecoverOrphans(context.Background()); err != nil {
		t.Fatalf("first RecoverOrphans: %v", err)
	}
	if _, _, _, err := w1.Drain(context.Background()); err != nil {
		t.Fatalf("first Drain: %v", err)
	}

	// Second worker: same DB, fresh Worker instance. Nothing should be left.
	sub2 := &recordingSubmitter{}
	w2 := email.NewWorker(q, sub2, nil, fastWorkerConfig())
	if err := w2.RecoverOrphans(context.Background()); err != nil {
		t.Fatalf("second RecoverOrphans: %v", err)
	}
	if _, _, _, err := w2.Drain(context.Background()); err != nil {
		t.Fatalf("second Drain: %v", err)
	}

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

	// Post thrum-6qmf.8 the substrate drives drains; this test exercises
	// the explicit RecoverOrphans + Drain contract directly.
	if err := w.RecoverOrphans(context.Background()); err != nil {
		t.Fatalf("RecoverOrphans: %v", err)
	}
	if _, _, _, err := w.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	if sub.Count() != 1 {
		t.Errorf("expected 1 submission after recovery; got %d", sub.Count())
	}

	// Verify the row is now 'sent'.
	var status string
	_ = db.QueryRowContext(context.Background(),
		`SELECT status FROM email_outbound_queue LIMIT 1`).Scan(&status)
	if status != string(email.StatusSent) {
		t.Errorf("orphan row status after recovery: got %q, want %q", status, email.StatusSent)
	}
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
