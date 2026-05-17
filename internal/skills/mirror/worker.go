package mirror

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leonletto/thrum/internal/skills"
)

// ErrWorkerNotStarted fires when Enqueue is called before Start has
// installed the per-destination goroutines. Pre-flagged by
// @researcher_skills as a deadlock-prone invariant: callers must
// observe a typed error rather than blocking on an unbuffered channel
// that has no receiver yet. The TestWorker_EnqueueBeforeStart test
// pins this contract.
var ErrWorkerNotStarted = errors.New("mirror: worker not started")

// ErrWorkerAlreadyStarted fires on a second Start call. The
// once-started contract guards the per-destination map + goroutine
// roster: Start mutates state that the rest of the worker treats as
// immutable thereafter.
var ErrWorkerAlreadyStarted = errors.New("mirror: worker already started")

// Destination uniquely identifies a mirror target — a (worktree,
// runtime) pair gets its own channel, goroutine, and mutex. E9.5's
// EnsureMirrored shares the same mutex via the worker's MutexRegistry()
// so synchronous wake-handler calls don't tear concurrent async writes.
type Destination struct {
	WorktreePath string
	Runtime      string
}

// WorkerOpts configures a Worker. Constructor (New) applies defaults
// for any zero-value Duration field; SourceRoot and Destinations must
// be supplied by the caller (the daemon-lifecycle code at E9.4 builds
// them at boot).
//
// Pre-flagged risk (from @researcher_skills brainstorm): nil-deref
// from forgotten injected deps. New() panics on missing required
// fields rather than constructing a half-wired Worker that surfaces
// the bug 500ms later inside a goroutine.
type WorkerOpts struct {
	// SourceRoot is the absolute path to .thrum/skills/ from which
	// promoted skills are copied. Required.
	SourceRoot string

	// Destinations is the full set of (worktree, runtime) pairs the
	// worker will serve. Required; may be empty (worker starts cleanly
	// with no goroutines — sensible for a tests-only daemon).
	Destinations []Destination

	// Debounce is the per-destination coalescing window. Events for
	// the same SkillName within this window collapse to one apply
	// (latest-wins). Defaults to 500ms per spec §12.3.
	Debounce time.Duration

	// StopTimeout caps how long Stop waits for in-flight applies to
	// drain before force-cancelling. Defaults to 5s per AC.
	StopTimeout time.Duration

	// Logger is the slog handler used for overwrite-with-warning
	// surfaces (spec §12.5). When nil, falls back to slog.Default()
	// so tests that don't care about log output don't have to wire
	// anything.
	Logger *slog.Logger
}

// Worker is the serialized-per-destination mirror worker. One channel
// + one goroutine + one mutex per destination. Mutex registry is
// exposed via MutexRegistry() so E9.5's synchronous EnsureMirrored can
// acquire the same lock the async path uses.
type Worker struct {
	opts    WorkerOpts
	started atomic.Bool

	// stateMu guards channels, worktree, and registered against the
	// Stop race: Stop closes channels and reassigns the maps while
	// Enqueue / Reconcile / EnsureMirrored may be mid-flight.
	// Without this mutex, an Enqueue that passed the started.Load()
	// check could send to a closed channel and panic; Reconcile /
	// EnsureMirrored could race the map reassignment in Stop.
	//
	// Pattern: callers acquire RLock just long enough to snapshot
	// the fields they need (channel slice, destinations slice,
	// registered presence), then release the lock BEFORE doing slow
	// filesystem work or waiting on channel sends. Stop takes the
	// write lock to flip started→false + close channels (front
	// half), then again to reassign the maps after WaitGroup drain.
	stateMu  sync.RWMutex
	channels map[Destination]chan mirrorTask
	worktree map[string][]Destination
	// registered tracks every worktreePath the caller asked to mirror,
	// including those whose runtime resolved to a null-adapter entry.
	// EnsureMirrored uses this to distinguish "unknown worktree
	// (caller bug)" from "registered worktree with no v0.11 mirror
	// surface (success-skip)".
	registered map[string]struct{}

	mutexes sync.Map
	wg      sync.WaitGroup
	cancel  context.CancelFunc
}

