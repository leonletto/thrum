package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"
)

// MirrorEnqueuer is the subset of internal/skills/mirror.Worker that
// the watcher drives. Defined here in the parent package so the
// sub-package can satisfy the interface without creating an import
// cycle (parent MUST NOT import sub-package per spec §6).
type MirrorEnqueuer interface {
	Enqueue(event MirrorEvent, worktreePath string) error
}

// ProposalReminderer is the mint/cancel surface that the watcher
// invokes when proposed-skills directories appear or disappear.
// Implemented by *Staleness in E10.9; abstracted here so this task
// can land before staleness.go.
type ProposalReminderer interface {
	MintProposalReminder(ctx context.Context, proposalPath string) (string, error)
	CancelProposalReminder(ctx context.Context, proposalPath string) error
}

// SupervisorMessenger is the subset of internal/daemon/permission.
// Permission that the watcher uses to emit coordinator notifications
// for new proposed skills. Abstracted to keep internal/skills
// independent of the daemon's permission package.
//
// Empty threadID per AC: send.go treats "" as "open a new thread",
// NOT as a literal zero.
type SupervisorMessenger interface {
	SendSupervisorMessage(ctx context.Context, target, body, threadID string) error
}

// ChainResolver returns the list of coordinator agent IDs that
// should receive proposal notifications + staleness reminders. The
// same closure is injected into both this watcher and the E10.9
// Staleness instance so the resolver semantics stay consistent.
type ChainResolver func(ctx context.Context) ([]string, error)

// WatcherOpts injects the watcher's collaborators. Every field is
// required; New() panics on missing fields per the @researcher_skills
// pre-flagged nil-deref risk. The panic surfaces the wiring bug at
// construction time, NOT 500ms later inside a goroutine.
type WatcherOpts struct {
	// LibraryRoot is the absolute path to .thrum/skills/. Skill
	// changes here drive MirrorEvent enqueue.
	LibraryRoot string

	// ProposalRoot is the absolute path to .thrum/agents/ (the
	// parent of every <author>/proposed-skills/ directory).
	ProposalRoot string

	// Worktrees is the list of worktree paths that should receive
	// mirror events. The daemon-lifecycle layer (E9.4) populates
	// this from the same source the Worker uses.
	Worktrees []string

	// Mirror is the worker's Enqueue surface. Required.
	Mirror MirrorEnqueuer

	// Staleness is the proposal-reminder mint/cancel surface.
	// Required.
	Staleness ProposalReminderer

	// Supervisor is the coordinator-notification sink. Required.
	Supervisor SupervisorMessenger

	// Resolver returns the coordinator agent IDs that should
	// receive proposal notifications. Same closure as the one
	// passed to Staleness.
	Resolver ChainResolver
}

// WatcherEvent is the internal event channel emitted by the watcher
// for test introspection. Production code does not consume this —
// production effects flow through Mirror/Staleness/Supervisor.
type WatcherEvent struct {
	Kind       string // "library_change", "proposal_new", "proposal_removed", "reconcile"
	Path       string
	SkillName  string
	Author     string
	Frontmatter Frontmatter
	Err        error
}

// Watcher wraps an fsnotify.Watcher and translates filesystem events
// into mirror enqueues + proposal notifications + staleness mint/
// cancel calls. One Watcher per repo root; the daemon-lifecycle
// layer constructs and owns it.
type Watcher struct {
	opts    WatcherOpts
	fsw     *fsnotify.Watcher
	started atomic.Bool
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	mu      sync.Mutex
	authors map[string]struct{} // tracks author dirs we've added to the fsnotify watch
	subs    []chan WatcherEvent
	subsMu  sync.Mutex
}

// ErrWatcherNotStarted fires when Stop or Subscribe is called before
// Start. Matches the worker's once-started contract idiom.
var ErrWatcherNotStarted = errors.New("skills: watcher not started")

// NewWatcher validates WatcherOpts and constructs a Watcher. Panics
// on missing required fields (every field is required; see WatcherOpts
// docs for why). Use defer-recover in callers if a graceful-error
// surface is needed, though in practice the daemon-lifecycle wiring
// has all fields present-or-bug.
func NewWatcher(opts WatcherOpts) *Watcher {
	if opts.LibraryRoot == "" {
		panic("skills: WatcherOpts.LibraryRoot is required")
	}
	if opts.ProposalRoot == "" {
		panic("skills: WatcherOpts.ProposalRoot is required")
	}
	if opts.Mirror == nil {
		panic("skills: WatcherOpts.Mirror is required")
	}
	if opts.Staleness == nil {
		panic("skills: WatcherOpts.Staleness is required")
	}
	if opts.Supervisor == nil {
		panic("skills: WatcherOpts.Supervisor is required")
	}
	if opts.Resolver == nil {
		panic("skills: WatcherOpts.Resolver is required")
	}
	return &Watcher{
		opts:    opts,
		authors: make(map[string]struct{}),
	}
}

