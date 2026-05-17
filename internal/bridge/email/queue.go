package email

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// Status is a typed enum for email_outbound_queue.status. Every SQL binding
// of the status column MUST go through one of these constants — the absence
// of raw string literals in this file is the load-bearing guard against drift.
type Status string

const (
	StatusQueued  Status = "queued"
	StatusSending Status = "sending"
	StatusSent    Status = "sent"
	StatusFailed  Status = "failed"
)

// QueueEnvelope is the caller-visible input to Enqueue. Internal state
// (attempt_count, retry timing, status) is owned entirely by the queue.
type QueueEnvelope struct {
	FromAgent   string
	ToAddress   string
	Subject     string // empty → persisted as NULL (column is NULLable per §3.8)
	Body        string
	HeadersJSON string // raw JSON object; empty → defaults to "{}"
}

// SMTPSubmitter is the minimal surface over SMTPClient that the queue worker
// needs. Decoupled so unit tests supply a stub without spinning up a real
// SMTP server.
type SMTPSubmitter interface {
	Submit(ctx context.Context, env Envelope) error
}

// QueueConfig controls retry behaviour and polling cadence.
type QueueConfig struct {
	MaxAttempts    int           // row moves to 'failed' after this many total attempts (default 10)
	BackoffInitial time.Duration // delay after the first failed attempt (default 5s)
	BackoffCap     time.Duration // upper bound on backoff growth (default 5min)
	PollInterval   time.Duration // how often the worker ticks (default 5s)
}

func (c *QueueConfig) applyDefaults() {
	if c.MaxAttempts == 0 {
		c.MaxAttempts = 10
	}
	if c.BackoffInitial == 0 {
		c.BackoffInitial = 5 * time.Second
	}
	if c.BackoffCap == 0 {
		c.BackoffCap = 5 * time.Minute
	}
	if c.PollInterval == 0 {
		c.PollInterval = 5 * time.Second
	}
}

// Queue handles DB-level operations for email_outbound_queue. Stateless
// beyond the db handle — safe to use from multiple goroutines.
type Queue struct {
	db *sql.DB
}

// NewQueue returns a Queue backed by db. Caller must have already run
// schema.InitDB so the table + index exist.
func NewQueue(db *sql.DB) *Queue {
	return &Queue{db: db}
}