// mirrorTask is the per-event payload that travels on the
// destination channel. Wrapped (rather than sending the raw
// MirrorEvent) so future fields (telemetry, batched-enqueue
// support) don't break callers. A non-nil reconcileWG marks a
// boot-time reconcile tick: the destination goroutine runs a full
// canonical-vs-destination diff + apply and signals completion via
// wg.Done.
//
// nameFilter is the optional scoping set for E10.7's skill.sync
// names argument. When non-nil, reconcileDestination only considers
// canonical entries whose name is in the set AND only removes
// destination entries whose name is in the set (so unscoped skills
// at the destination remain untouched).
type mirrorTask struct {
	event       skills.MirrorEvent
	reconcileWG *sync.WaitGroup
	nameFilter  map[string]struct{}
}

// New constructs a Worker, applies defaults, and validates required
// deps. Panics on missing-required because a half-wired Worker
// surfaces its bug far from the construction site and is a real
// foot-gun in tests (@researcher_skills pre-flagged this).
func New(opts WorkerOpts) *Worker {
	if opts.SourceRoot == "" {
		panic("mirror: WorkerOpts.SourceRoot is required")
	}
	if opts.Debounce == 0 {
		opts.Debounce = 500 * time.Millisecond
	}
	if opts.StopTimeout == 0 {
		opts.StopTimeout = 5 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Worker{
		opts:       opts,
		channels:   make(map[Destination]chan mirrorTask),
		worktree:   make(map[string][]Destination),
		registered: make(map[string]struct{}),
	}
}

// Start spawns one goroutine per non-null-adapter destination.
// Per-destination goroutines are guaranteed to be running before
// Start returns — callers can Enqueue safely from that moment without
// risking an enqueue-before-receiver deadlock.
func (w *Worker) Start(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return ErrWorkerAlreadyStarted
	}
	ctx, w.cancel = context.WithCancel(ctx)

	// Acquire a per-destination mutex up-front so the registry is
	// stable before any goroutine starts touching it.
	for _, dest := range w.opts.Destinations {
		entry, err := Lookup(dest.Runtime)
		if err != nil {
			// Unknown runtime is a misconfiguration; roll back the
			// started flag so a future Start can retry once the
			// config is fixed.
			w.started.Store(false)
			return fmt.Errorf("mirror: start destination %+v: %w", dest, err)
		}
		// Mark the worktree as registered regardless of adapter
		// state so EnsureMirrored can distinguish "unknown" from
		// "null-adapter success-skip".
		w.registered[dest.WorktreePath] = struct{}{}
		if entry == nil {
			// Null adapter — runtime is registered but has no v0.11
			// mirror surface. Skip silently per spec §11.
			continue
		}
		w.mutexes.LoadOrStore(destinationKey(dest), &sync.Mutex{})

		ch := make(chan mirrorTask, 64)
		w.channels[dest] = ch
		w.worktree[dest.WorktreePath] = append(w.worktree[dest.WorktreePath], dest)

		w.wg.Add(1)
		// Use a started-barrier channel so Start cannot return until
		// the destination goroutine has actually entered its select
		// loop. Without this, a fast caller could Enqueue before
		// runDestination starts ranging on ch and block on the
		// buffered channel filling. The barrier is closed inside the
		// goroutine just before its first select.
		ready := make(chan struct{})
		go w.runDestination(ctx, dest, entry, ch, ready)
		<-ready
	}
	return nil
}