// Subscribe returns a typed event channel that mirrors every
// fsnotify-derived event the watcher processes. Test-only surface;
// production effects flow through the injected interfaces. Buffer
// is sized 64 to absorb bursts during reconcile-on-boot.
func (w *Watcher) Subscribe() <-chan WatcherEvent {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	ch := make(chan WatcherEvent, 64)
	w.subs = append(w.subs, ch)
	return ch
}

// Start initializes the fsnotify watcher, registers existing
// directories, walks pre-existing proposed-skills to emit Reconcile
// events, and launches the event-distribution goroutine.
func (w *Watcher) Start(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return errors.New("skills: watcher already started")
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		w.started.Store(false)
		return fmt.Errorf("skills: fsnotify.NewWatcher: %w", err)
	}
	w.fsw = fsw

	// Ensure roots exist so AddWatch doesn't fail noisily.
	if err := os.MkdirAll(w.opts.LibraryRoot, 0o750); err != nil {
		w.started.Store(false)
		_ = fsw.Close()
		return fmt.Errorf("skills: mkdir library: %w", err)
	}
	if err := os.MkdirAll(w.opts.ProposalRoot, 0o750); err != nil {
		w.started.Store(false)
		_ = fsw.Close()
		return fmt.Errorf("skills: mkdir proposals: %w", err)
	}

	// Watch the library root recursively (one Add per subdirectory).
	if err := w.addLibraryWatches(); err != nil {
		w.started.Store(false)
		_ = fsw.Close()
		return err
	}

	// Watch the proposal root + every existing author's proposed-skills/.
	if err := fsw.Add(w.opts.ProposalRoot); err != nil {
		w.started.Store(false)
		_ = fsw.Close()
		return fmt.Errorf("skills: watch proposal root: %w", err)
	}
	if err := w.addProposalWatchesAndReconcile(ctx); err != nil {
		w.started.Store(false)
		_ = fsw.Close()
		return err
	}

	ctx, w.cancel = context.WithCancel(ctx)
	w.wg.Add(1)
	go w.run(ctx)
	return nil
}

// Stop cancels the run-loop, closes the fsnotify watcher, and waits
// for the goroutine to exit.
func (w *Watcher) Stop() error {
	if !w.started.Load() {
		return nil
	}
	w.cancel()
	if err := w.fsw.Close(); err != nil {
		// Continue waiting for goroutine; surface close error after.
		w.wg.Wait()
		w.started.Store(false)
		return fmt.Errorf("skills: fsnotify close: %w", err)
	}
	w.wg.Wait()
	w.started.Store(false)

	w.subsMu.Lock()
	for _, ch := range w.subs {
		close(ch)
	}
	w.subs = nil
	w.subsMu.Unlock()
	return nil
}

// addLibraryWatches registers <LibraryRoot> + each existing skill
// subdirectory with fsnotify. Sub-skill files (SKILL.md, helpers,
// assets) inherit the parent dir's watch.
func (w *Watcher) addLibraryWatches() error {
	if err := w.fsw.Add(w.opts.LibraryRoot); err != nil {
		return fmt.Errorf("skills: watch library root: %w", err)
	}
	entries, err := os.ReadDir(w.opts.LibraryRoot)
	if err != nil {
		return fmt.Errorf("skills: read library root: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(w.opts.LibraryRoot, e.Name())
		if err := w.fsw.Add(dir); err != nil {
			return fmt.Errorf("skills: watch %s: %w", dir, err)
		}
	}
	return nil
}

// addProposalWatchesAndReconcile registers each existing author's
// proposed-skills/ directory + emits a Reconcile event per pre-
// existing proposal so the staleness mint at boot covers every
// in-flight draft.
func (w *Watcher) addProposalWatchesAndReconcile(ctx context.Context) error {
	authorDirs, err := os.ReadDir(w.opts.ProposalRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("skills: read proposal root: %w", err)
	}
	for _, ad := range authorDirs {
		if !ad.IsDir() {
			continue
		}
		authorDir := filepath.Join(w.opts.ProposalRoot, ad.Name())
		if err := w.fsw.Add(authorDir); err != nil {
			return fmt.Errorf("skills: watch %s: %w", authorDir, err)
		}
		proposedDir := filepath.Join(authorDir, "proposed-skills")
		if _, err := os.Stat(proposedDir); err != nil {
			continue
		}
		if err := w.fsw.Add(proposedDir); err != nil {
			return fmt.Errorf("skills: watch %s: %w", proposedDir, err)
		}
		w.mu.Lock()
		w.authors[ad.Name()] = struct{}{}
		w.mu.Unlock()

		// Reconcile pass: emit a synthetic event per existing
		// proposal so the staleness mint covers every in-flight draft.
		proposals, err := os.ReadDir(proposedDir)
		if err != nil {
			return fmt.Errorf("skills: read %s: %w", proposedDir, err)
		}
		for _, p := range proposals {
			if !p.IsDir() {
				continue
			}
			skillMd := filepath.Join(proposedDir, p.Name(), "SKILL.md")
			if _, err := os.Stat(skillMd); err != nil {
				continue
			}
			w.handleProposalNew(ctx, skillMd, ad.Name(), p.Name(), "reconcile")
		}
	}
	return nil
}

// run is the fsnotify event-distribution loop. Owns dispatch to the
// per-event handlers below.
func (w *Watcher) run(ctx context.Context) {
	defer w.wg.Done()
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.dispatch(ctx, ev)
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			// fsnotify errors are non-fatal; the next event repairs
			// state. Loop continues.
		case <-ctx.Done():
			return
		}
	}
}

