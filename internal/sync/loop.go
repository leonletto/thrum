package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/projection"
)

// EventIngester is the minimal interface the sync loop uses to apply
// an event that arrived via git sync from a peer's JSONL. *state.State
// satisfies this via IngestSyncedEvent, which runs the projector AND
// fires the event-write hook. SyncLoop prefers this path when set so
// cross-repo replies can reach the permission package's reply
// interceptor (Task 6.3 / Phase 6 cross-repo correctness).
type EventIngester interface {
	IngestSyncedEvent(ctx context.Context, event []byte) error
}

// SyncLoop manages the event-triggered sync cycle.
type SyncLoop struct {
	syncer       *Syncer
	projector    *projection.Projector
	ingester     EventIngester // optional; when set, updateProjection routes through it
	repoPath     string
	syncDir      string // Path to sync worktree (.git/thrum-sync/a-sync)
	thrumDir     string // Path to .thrum/ directory (used for lock path)
	localOnly    bool   // when true, skip all remote git operations
	stopCh       chan struct{}
	stoppedCh    chan struct{}
	notifyCh     chan []string // Channel to notify of new event IDs
	manualSyncCh chan struct{} // Channel to trigger manual sync
	mu           sync.Mutex
	running      bool
	lastSyncAt   time.Time
	lastError    error
	// walkerCounts provides per-walk row counts for the sync.commit telemetry
	// event. Set via SetCommitCountsProvider from bootstrap; nil is safe (emits
	// zeros for the count fields). The provider returns (stateFiles, msgRows, rcptRows).
	walkerCounts func() (stateFiles, msgRows, rcptRows int)
}

// SetIngester installs an EventIngester so synced events flow through
// *state.State.IngestSyncedEvent (which fires the event-write hook)
// instead of calling projector.Apply directly. Production daemon boot
// calls this immediately after NewSyncLoop; tests that don't care
// about the hook can leave it nil and the projector-only fallback
// preserves prior behavior.
func (l *SyncLoop) SetIngester(ing EventIngester) {
	l.ingester = ing
}

// SetCommitCountsProvider wires a callback that returns the per-walk row
// counts (stateFiles, msgRows, rcptRows) from the most recent snapshot
// walker run. Called from bootstrap after the Walker is constructed:
//
//	syncLoop.SetCommitCountsProvider(func() (int, int, int) {
//	    c := walker.LastCounts()
//	    return c.StateFiles, c.MessageRows, c.ReceiptRows
//	})
//
// When nil (tests that don't construct a walker), doSync emits zero
// counts in the sync.commit event — safe and non-fatal.
func (l *SyncLoop) SetCommitCountsProvider(fn func() (stateFiles, msgRows, rcptRows int)) {
	l.walkerCounts = fn
}

// NewSyncLoop creates a new sync loop.
// - syncer: handles git operations (fetch, merge, push)
// - projector: applies events to SQLite
// - repoPath: path to the git repository
// - syncDir: path to sync worktree (.git/thrum-sync/a-sync)
// - thrumDir: path to .thrum/ directory (used for lock path)
// - localOnly: when true, skip all remote git operations (push/fetch).
//
// Sync is event-triggered (via Triggers.SyncOnWrite) as of v0.10.6
// (thrum-s6os). The periodic ticker has been removed; sync runs on
// structural writes and once at startup for catch-up.
func NewSyncLoop(syncer *Syncer, projector *projection.Projector, repoPath string, syncDir string, thrumDir string, localOnly bool) *SyncLoop {
	return &SyncLoop{
		syncer:       syncer,
		projector:    projector,
		repoPath:     repoPath,
		syncDir:      syncDir,
		thrumDir:     thrumDir,
		localOnly:    localOnly,
		stopCh:       make(chan struct{}),
		stoppedCh:    make(chan struct{}),
		notifyCh:     make(chan []string, 100), // Buffered for async notifications
		manualSyncCh: make(chan struct{}, 1),   // Buffered to avoid blocking
	}
}