// Stop closes all channels and waits up to StopTimeout for pending
// applies to drain. After the timeout, remaining goroutines are
// force-cancelled via the per-Start context. After Stop returns the
// worker can be Start'd again with new opts (callers typically just
// discard the Worker).
//
// Ordering against Enqueue: stateMu blocks new Enqueues from seeing
// the maps mid-transition. Inside the lock we flip started→false
// FIRST (so any Enqueue waiting on the lock bails out with
// ErrWorkerNotStarted instead of sending to a closed channel), then
// close the channels and reassign the maps.
func (w *Worker) Stop() error {
	if !w.started.Load() {
		return nil
	}

	w.stateMu.Lock()
	w.started.Store(false)
	for _, ch := range w.channels {
		close(ch)
	}
	w.stateMu.Unlock()

	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(w.opts.StopTimeout):
		w.cancel()
		<-done
	}

	w.stateMu.Lock()
	w.channels = make(map[Destination]chan mirrorTask)
	w.worktree = make(map[string][]Destination)
	w.registered = make(map[string]struct{})
	w.stateMu.Unlock()
	return nil
}

// Enqueue dispatches an event to every destination registered for
// the supplied worktreePath. A null-adapter runtime contributes no
// destinations and is a silent skip. Enqueue before Start returns
// ErrWorkerNotStarted rather than blocking on a non-existent receiver
// — pinned by TestWorker_EnqueueBeforeStart per coordinator's pre-flag.
//
// Enqueue is non-blocking on a healthy worker: each destination
// channel is buffered (size 64) and the debounce coalesces same-name
// events. A full channel surfaces ErrBackpressure rather than
// blocking the caller (the watcher's fsnotify loop must never block).
func (w *Worker) Enqueue(event skills.MirrorEvent, worktreePath string) error {
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	if !w.started.Load() {
		return ErrWorkerNotStarted
	}
	dests, ok := w.worktree[worktreePath]
	if !ok || len(dests) == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownWorktree, worktreePath)
	}
	for _, dest := range dests {
		ch := w.channels[dest]
		select {
		case ch <- mirrorTask{event: event}:
		default:
			// Backpressure: drop the oldest pending event in the
			// channel to make room. Since each event is debounced
			// per-SkillName at the destination goroutine, the worst
			// case is one missed mid-stream event for one skill —
			// the next event for that skill repairs the state. This
			// is preferable to blocking the watcher loop.
			select {
			case <-ch:
			default:
			}
			ch <- mirrorTask{event: event}
		}
	}
	return nil
}