// dispatch routes an fsnotify event to the library or proposal
// handler based on path prefix. Unknown paths are ignored (a stale
// watch on a removed directory can fire one event after removal).
func (w *Watcher) dispatch(ctx context.Context, ev fsnotify.Event) {
	switch {
	case strings.HasPrefix(ev.Name, w.opts.LibraryRoot+string(filepath.Separator)):
		w.handleLibraryEvent(ev)
	case strings.HasPrefix(ev.Name, w.opts.ProposalRoot+string(filepath.Separator)):
		w.handleProposalEvent(ctx, ev)
	}
}

// handleLibraryEvent fans an event in .thrum/skills/ out to every
// worktree's mirror destination. SkillName is derived from the path
// relative to LibraryRoot — the first segment.
func (w *Watcher) handleLibraryEvent(ev fsnotify.Event) {
	rel, err := filepath.Rel(w.opts.LibraryRoot, ev.Name)
	if err != nil || rel == "." {
		return
	}
	parts := strings.Split(rel, string(filepath.Separator))
	skillName := parts[0]
	if skillName == "" || skillName == ".gitkeep" {
		return
	}

	var kind MirrorEventKind
	switch {
	case ev.Op&fsnotify.Remove != 0:
		kind = MirrorEventKindDelete
	case ev.Op&fsnotify.Create != 0:
		kind = MirrorEventKindCreate
		// New skill subdirectory: add it to the watch list.
		full := filepath.Join(w.opts.LibraryRoot, skillName)
		if info, statErr := os.Stat(full); statErr == nil && info.IsDir() {
			_ = w.fsw.Add(full)
		}
	case ev.Op&fsnotify.Write != 0:
		kind = MirrorEventKindUpdate
	default:
		return
	}

	mirrorEv := MirrorEvent{
		Kind:      kind,
		SkillName: skillName,
		Trigger:   TriggerFileChange,
	}
	for _, wtree := range w.opts.Worktrees {
		if err := w.opts.Mirror.Enqueue(mirrorEv, wtree); err != nil {
			w.broadcast(WatcherEvent{Kind: "library_change", Path: ev.Name, SkillName: skillName, Err: err})
			continue
		}
	}
	w.broadcast(WatcherEvent{Kind: "library_change", Path: ev.Name, SkillName: skillName})
}

