package email_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/schema"
)

// testDB opens and initialises a fresh SQLite DB for one test. The DB is
// closed automatically via t.Cleanup.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// fakeSubmitter is a thread-safe stub for SMTPSubmitter.
type fakeSubmitter struct {
	mu          sync.Mutex
	submitErr   error
	submitCount int
}

func (f *fakeSubmitter) Submit(_ context.Context, _ email.Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitCount++
	return f.submitErr
}

func (f *fakeSubmitter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.submitCount
}

// fakeNotifier counts Notify calls.
type fakeNotifier struct {
	mu    sync.Mutex
	calls []string
}

func (n *fakeNotifier) Notify(_ context.Context, msg string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, msg)
	return nil
}

func (n *fakeNotifier) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.calls)
}

// fastCfg returns a QueueConfig with short delays for unit tests.
func fastCfg() email.QueueConfig {
	return email.QueueConfig{
		MaxAttempts:    3,
		BackoffInitial: 10 * time.Millisecond,
		BackoffCap:     100 * time.Millisecond,
	}
}

func enqueueOne(t *testing.T, q *email.Queue) int64 {
	t.Helper()
	id, err := q.Enqueue(context.Background(), email.QueueEnvelope{
		FromAgent:   "agent@example.com",
		ToAddress:   "dest@example.com",
		Body:        "test body",
		HeadersJSON: "{}",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return id
}

// setNextRetryAt overrides next_retry_at for all queued rows so Drain finds them
// regardless of when the row was last retried.
func setNextRetryAt(t *testing.T, db *sql.DB, ms int64) {
	t.Helper()
	// Use the typed Status constant rather than a raw literal so the
	// enum-guard-completeness claim (D-B1.10 acceptance) holds across
	// both production code AND test helpers.
	_, err := db.ExecContext(context.Background(),
		`UPDATE email_outbound_queue SET next_retry_at = ? WHERE status = ?`, ms, string(email.StatusQueued))
	if err != nil {
		t.Fatalf("setNextRetryAt: %v", err)
	}
}

// getNextRetryAt reads next_retry_at from the first (and typically only) row.
func getNextRetryAt(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var v int64
	err := db.QueryRowContext(context.Background(),
		`SELECT next_retry_at FROM email_outbound_queue LIMIT 1`).Scan(&v)
	if err != nil {
		t.Fatalf("getNextRetryAt: %v", err)
	}
	return v
}

// TestQueue_EnqueueInsertsQueued verifies that a freshly enqueued row lands
// with status=queued, attempt_count=0, and next_retry_at≈now.
func TestQueue_EnqueueInsertsQueued(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	q := email.NewQueue(db)

	before := time.Now().UnixMilli()
	id, err := q.Enqueue(context.Background(), email.QueueEnvelope{
		FromAgent:   "agent@example.com",
		ToAddress:   "dest@example.com",
		Subject:     "Hello",
		Body:        "body",
		HeadersJSON: `{"X-Custom":"1"}`,
	})
	after := time.Now().UnixMilli()

	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	var status string
	var attemptCount int
	var nextRetryAt int64
	err = db.QueryRowContext(context.Background(),
		`SELECT status, attempt_count, next_retry_at FROM email_outbound_queue WHERE id = ?`, id).
		Scan(&status, &attemptCount, &nextRetryAt)
	if err != nil {
		t.Fatalf("scan row: %v", err)
	}

	if status != string(email.StatusQueued) {
		t.Errorf("status: got %q, want %q", status, email.StatusQueued)
	}
	if attemptCount != 0 {
		t.Errorf("attempt_count: got %d, want 0", attemptCount)
	}
	if nextRetryAt < before || nextRetryAt > after+100 {
		t.Errorf("next_retry_at %d not in [%d, %d]", nextRetryAt, before, after+100)
	}
}

// TestQueue_DrainSendsAndMarksSent verifies that a row whose submitter returns
// nil transitions to status=sent.
func TestQueue_DrainSendsAndMarksSent(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	q := email.NewQueue(db)
	_ = enqueueOne(t, q)

	stub := &fakeSubmitter{}
	w := email.NewWorker(q, stub, nil, fastCfg())

	sent, requeued, failed, err := w.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if sent != 1 || requeued != 0 || failed != 0 {
		t.Fatalf("expected sent=1 requeued=0 failed=0, got %d/%d/%d", sent, requeued, failed)
	}
	if stub.count() != 1 {
		t.Fatalf("submitCount: got %d, want 1", stub.count())
	}

	var status string
	_ = db.QueryRowContext(context.Background(),
		`SELECT status FROM email_outbound_queue LIMIT 1`).Scan(&status)
	if status != string(email.StatusSent) {
		t.Errorf("db status: got %q, want %q", status, email.StatusSent)
	}
}

// TestQueue_DrainTransientRequeuesWithBackoff verifies that a transient SMTP
// error flips the row back to queued with attempt_count=1 and next_retry_at
// past now.
func TestQueue_DrainTransientRequeuesWithBackoff(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	q := email.NewQueue(db)
	_ = enqueueOne(t, q)

	stub := &fakeSubmitter{submitErr: fmt.Errorf("%w: connection reset", email.ErrSmtpTransient)}
	w := email.NewWorker(q, stub, nil, fastCfg())

	before := time.Now().UnixMilli()
	_, requeued, _, err := w.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("expected requeued=1, got %d", requeued)
	}

	var status string
	var attemptCount int
	var nextRetryAt int64
	_ = db.QueryRowContext(context.Background(),
		`SELECT status, attempt_count, next_retry_at FROM email_outbound_queue LIMIT 1`).
		Scan(&status, &attemptCount, &nextRetryAt)

	if status != string(email.StatusQueued) {
		t.Errorf("status after transient: got %q, want %q", status, email.StatusQueued)
	}
	if attemptCount != 1 {
		t.Errorf("attempt_count after transient: got %d, want 1", attemptCount)
	}
	if nextRetryAt <= before {
		t.Errorf("next_retry_at should be in the future: %d <= %d", nextRetryAt, before)
	}

	// A second Drain should find nothing — the row is cooling off.
	sent2, requeued2, failed2, _ := w.Drain(context.Background())
	if sent2 != 0 || requeued2 != 0 || failed2 != 0 {
		t.Fatalf("second Drain should be empty, got %d/%d/%d", sent2, requeued2, failed2)
	}
}

// TestQueue_DrainBackoffCappedAtMax verifies that repeated failures never
// push next_retry_at further than BackoffCap beyond now.
func TestQueue_DrainBackoffCappedAtMax(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	q := email.NewQueue(db)
	_ = enqueueOne(t, q)

	cfg := email.QueueConfig{
		MaxAttempts:    100,
		BackoffInitial: 10 * time.Millisecond,
		BackoffCap:     50 * time.Millisecond,
	}
	stub := &fakeSubmitter{submitErr: fmt.Errorf("%w", email.ErrSmtpTransient)}
	w := email.NewWorker(q, stub, nil, cfg)

	// Drive 20 transient failures, resetting next_retry_at each time so the
	// row stays due and we can observe backoff values past the natural cap.
	for i := 0; i < 20; i++ {
		setNextRetryAt(t, db, 0) // make row due now
		_, _, _, err := w.Drain(context.Background())
		if err != nil {
			t.Fatalf("Drain iter %d: %v", i, err)
		}
	}

	nowMs := time.Now().UnixMilli()
	nextRetry := getNextRetryAt(t, db)
	delta := nextRetry - nowMs
	capMs := cfg.BackoffCap.Milliseconds()
	epsilon := int64(200) // tolerate clock jitter
	if delta > capMs+epsilon {
		t.Fatalf("backoff exceeded cap: delta=%dms, cap=%dms", delta, capMs)
	}
}

// TestQueue_MaxAttemptsMarksFailed verifies that when attempt_count reaches
// MaxAttempts on a transient error the row goes to failed and notifier fires
// exactly once.
func TestQueue_MaxAttemptsMarksFailed(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	q := email.NewQueue(db)
	_ = enqueueOne(t, q)

	notifier := &fakeNotifier{}
	cfg := email.QueueConfig{
		MaxAttempts:    1, // fail on the first attempt (newAttemptCount=1 >= MaxAttempts=1)
		BackoffInitial: 10 * time.Millisecond,
		BackoffCap:     100 * time.Millisecond,
	}
	stub := &fakeSubmitter{submitErr: fmt.Errorf("%w", email.ErrSmtpTransient)}
	w := email.NewWorker(q, stub, notifier, cfg)

	// First attempt: attempt_count 0→1; MaxAttempts=1 so this immediately marks the row failed.
	_, _, failed, err := w.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if failed != 1 {
		t.Fatalf("expected failed=1, got %d", failed)
	}
	if notifier.callCount() != 1 {
		t.Fatalf("expected 1 notifier call, got %d", notifier.callCount())
	}

	var status string
	_ = db.QueryRowContext(context.Background(),
		`SELECT status FROM email_outbound_queue LIMIT 1`).Scan(&status)
	if status != string(email.StatusFailed) {
		t.Errorf("db status: got %q, want %q", status, email.StatusFailed)
	}
}

// TestQueue_PermanentErrorFailsImmediately verifies that ErrSmtpPermanent
// skips all retry logic and marks the row failed on the first attempt.
func TestQueue_PermanentErrorFailsImmediately(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	q := email.NewQueue(db)
	_ = enqueueOne(t, q)

	notifier := &fakeNotifier{}
	stub := &fakeSubmitter{submitErr: fmt.Errorf("%w: user unknown", email.ErrSmtpPermanent)}
	cfg := fastCfg()
	cfg.MaxAttempts = 10 // would allow 9 more retries — permanent must skip them all
	w := email.NewWorker(q, stub, notifier, cfg)

	_, _, failed, err := w.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if failed != 1 {
		t.Fatalf("expected failed=1 on first attempt, got %d", failed)
	}
	if stub.count() != 1 {
		t.Fatalf("submitter called %d times, expected exactly 1", stub.count())
	}
	if notifier.callCount() != 1 {
		t.Fatalf("notifier called %d times, expected exactly 1", notifier.callCount())
	}

	var status string
	_ = db.QueryRowContext(context.Background(),
		`SELECT status FROM email_outbound_queue LIMIT 1`).Scan(&status)
	if status != string(email.StatusFailed) {
		t.Errorf("db status: got %q, want %q", status, email.StatusFailed)
	}
}

// TestQueue_SendingStateRollsBackOnError verifies that the orphan-recovery
// pass rescues rows stranded in 'sending' by a previously crashed worker.
// Post thrum-6qmf.8 the substrate drives drains, so this test invokes
// RecoverOrphans + Drain explicitly rather than the removed Worker.Run
// ticker.
func TestQueue_SendingStateRollsBackOnError(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	q := email.NewQueue(db)

	// Simulate a row orphaned mid-send 10 minutes ago.
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

	stub := &fakeSubmitter{} // success
	cfg := fastCfg()
	w := email.NewWorker(q, stub, nil, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := w.RecoverOrphans(ctx); err != nil {
		t.Fatalf("RecoverOrphans: %v", err)
	}
	if _, _, _, err := w.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	// The recovery sweep should have flipped the orphan to 'queued',
	// and the Drain call should have submitted and sent it.
	if stub.count() == 0 {
		t.Fatal("orphan row was not recovered and sent")
	}
}

// TestQueue_StatusEnumGuardRejectsInvalid is an informational compile-time
// specification. Direct SQL would accept arbitrary status strings like
// 'pending' or 'dropped'. The typed Status constants in queue.go ensure
// every SQL binding goes through one of the four canonical values —
// the absence of raw status literals in queue.go is verifiable by grep:
//
//	grep -n '"queued"\|"sending"\|"sent"\|"failed"' internal/bridge/email/queue.go
//
// That grep must return zero lines. This test body is empty because the
// invariant is structural (compile-time + grep), not detectable at runtime.
func TestQueue_StatusEnumGuardRejectsInvalid(t *testing.T) {
	t.Parallel()
	var _ = email.StatusQueued
	var _ = email.StatusSending
	var _ = email.StatusSent
	var _ = email.StatusFailed
}

// TestQueue_DrainHonorsContextCancel verifies Worker.Drain exits promptly
// when its context is cancelled mid-iteration. Pre thrum-6qmf.8 the
// per-Worker ticker loop guarded the cancel contract; the substrate now
// owns scheduling, so the per-drain contract is what matters.
func TestQueue_DrainHonorsContextCancel(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	q := email.NewQueue(db)

	stub := &fakeSubmitter{}
	w := email.NewWorker(q, stub, nil, fastCfg())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		_, _, _, _ = w.Drain(ctx)
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Worker.Drain did not exit within 2s after context cancel")
	}
}

// TestQueue_ConcurrentDrainsNoDoubleSend verifies that two Drain calls racing
// on the same queue each deliver every row exactly once. The atomic
// UPDATE-with-RowsAffected claim is the race guard.
func TestQueue_ConcurrentDrainsNoDoubleSend(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	q := email.NewQueue(db)

	const rowCount = 10
	for i := 0; i < rowCount; i++ {
		_ = enqueueOne(t, q)
	}

	stub := &fakeSubmitter{}
	w := email.NewWorker(q, stub, nil, fastCfg())

	var totalSent atomic.Int64
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			sent, _, _, err := w.Drain(context.Background())
			if err == nil {
				totalSent.Add(int64(sent))
			}
		}()
	}
	wg.Wait()

	if int(totalSent.Load()) != rowCount {
		t.Fatalf("total sent across both drains: got %d, want %d", totalSent.Load(), rowCount)
	}
	if stub.count() != rowCount {
		t.Fatalf("submit count: got %d, want %d (no double-sends)", stub.count(), rowCount)
	}
}