// EnqueueAll dispatches an event to every registered worktree that
// has at least one destination (null-adapter worktrees are silently
// skipped). Returns the count of successfully-enqueued worktrees and
// the first error encountered (callers log + continue on subsequent
// failures — the next Reconcile pass repairs drift).
//
// Intended for the skill.delete handler at E10.6: a coordinator-issued
// delete must propagate Kind=delete to every destination, but the
// handler doesn't know the worktree list. This method encapsulates
// both the worktree enumeration and the per-worktree enqueue under a
// single lock-acquisition boundary.
func (w *Worker) EnqueueAll(event skills.MirrorEvent) (int, error) {
	w.stateMu.RLock()
	if !w.started.Load() {
		w.stateMu.RUnlock()
		return 0, ErrWorkerNotStarted
	}
	wtrees := make([]string, 0, len(w.worktree))
	for wtree := range w.worktree {
		wtrees = append(wtrees, wtree)
	}
	w.stateMu.RUnlock()

	var count int
	var firstErr error
	for _, wtree := range wtrees {
		if err := w.Enqueue(event, wtree); err != nil {
			// ErrUnknownWorktree shouldn't surface here (we just
			// snapshotted from w.worktree), but a concurrent Stop +
			// Start could race. Treat as silent skip + record as the
			// firstErr for caller diagnostics.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		count++
	}
	return count, firstErr
}

// MutexRegistry exposes the per-destination mutex map so E9.5's
// synchronous EnsureMirrored can serialize against the async worker.
// Keys are destinationKey(Destination) strings; values are
// *sync.Mutex. Callers must Lock/Unlock — the registry does not own
// the lifecycle. The map itself never has entries deleted (only
// added at Start), so concurrent reads + LoadOrStore are race-clean
// per sync.Map's documented guarantees.
func (w *Worker) MutexRegistry() *sync.Map {
	return &w.mutexes
}

// destinationKey returns the canonical mutex-registry key for a
// destination. Exported indirectly via MutexRegistry callers; kept
// unexported so the encoding can evolve without breaking consumers.
func destinationKey(dest Destination) string {
	return dest.WorktreePath + "|" + dest.Runtime
}

func (w *Worker) destMutex(dest Destination) *sync.Mutex {
	v, _ := w.mutexes.LoadOrStore(destinationKey(dest), &sync.Mutex{})
	mu, ok := v.(*sync.Mutex)
	if !ok {
		// Unreachable: every LoadOrStore call site stores
		// *sync.Mutex. Panic surfaces the corruption immediately
		// rather than masking it.
		panic("mirror: destMutex: registry value is not *sync.Mutex")
	}
	return mu
}

// runDestination is the per-destination goroutine. Owns one pending
// map (skillName -> latest event) and one debounce timer. Events
// arrive on ch; closing ch signals drain-and-exit. ctx cancellation
// is the force-stop path used by Worker.Stop after StopTimeout.
func (w *Worker) runDestination(
	ctx context.Context,
	dest Destination,
	entry *AdapterEntry,
	ch chan mirrorTask,
	ready chan struct{},
) {
	defer w.wg.Done()
	mu := w.destMutex(dest)

	pending := make(map[string]skills.MirrorEvent)
	var timer *time.Timer
	var timerC <-chan time.Time

	flush := func() {
		// Snapshot + clear so the apply path can't be reentered with
		// state changing under it.
		batch := pending
		pending = make(map[string]skills.MirrorEvent)
		timer = nil
		timerC = nil

		mu.Lock()
		defer mu.Unlock()
		for _, ev := range batch {
			if err := w.applyOne(ev, dest, entry); err != nil {
				w.opts.Logger.Warn(
					"skills mirror apply failed",
					"worktree", dest.WorktreePath,
					"runtime", dest.Runtime,
					"skill", ev.SkillName,
					"kind", string(ev.Kind),
					"err", err,
				)
			}
		}
	}

	// Signal Start: this goroutine is now in the select loop and
	// ready to receive. Prevents enqueue-before-receiver races.
	close(ready)

	for {
		select {
		case task, ok := <-ch:
			if !ok {
				flush()
				return
			}
			if task.reconcileWG != nil {
				// Reconcile tick: flush any pending debounced work
				// first, then run a full canonical-vs-destination
				// diff under the destination mutex. Errors are
				// logged inside reconcileDestination; Reconcile's
				// caller is best-effort.
				flush()
				_ = w.reconcileDestination(dest, entry, task.nameFilter)
				task.reconcileWG.Done()
				continue
			}
			pending[task.event.SkillName] = task.event
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(w.opts.Debounce)
			timerC = timer.C
		case <-timerC:
			flush()
		case <-ctx.Done():
			flush()
			return
		}
	}
}

// Reconcile walks the source library and re-applies every skill to
// every destination, then removes any destination-side skill that's
// no longer canonical. Blocks until every destination signals
// completion. Used at daemon boot (per E9.4 lifecycle wiring) to
// repair drift accumulated while the daemon was down.
//
// Reconcile is idempotent: a second call against converged state
// performs zero filesystem writes (copyFile skips identical content).
// Null-adapter destinations were never registered, so there's nothing
// to reconcile for them.
//
// Pre-flagged-MINOR-#7 guard: Worker.Start's ready-barrier ensures
// every destination goroutine is in its select loop before Start
// returns. Reconcile cannot deadlock on a non-existent receiver.
func (w *Worker) Reconcile(ctx context.Context) error {
	// Snapshot the channel set under the read lock so a concurrent
	// Stop can't reassign w.channels mid-loop. Release the lock
	// before sending on the channels (the sends can block briefly
	// behind debounced applies; holding stateMu while we wait would
	// stall Stop and any other Enqueue).
	w.stateMu.RLock()
	if !w.started.Load() {
		w.stateMu.RUnlock()
		return ErrWorkerNotStarted
	}
	channels := make([]chan mirrorTask, 0, len(w.channels))
	for _, ch := range w.channels {
		channels = append(channels, ch)
	}
	w.stateMu.RUnlock()

	// SIGKILL backstop for E10.4 atomic-move promote: a promote that
	// was killed between writing the `.thrum/skills/<name>.tmp/` staging
	// dir and the os.Rename into `.thrum/skills/<name>/` leaves the
	// staging dir on disk. Defer-rollback inside HandlePromote handles
	// the common-case failures during a live promote; this catches the
	// "process vanished" case at the next daemon boot's Reconcile pass.
	//
	// Symmetric cleanup for `.old/` backup-aside dirs created by the
	// edit-promote rollback path — same SIGKILL window, same fix.
	w.cleanupStalePromoteLeftovers()

	var wg sync.WaitGroup
	for _, ch := range channels {
		wg.Add(1)
		select {
		case ch <- mirrorTask{
			event:       skills.MirrorEvent{Trigger: skills.TriggerRestartReconcile},
			reconcileWG: &wg,
		}:
		case <-ctx.Done():
			wg.Done() // balance the Add so wg.Wait can exit
			return ctx.Err()
		}
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReconcileNames is the scoped variant of Reconcile: only the listed
// skill names are considered at each destination. Both the copy pass
// AND the stale-removal pass honor the filter — so unrelated skills
// at the destination remain untouched by a scoped sync. An empty or
// nil names slice falls through to a full Reconcile (the caller's
// "no filter" intent is preserved).
//
// Intended for the skill.sync RPC at E10.7 when the operator passes
// `names=[a, b]`. The synchronous-from-caller's-perspective contract
// is preserved (blocks until every destination's reconcile pass
// completes, same as Reconcile).
func (w *Worker) ReconcileNames(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return w.Reconcile(ctx)
	}
	filter := make(map[string]struct{}, len(names))
	for _, n := range names {
		filter[n] = struct{}{}
	}

	w.stateMu.RLock()
	if !w.started.Load() {
		w.stateMu.RUnlock()
		return ErrWorkerNotStarted
	}
	channels := make([]chan mirrorTask, 0, len(w.channels))
	for _, ch := range w.channels {
		channels = append(channels, ch)
	}
	w.stateMu.RUnlock()

	w.cleanupStalePromoteLeftovers()

	var wg sync.WaitGroup
	for _, ch := range channels {
		wg.Add(1)
		select {
		case ch <- mirrorTask{
			event:       skills.MirrorEvent{Trigger: skills.TriggerManualSync},
			reconcileWG: &wg,
			nameFilter:  filter,
		}:
		case <-ctx.Done():
			wg.Done()
			return ctx.Err()
		}
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// reconcileDestination performs a full canonical-vs-destination diff
// + apply under the destination mutex. Holds the lock for the
// entire pass so EnsureMirrored (E9.5) can't see a half-reconciled
// state.
//
// Returns the first filesystem error encountered. The async
// Reconcile caller logs + swallows the error (best-effort drift
// correction); the synchronous EnsureMirrored caller surfaces it so
// B-B1's stage-3 wake handler can roll back. Wrapped errors satisfy
// errors.Is(_, ErrMirrorWrite).
func (w *Worker) reconcileDestination(dest Destination, entry *AdapterEntry, nameFilter map[string]struct{}) error {
	mu := w.destMutex(dest)
	mu.Lock()
	defer mu.Unlock()

	// Enumerate canonical skills (skills with a SKILL.md in
	// <SourceRoot>/<name>/). A read error here MUST NOT be silently
	// swallowed: if we treated read failure as "empty canonical",
	// the cleanup loop below would wipe every destination skill on
	// a transient filesystem hiccup. Surface the error loudly + bail
	// before any destructive work.
	entries, readErr := os.ReadDir(w.opts.SourceRoot)
	if readErr != nil {
		if !os.IsNotExist(readErr) {
			err := fmt.Errorf("%w: read source %s: %w", ErrMirrorWrite, w.opts.SourceRoot, readErr)
			w.opts.Logger.Warn(
				"skills mirror reconcile aborted: source read failed",
				"worktree", dest.WorktreePath,
				"runtime", dest.Runtime,
				"err", err,
			)
			return err
		}
		// SourceRoot legitimately doesn't exist (fresh repo before
		// any skills landed) — proceed with an empty canonical
		// set. The cleanup loop's destDir read is the next gate.
	}
	canonical := map[string]struct{}{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if nameFilter != nil {
			if _, ok := nameFilter[e.Name()]; !ok {
				continue
			}
		}
		if _, err := os.Stat(filepath.Join(w.opts.SourceRoot, e.Name(), "SKILL.md")); err == nil {
			canonical[e.Name()] = struct{}{}
		}
	}

	destDir := filepath.Join(dest.WorktreePath, entry.MirrorPath)

	// Copy / refresh every canonical skill.
	var firstErr error
	for name := range canonical {
		srcDir := filepath.Join(w.opts.SourceRoot, name)
		dstDir := filepath.Join(destDir, name)
		if err := w.copyDir(srcDir, dstDir); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			w.opts.Logger.Warn(
				"skills mirror reconcile apply failed",
				"worktree", dest.WorktreePath,
				"runtime", dest.Runtime,
				"skill", name,
				"err", err,
			)
		}
	}

	// Remove destination-side skills that aren't in canonical. When
	// nameFilter is set, removal is also scoped — unrelated destination
	// skills are not touched by a scoped reconcile.
	if entries, err := os.ReadDir(destDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if _, ok := canonical[e.Name()]; ok {
				continue
			}
			if nameFilter != nil {
				if _, ok := nameFilter[e.Name()]; !ok {
					continue
				}
			}
			stale := filepath.Join(destDir, e.Name())
			if rmErr := os.RemoveAll(stale); rmErr != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%w: remove %s: %w", ErrMirrorWrite, stale, rmErr)
				}
				w.opts.Logger.Warn(
					"skills mirror reconcile remove failed",
					"path", stale,
					"err", rmErr,
				)
			}
		}
	}
	return firstErr
}