// handleProposalEvent distinguishes between three sub-cases:
//   - A new author directory appears under ProposalRoot
//   - A SKILL.md is created/removed under an existing author's
//     proposed-skills/<name>/
//   - The proposed-skills/<name>/ directory itself is removed
func (w *Watcher) handleProposalEvent(ctx context.Context, ev fsnotify.Event) {
	rel, err := filepath.Rel(w.opts.ProposalRoot, ev.Name)
	if err != nil || rel == "." {
		return
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 {
		return
	}
	author := parts[0]

	// New author dir under ProposalRoot — start watching it.
	if len(parts) == 1 && ev.Op&fsnotify.Create != 0 {
		w.addAuthorWatch(author)
		return
	}

	// proposed-skills/ subdirectory created inside an author dir
	// after Start — register the watch. Handles the v0.10.x → v0.11
	// case where the daemon comes up before any agent has minted
	// state, and the case where addAuthorWatch raced the parent
	// MkdirAll and missed the proposed-skills/ child.
	if len(parts) == 2 && parts[1] == "proposed-skills" && ev.Op&fsnotify.Create != 0 {
		proposedDir := filepath.Join(w.opts.ProposalRoot, author, "proposed-skills")
		_ = w.fsw.Add(proposedDir)
		return
	}

	// Proposed-skills/<name>/ removal: handle cancel.
	if len(parts) >= 3 && parts[1] == "proposed-skills" {
		skillName := parts[2]
		proposalPath := filepath.Join(w.opts.ProposalRoot, author, "proposed-skills", skillName, "SKILL.md")
		switch {
		case ev.Op&fsnotify.Remove != 0:
			// Removal can fire for the SKILL.md itself or the
			// directory. Either way, cancel the reminder by path.
			if cancelErr := w.opts.Staleness.CancelProposalReminder(ctx, proposalPath); cancelErr != nil {
				w.broadcast(WatcherEvent{Kind: "proposal_removed", Path: proposalPath, SkillName: skillName, Author: author, Err: cancelErr})
				return
			}
			w.broadcast(WatcherEvent{Kind: "proposal_removed", Path: proposalPath, SkillName: skillName, Author: author})
			return
		case ev.Op&fsnotify.Create != 0, ev.Op&fsnotify.Write != 0:
			// When the skill subdir is created, add a watch on it
			// so subsequent SKILL.md write events fire. fsnotify
			// otherwise only sees the dir-level Create, not the
			// file inside.
			skillDir := filepath.Join(w.opts.ProposalRoot, author, "proposed-skills", skillName)
			if info, statErr := os.Stat(skillDir); statErr == nil && info.IsDir() {
				_ = w.fsw.Add(skillDir)
			}
			// Dispatch to proposal-new handler when the SKILL.md
			// materializes. fsnotify may fire Create on the
			// directory first and Write on the file shortly after;
			// the handler is idempotent (Staleness.Mint is
			// idempotent per its contract).
			if _, statErr := os.Stat(proposalPath); statErr == nil {
				w.handleProposalNew(ctx, proposalPath, author, skillName, "fsnotify")
			}
		}
	}
}

// handleProposalNew is the shared code path for both the reconcile-
// at-boot walk and the live fsnotify Create event. Loads the
// frontmatter, sends a supervisor notification, and mints a
// staleness reminder.
func (w *Watcher) handleProposalNew(ctx context.Context, proposalPath, author, skillName, source string) {
	data, err := os.ReadFile(proposalPath) //nolint:gosec // proposalPath is <repoRoot>/.thrum/agents/<author>/proposed-skills/<name>/SKILL.md
	if err != nil {
		w.broadcast(WatcherEvent{Kind: "proposal_new", Path: proposalPath, SkillName: skillName, Author: author, Err: err})
		return
	}
	fm, _, _ := splitFrontmatter(data)

	// Notify every coordinator agent. Resolver errors are non-fatal;
	// we still emit the WatcherEvent so tests see the trigger.
	coords, resolverErr := w.opts.Resolver(ctx)
	if resolverErr == nil {
		body := fmt.Sprintf("Skill proposal: %s/%s at %s (trigger=%s)", author, skillName, proposalPath, source)
		for _, coord := range coords {
			if sendErr := w.opts.Supervisor.SendSupervisorMessage(ctx, coord, body, ""); sendErr != nil {
				w.broadcast(WatcherEvent{Kind: "proposal_new", Path: proposalPath, SkillName: skillName, Author: author, Frontmatter: fm, Err: sendErr})
				continue
			}
		}
	}

	if _, err := w.opts.Staleness.MintProposalReminder(ctx, proposalPath); err != nil {
		w.broadcast(WatcherEvent{Kind: "proposal_new", Path: proposalPath, SkillName: skillName, Author: author, Frontmatter: fm, Err: err})
		return
	}
	w.broadcast(WatcherEvent{Kind: "proposal_new", Path: proposalPath, SkillName: skillName, Author: author, Frontmatter: fm})
}

// addAuthorWatch starts watching a newly-created author directory
// under ProposalRoot. Idempotent.
func (w *Watcher) addAuthorWatch(author string) {
	w.mu.Lock()
	if _, ok := w.authors[author]; ok {
		w.mu.Unlock()
		return
	}
	w.authors[author] = struct{}{}
	w.mu.Unlock()

	authorDir := filepath.Join(w.opts.ProposalRoot, author)
	_ = w.fsw.Add(authorDir)
	proposedDir := filepath.Join(authorDir, "proposed-skills")
	if _, err := os.Stat(proposedDir); err == nil {
		_ = w.fsw.Add(proposedDir)
	}
}

// broadcast fans a WatcherEvent to every Subscribe()'d channel.
// Non-blocking: drops on full subscribers so a slow test consumer
// can't block the run loop.
func (w *Watcher) broadcast(ev WatcherEvent) {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	for _, ch := range w.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}