// Start starts the sync loop in a goroutine.
func (l *SyncLoop) Start(ctx context.Context) error {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return fmt.Errorf("sync loop already running")
	}
	l.running = true
	l.mu.Unlock()

	// Ensure sync branch exists before starting loop
	if err := l.syncer.branchManager.EnsureSyncBranch(ctx); err != nil {
		return fmt.Errorf("ensure sync branch: %w", err)
	}

	// Ensure the sync worktree exists (and is healthy) before starting the loop.
	// Without this, git status --porcelain in hasChanges() fails with
	// "fatal: this operation must be run in a work tree" every sync cycle.
	if err := l.syncer.branchManager.CreateSyncWorktree(ctx, l.syncDir); err != nil {
		return fmt.Errorf("ensure sync worktree: %w", err)
	}

	go l.run(ctx)
	return nil
}

// Stop stops the sync loop and waits for it to finish.
func (l *SyncLoop) Stop() error {
	l.mu.Lock()
	if !l.running {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()

	close(l.stopCh)
	<-l.stoppedCh

	// Reset state for potential restart
	l.mu.Lock()
	l.running = false
	l.stopCh = make(chan struct{})
	l.stoppedCh = make(chan struct{})
	l.mu.Unlock()

	return nil
}

// TriggerSync manually triggers a sync cycle (non-blocking).
func (l *SyncLoop) TriggerSync() {
	select {
	case l.manualSyncCh <- struct{}{}:
	default:
		// Already a pending manual sync
	}
}

// NotifyChannel returns a channel that receives new event IDs after each sync.
// This is used by the subscription system (Epic 6) to notify subscribers.
func (l *SyncLoop) NotifyChannel() <-chan []string {
	return l.notifyCh
}

// IsLocalOnly returns whether the sync loop is in local-only mode.
func (l *SyncLoop) IsLocalOnly() bool {
	return l.localOnly
}

// GetStatus returns the current sync status.
func (l *SyncLoop) GetStatus() SyncStatus {
	l.mu.Lock()
	defer l.mu.Unlock()

	status := SyncStatus{
		Running:    l.running,
		LocalOnly:  l.localOnly,
		LastSyncAt: l.lastSyncAt,
	}

	if l.lastError != nil {
		status.LastError = l.lastError.Error()
	}

	return status
}

// SyncStatus contains the current status of the sync loop.
type SyncStatus struct {
	Running    bool      `json:"running"`
	LocalOnly  bool      `json:"local_only"`
	LastSyncAt time.Time `json:"last_sync_at"`
	LastError  string    `json:"last_error,omitempty"`
}

// run is the main loop that runs in a goroutine.
func (l *SyncLoop) run(ctx context.Context) {
	defer close(l.stoppedCh)

	// Do an initial sync to catch up on any peer events written while the
	// daemon was offline. Subsequent syncs are triggered by SyncOnWrite
	// (structural events) or TriggerSync (manual/RPC).
	l.doSync(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-l.stopCh:
			return
		case <-l.manualSyncCh:
			l.doSync(ctx)
		}
	}
}