// applyOne performs the filesystem mutation for a single event under
// the destination mutex (held by the caller). Returns any
// filesystem error so the caller can log it; the caller does NOT
// retry — the watcher's next event repairs state on a transient
// failure, and reconcile-on-restart handles permanent ones.
func (w *Worker) applyOne(ev skills.MirrorEvent, dest Destination, entry *AdapterEntry) error {
	srcDir := filepath.Join(w.opts.SourceRoot, ev.SkillName)
	dstDir := filepath.Join(dest.WorktreePath, entry.MirrorPath, ev.SkillName)

	switch ev.Kind {
	case skills.MirrorEventKindDelete:
		if err := os.RemoveAll(dstDir); err != nil {
			return fmt.Errorf("%w: remove %s: %w", ErrMirrorWrite, dstDir, err)
		}
		return nil
	case skills.MirrorEventKindCreate, skills.MirrorEventKindUpdate, skills.MirrorEventKindReconcile:
		return w.copyDir(srcDir, dstDir)
	default:
		return fmt.Errorf("mirror: unknown event kind %q", ev.Kind)
	}
}

// copyDir recursively mirrors srcDir to dstDir. Modes are 0755 for
// directories and 0644 for files per plan §E9.2 AC — the mirror
// surface is intentionally runtime-readable (agent runtimes pick up
// SKILL.md files via their own loader, which runs in the user's
// own process; broader read needs the looser bits). Pre-existing
// destination files with different content trigger an overwrite-with-
// warning log per spec §12.5.
func (w *Worker) copyDir(srcDir, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil { // #nosec G301 -- runtime-readable mirror per plan AC
		return fmt.Errorf("%w: mkdir %s: %w", ErrMirrorWrite, dstDir, err)
	}
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return relErr
		}
		dst := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755) // #nosec G301 -- runtime-readable mirror per plan AC
		}
		return w.copyFile(path, dst)
	})
}