// Enqueue inserts an outbound email as status=queued with next_retry_at=now.
// Returns the autoincrement id of the new row.
func (q *Queue) Enqueue(ctx context.Context, env QueueEnvelope) (int64, error) {
	headersJSON := env.HeadersJSON
	if headersJSON == "" {
		headersJSON = "{}"
	}
	// Guard against garbage JSON before it enters the column.
	if !json.Valid([]byte(headersJSON)) {
		return 0, fmt.Errorf("email queue: headers_json is not valid JSON")
	}

	nowMs := nowMillis()

	// subject is NULLable per §3.8; map "" → nil so the column stores NULL.
	var subject interface{}
	if env.Subject != "" {
		subject = env.Subject
	}

	res, err := q.db.ExecContext(ctx, `
		INSERT INTO email_outbound_queue
			(from_agent, to_address, subject, body, headers_json,
			 attempt_count, next_retry_at, status, enqueued_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		env.FromAgent, env.ToAddress, subject, env.Body, headersJSON,
		nowMs, string(StatusQueued), nowMs, nowMs,
	)
	if err != nil {
		return 0, fmt.Errorf("email queue enqueue: %w", err)
	}
	return res.LastInsertId()
}

// Worker polls the queue and submits due rows via the configured SMTPSubmitter.
type Worker struct {
	q         *Queue
	submitter SMTPSubmitter
	notifier  CoordinatorNotifier
	cfg       QueueConfig
}

// NewWorker returns a Worker. cfg fields with zero values receive sensible
// defaults via applyDefaults.
func NewWorker(q *Queue, submitter SMTPSubmitter, notifier CoordinatorNotifier, cfg QueueConfig) *Worker {
	cfg.applyDefaults()
	return &Worker{q: q, submitter: submitter, notifier: notifier, cfg: cfg}
}

// Run is a long-running ticker loop. On startup it runs the orphan-row
// recovery sweep once, then drains on each tick. Returns when ctx is done.
func (w *Worker) Run(ctx context.Context) error {
	// Recovery sweep: rows left in 'sending' by a crashed worker are
	// invisible to Drain's WHERE status='queued' predicate. Any row that
	// has been 'sending' for more than 5 minutes must have survived a crash
	// — flip it back to 'queued' so it gets retried.
	_ = w.recoverOrphans(ctx)

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, _, _, err := w.Drain(ctx); err != nil && !errors.Is(err, context.Canceled) {
				// Transient DB errors on a single tick are non-fatal; log
				// and keep looping rather than killing the worker.
				_ = err
			}
		}
	}
}

// Drain performs one full iteration: claims all due queued rows and
// submits them. Returns counts of rows that were sent, requeued, or
// failed in this pass. Testable as a synchronous unit.
func (w *Worker) Drain(ctx context.Context) (sent, requeued, failed int, err error) {
	ids, err := w.dueCandidates(ctx)
	if err != nil {
		return 0, 0, 0, err
	}

	for _, id := range ids {
		if ctx.Err() != nil {
			return sent, requeued, failed, ctx.Err()
		}

		outcome, oErr := w.processOne(ctx, id)
		switch outcome {
		case outcomeSent:
			sent++
		case outcomeRequeued:
			requeued++
		case outcomeFailed:
			failed++
		case outcomeSkipped:
			// another concurrent worker claimed it first
		}
		if oErr != nil && !errors.Is(oErr, errSkipped) {
			// Non-fatal: log but continue to next row.
			_ = oErr
		}
	}
	return sent, requeued, failed, nil
}

// outcome codes returned by processOne to keep Drain's counter logic clean.
type outcome int

const (
	outcomeSent     outcome = iota
	outcomeRequeued outcome = iota
	outcomeFailed   outcome = iota
	outcomeSkipped  outcome = iota
)

var errSkipped = errors.New("row claimed by another worker")

// processOne atomically claims a single queued row, calls the submitter,
// and persists the result. The atomic claim (UPDATE with RowsAffected check)
// means two concurrent Drain calls can never double-send the same row.
func (w *Worker) processOne(ctx context.Context, id int64) (outcome, error) {
	nowMs := nowMillis()

	// Atomic claim: flip queued→sending only if status is still 'queued'.
	// RowsAffected==0 means another goroutine already claimed it — skip.
	res, err := w.q.db.ExecContext(ctx, `
		UPDATE email_outbound_queue
		   SET status = ?, updated_at = ?
		 WHERE id = ? AND status = ?`,
		string(StatusSending), nowMs, id, string(StatusQueued),
	)
	if err != nil {
		return outcomeSkipped, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return outcomeSkipped, err
	}
	if n != 1 {
		return outcomeSkipped, errSkipped
	}

	// Load the full row now that we own it.
	row := w.q.db.QueryRowContext(ctx, `
		SELECT from_agent, to_address, subject, body, headers_json, attempt_count
		  FROM email_outbound_queue WHERE id = ?`, id)

	var (
		fromAgent    string
		toAddress    string
		subjectNull  sql.NullString
		body         string
		headersJSON  string
		attemptCount int
	)
	if err := row.Scan(&fromAgent, &toAddress, &subjectNull, &body, &headersJSON, &attemptCount); err != nil {
		// Row vanished between claim and SELECT — extremely unlikely with
		// SQLite single-writer, but handle it gracefully.
		return outcomeSkipped, err
	}

	subject := ""
	if subjectNull.Valid {
		subject = subjectNull.String
	}

	// Build the Envelope that SMTPClient.Submit expects.
	env := Envelope{
		From: fromAgent,
		To:   []string{toAddress},
		// The queue stores the raw MIME body in the body column.
		Raw: []byte(body),
	}
	_ = subject // present in the queue row; MIME headers are in Raw / headers_json

	submitErr := w.submitter.Submit(ctx, env)
	if submitErr == nil {
		return outcomeSent, w.markSent(ctx, id)
	}

	// Permanent error: skip retries regardless of attempt_count.
	if errors.Is(submitErr, ErrSmtpPermanent) {
		if err := w.markFailed(ctx, id, attemptCount+1, submitErr.Error()); err != nil {
			return outcomeFailed, err
		}
		w.alertCoordinator(ctx, toAddress, attemptCount+1, submitErr.Error())
		return outcomeFailed, nil
	}

	// Transient error: requeue with backoff, or permanently fail if we've
	// hit MaxAttempts.
	newAttemptCount := attemptCount + 1
	if newAttemptCount >= w.cfg.MaxAttempts {
		if err := w.markFailed(ctx, id, newAttemptCount, submitErr.Error()); err != nil {
			return outcomeFailed, err
		}
		w.alertCoordinator(ctx, toAddress, newAttemptCount, submitErr.Error())
		return outcomeFailed, nil
	}

	nextRetry := computeNextRetry(newAttemptCount, w.cfg.BackoffInitial, w.cfg.BackoffCap)
	return outcomeRequeued, w.markRequeued(ctx, id, newAttemptCount, nextRetry, submitErr.Error())
}

// markSent writes the terminal success state.
func (w *Worker) markSent(ctx context.Context, id int64) error {
	_, err := w.q.db.ExecContext(ctx, `
		UPDATE email_outbound_queue
		   SET status = ?, updated_at = ?, last_error = NULL
		 WHERE id = ?`,
		string(StatusSent), nowMillis(), id,
	)
	return err
}

// markFailed writes the terminal failure state.
func (w *Worker) markFailed(ctx context.Context, id int64, attempts int, lastErr string) error {
	_, err := w.q.db.ExecContext(ctx, `
		UPDATE email_outbound_queue
		   SET status = ?, attempt_count = ?, last_error = ?, updated_at = ?
		 WHERE id = ?`,
		string(StatusFailed), attempts, truncateError(lastErr), nowMillis(), id,
	)
	return err
}

// markRequeued flips the row back to 'queued' with updated backoff timing.
func (w *Worker) markRequeued(ctx context.Context, id int64, attempts int, nextRetryAt int64, lastErr string) error {
	_, err := w.q.db.ExecContext(ctx, `
		UPDATE email_outbound_queue
		   SET status = ?, attempt_count = ?, next_retry_at = ?, last_error = ?, updated_at = ?
		 WHERE id = ?`,
		string(StatusQueued), attempts, nextRetryAt, truncateError(lastErr), nowMillis(), id,
	)
	return err
}

// alertCoordinator sends a single notification per failed row. One alert per
// failure event, not per retry — callers invoke this exactly once when a row
// transitions to 'failed'.
func (w *Worker) alertCoordinator(ctx context.Context, toAddress string, attempts int, lastErr string) {
	if w.notifier == nil {
		return
	}
	msg := fmt.Sprintf("@coordinator_main: email send to %s failed after %d attempts (last error: %s)",
		toAddress, attempts, truncateError(lastErr))
	_ = w.notifier.Notify(ctx, msg)
}

// recoverOrphans flips rows stuck in 'sending' for more than 5 minutes back
// to 'queued'. Rows go 'sending' when a worker claims them; the only way
// they stay 'sending' indefinitely is a worker crash mid-submit. 5 minutes
// is chosen to be safely larger than any realistic SMTP handshake timeout
// (30s dial + 30s per DATA chunk) while still short enough to catch stuck
// rows before the operator notices.
func (w *Worker) recoverOrphans(ctx context.Context) error {
	cutoffMs := nowMillis() - (5 * 60 * 1000) // 5 minutes ago
	_, err := w.q.db.ExecContext(ctx, `
		UPDATE email_outbound_queue
		   SET status = ?, updated_at = ?
		 WHERE status = ? AND updated_at < ?`,
		string(StatusQueued), nowMillis(), string(StatusSending), cutoffMs,
	)
	return err
}

// dueCandidates returns the ids of queued rows whose next_retry_at is ≤ now,
// ordered by enqueued_at so oldest rows are delivered first. Uses the
// idx_email_queue_next index.
func (w *Worker) dueCandidates(ctx context.Context) ([]int64, error) {
	rows, err := w.q.db.QueryContext(ctx, `
		SELECT id FROM email_outbound_queue
		 WHERE status = ? AND next_retry_at <= ?
		 ORDER BY enqueued_at ASC
		 LIMIT 100`,
		string(StatusQueued), nowMillis(),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// computeNextRetry returns the unix-millisecond timestamp for the next retry.
// attempt is the count AFTER the current failure (so attempt=1 → first retry
// delay is BackoffInitial * 2^0 = BackoffInitial).
func computeNextRetry(attempt int, initial, maxDelay time.Duration) int64 {
	exp := math.Pow(2, float64(attempt-1))
	delay := time.Duration(float64(initial) * exp)
	if delay > maxDelay {
		delay = maxDelay
	}
	return nowMillis() + delay.Milliseconds()
}

// nowMillis returns the current time as unix milliseconds.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}

// truncateError caps error strings stored in last_error at 512 bytes to
// prevent runaway messages from inflating the DB row size.
func truncateError(s string) string {
	const limit = 512
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