// doSync performs a single sync cycle.
func (l *SyncLoop) doSync(ctx context.Context) {
	// Acquire lock
	lockPath := filepath.Join(paths.VarDir(l.thrumDir), "sync.lock")
	lock, err := acquireLock(lockPath)
	if err != nil {
		l.setError(fmt.Errorf("acquire lock: %w", err))
		return
	}
	defer func() { _ = releaseLock(lock) }()

	// 1. Fetch remote
	if err := l.syncer.merger.Fetch(ctx); err != nil {
		l.setError(fmt.Errorf("fetch: %w", err))
		return
	}

	// 2. Merge all files (events.jsonl + messages/*.jsonl)
	mergeResult, err := l.syncer.merger.MergeAll(ctx)
	if err != nil {
		if !l.localOnly {
			l.setError(fmt.Errorf("merge: %w", err))
			return
		}
		// In local-only mode, merge errors are expected (no remote to merge
		// from). Continue to CommitAndPush so local changes are committed.
		log.Printf("sync: merge skipped in local-only mode: %v", err)
	}

	// 3. Update SQLite projection with new events
	if mergeResult != nil && mergeResult.NewEvents > 0 {
		if err := l.updateProjection(ctx, mergeResult.NewParsedEvents); err != nil {
			l.setError(fmt.Errorf("update projection: %w", err))
			return
		}

		// 4. Notify subscribers of new events (Epic 6)
		if len(mergeResult.EventIDs) > 0 {
			select {
			case l.notifyCh <- mergeResult.EventIDs:
			default:
				// Channel full, log warning
				log.Printf("sync: notification channel full, dropping %d event IDs", len(mergeResult.EventIDs))
			}
		}
	}

	// 5. Commit and push if local changes.
	// Capture HEAD before CommitAndPush so the post-call comparison can
	// tell whether a new commit actually landed. Spec §10 requires
	// sync.commit to fire "per commit landed on a-sync" — emitting on
	// every doSync (including no-op CommitAndPush paths) would mint
	// false-positive telemetry that downstream operators can't easily
	// distinguish from real commits.
	preSHA := ""
	if shaBytes, shaErr := safecmd.Git(ctx, l.syncDir, "rev-parse", "HEAD"); shaErr == nil {
		preSHA = strings.TrimSpace(string(shaBytes))
	}
	if err := l.syncer.CommitAndPush(ctx); err != nil {
		l.setError(fmt.Errorf("commit and push: %w", err))
		return
	}

	// 6. Emit sync.commit telemetry only when a new commit actually
	// landed (post-HEAD differs from pre-HEAD).
	postSHA := ""
	if shaBytes, shaErr := safecmd.Git(ctx, l.syncDir, "rev-parse", "HEAD"); shaErr == nil {
		postSHA = strings.TrimSpace(string(shaBytes))
	}
	if postSHA != "" && postSHA != preSHA {
		stateFiles, msgRows, rcptRows := 0, 0, 0
		if l.walkerCounts != nil {
			stateFiles, msgRows, rcptRows = l.walkerCounts()
		}
		filesChanged := stateFiles + msgRows + rcptRows
		slog.Info("sync.commit",
			"commit_sha", postSHA,
			"files_changed", filesChanged,
			"state_files", stateFiles,
			"message_rows", msgRows,
			"receipt_rows", rcptRows)
	}

	// Success - update status
	l.mu.Lock()
	l.lastSyncAt = time.Now()
	l.lastError = nil
	l.mu.Unlock()
}

// updateProjection applies the parsed events to SQLite. When an
// EventIngester is set (production), it routes each event through
// state.IngestSyncedEvent so the event-write hook fires — that is the
// load-bearing bridge for cross-repo reply delivery. Without an
// ingester (tests that only care about projection), it falls back to
// calling projector.Apply directly.
//
// Phase 5 optimization: events are passed from the merge step,
// eliminating redundant file I/O.
func (l *SyncLoop) updateProjection(ctx context.Context, parsedEvents []json.RawMessage) error {
	for _, event := range parsedEvents {
		id, _ := extractEventIDFromRaw(event)

		if l.ingester != nil {
			if err := l.ingester.IngestSyncedEvent(ctx, event); err != nil {
				return fmt.Errorf("ingest synced event %s: %w", id, err)
			}
			continue
		}
		if err := l.projector.Apply(ctx, event); err != nil {
			return fmt.Errorf("apply event %s: %w", id, err)
		}
	}

	return nil
}

// extractEventIDFromRaw extracts the event ID from a raw JSON event.
func extractEventIDFromRaw(data json.RawMessage) (string, error) {
	// Parse to get type
	var base struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &base); err != nil {
		return "", err
	}

	// Extract ID based on type
	return extractEventID(data, base.Type)
}

// setError updates the last error status.
func (l *SyncLoop) setError(err error) {
	l.mu.Lock()
	l.lastError = err
	l.mu.Unlock()
	log.Printf("sync: error: %v", err)
}
