package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/projection"
)

// SyncLoop manages the periodic sync cycle.
type SyncLoop struct {
	interval     time.Duration
	syncer       *Syncer
	projector    *projection.Projector
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
}

// NewSyncLoop creates a new sync loop.
// - syncer: handles git operations (fetch, merge, push)
// - projector: applies events to SQLite
// - repoPath: path to the git repository
// - syncDir: path to sync worktree (.git/thrum-sync/a-sync)
// - thrumDir: path to .thrum/ directory (used for lock path)
// - interval: how often to sync (default: 60 seconds)
// - localOnly: when true, skip all remote git operations (push/fetch).
func NewSyncLoop(syncer *Syncer, projector *projection.Projector, repoPath string, syncDir string, thrumDir string, interval time.Duration, localOnly bool) *SyncLoop {
	if interval == 0 {
		interval = 60 * time.Second
	}

	return &SyncLoop{
		interval:     interval,
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
	if err := l.syncer.branchManager.EnsureSyncBranch(); err != nil {
		return fmt.Errorf("ensure sync branch: %w", err)
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

	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()

	// Do an initial sync
	l.doSync(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.doSync(ctx)
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
		l.setError(fmt.Errorf("merge: %w", err))
		return
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

	// 5. Commit and push if local changes
	if err := l.syncer.CommitAndPush(ctx); err != nil {
		l.setError(fmt.Errorf("commit and push: %w", err))
		return
	}

	// Success - update status
	l.mu.Lock()
	l.lastSyncAt = time.Now()
	l.lastError = nil
	l.mu.Unlock()
}

// updateProjection applies the parsed events directly to SQLite.
// Phase 5 optimization: events are passed from merge step, eliminating redundant file I/O.
func (l *SyncLoop) updateProjection(ctx context.Context, parsedEvents []json.RawMessage) error {
	// Apply each parsed event to the projector
	for _, event := range parsedEvents {
		// Extract event ID for error reporting
		id, _ := extractEventIDFromRaw(event)

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