// copyFile copies one file, applying the overwrite-with-warning rule.
// The src/dst pair has already been computed by copyDir; this
// function is the leaf write.
func (w *Worker) copyFile(src, dst string) error {
	srcData, err := os.ReadFile(src) // #nosec G304 -- src derived from filepath.WalkDir over caller-supplied SourceRoot
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrMirrorWrite, src, err)
	}

	// Overwrite-with-warning: only when an existing file's content
	// differs. Identical files are a no-op (avoids spurious warns on
	// reconcile-at-restart where every file looks "already there").
	if existing, readErr := os.ReadFile(dst); readErr == nil { // #nosec G304 -- dst derived from caller-supplied worktree + adapter MirrorPath
		if !bytes.Equal(existing, srcData) {
			w.opts.Logger.Warn(
				"skills mirror overwriting hand-edited file",
				"path", dst,
			)
		} else {
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { // #nosec G301 -- runtime-readable mirror per plan AC
		return fmt.Errorf("%w: mkdir %s: %w", ErrMirrorWrite, filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, srcData, 0o644); err != nil { // #nosec G306 -- runtime-readable mirror per plan AC
		return fmt.Errorf("%w: write %s: %w", ErrMirrorWrite, dst, err)
	}
	return nil
}

// cleanupStalePromoteLeftovers removes any `.tmp/` and `.old/`
// directories directly under the source root. These are atomic-move
// artifacts from the E10.4 skill.promote handler that survived a
// SIGKILL between the temp-dir write and the final os.Rename — under
// a live promote, defer-rollback removes both. Both patterns are
// unconditionally removed because:
//
//   - `.tmp/` is the in-progress staging dir; if the rename had
//     succeeded it would have become `<name>/` (no .tmp suffix).
//   - `.old/` is the renamed-aside previous version during an edit-
//     promote; success removes it, failure rolls back over it.
//
// Errors during cleanup are logged at warn and swallowed — the next
// Reconcile pass will retry, and a lingering leftover is at worst
// disk waste (the mirror destination layer never reads from
// `<SourceRoot>/*.tmp/`).
func (w *Worker) cleanupStalePromoteLeftovers() {
	matches, err := filepath.Glob(filepath.Join(w.opts.SourceRoot, "*.tmp"))
	if err == nil {
		for _, m := range matches {
			// Log BEFORE the remove so operators reading the daemon
			// log see the announcement of the intended action. A
			// post-remove log line reads as past-tense narration of
			// completed work, but the cleanup might fail — only the
			// pre-log accurately reflects "about to do X" even when
			// the followup errors out.
			w.opts.Logger.Warn("skills promote: removing stale tmp dir from prior crash", "path", m)
			if rmErr := os.RemoveAll(m); rmErr != nil {
				w.opts.Logger.Warn("skills promote: failed to clean stale tmp dir", "path", m, "err", rmErr)
				continue
			}
		}
	} else {
		w.opts.Logger.Warn("skills promote: glob *.tmp failed", "root", w.opts.SourceRoot, "err", err)
	}
	matches, err = filepath.Glob(filepath.Join(w.opts.SourceRoot, "*.old"))
	if err == nil {
		for _, m := range matches {
			w.opts.Logger.Warn("skills promote: removing stale backup dir from prior crash", "path", m)
			if rmErr := os.RemoveAll(m); rmErr != nil {
				w.opts.Logger.Warn("skills promote: failed to clean stale backup dir", "path", m, "err", rmErr)
				continue
			}
		}
	} else {
		w.opts.Logger.Warn("skills promote: glob *.old failed", "root", w.opts.SourceRoot, "err", err)
	}
}

// Compile-time interface guards make the public surface explicit so
// future refactors don't accidentally hide a method. io.Closer
// covers the Stop pattern most consumers reach for.
var _ io.Closer = (*Worker)(nil)

// Close is io.Closer's contract; delegates to Stop. Allows the
// worker to participate in `defer w.Close()` patterns without the
// caller having to remember a non-standard method name.
func (w *Worker) Close() error { return w.Stop() }
