package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// fakeMirror records every Enqueue call so tests can assert events
// flowed through the watcher → mirror surface.
type fakeMirror struct {
	mu     sync.Mutex
	events []MirrorEvent
	wtrees []string
}

func (f *fakeMirror) Enqueue(event MirrorEvent, worktreePath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
	f.wtrees = append(f.wtrees, worktreePath)
	return nil
}

func (f *fakeMirror) snapshot() ([]MirrorEvent, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]MirrorEvent, len(f.events))
	copy(out, f.events)
	tr := make([]string, len(f.wtrees))
	copy(tr, f.wtrees)
	return out, tr
}

// fakeStaleness records mint + cancel calls. Tests can also inject a
// returnErr to confirm error paths surface.
type fakeStaleness struct {
	mu        sync.Mutex
	mints     []string
	cancels   []string
	returnErr error
}

func (f *fakeStaleness) MintProposalReminder(_ context.Context, path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mints = append(f.mints, path)
	return "reminder-" + filepath.Base(path), f.returnErr
}

func (f *fakeStaleness) CancelProposalReminder(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, path)
	return f.returnErr
}

func (f *fakeStaleness) mintCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.mints)
}

func (f *fakeStaleness) cancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cancels)
}

// fakeSupervisor records every supervisor notification.
type fakeSupervisor struct {
	mu        sync.Mutex
	messages  []supervisorMsg
	returnErr error
}

type supervisorMsg struct {
	Target   string
	Body     string
	ThreadID string
}

func (f *fakeSupervisor) SendSupervisorMessage(_ context.Context, target, body, threadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, supervisorMsg{Target: target, Body: body, ThreadID: threadID})
	return f.returnErr
}

func (f *fakeSupervisor) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

// fixtureRoots returns a tempdir with .thrum/skills + .thrum/agents
// pre-created. Tests use it as a fresh repo root.
func fixtureRoots(t *testing.T) (libraryRoot, proposalRoot string) {
	t.Helper()
	root := t.TempDir()
	libraryRoot = filepath.Join(root, ".thrum", "skills")
	proposalRoot = filepath.Join(root, ".thrum", "agents")
	for _, d := range []string{libraryRoot, proposalRoot} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	return libraryRoot, proposalRoot
}

func defaultOpts(libraryRoot, proposalRoot string, mirror MirrorEnqueuer, staleness ProposalReminderer, supervisor SupervisorMessenger) WatcherOpts {
	return WatcherOpts{
		LibraryRoot:  libraryRoot,
		ProposalRoot: proposalRoot,
		Worktrees:    []string{filepath.Dir(filepath.Dir(libraryRoot))},
		Mirror:       mirror,
		Staleness:    staleness,
		Supervisor:   supervisor,
		Resolver: func(_ context.Context) ([]string, error) {
			return []string{"@coordinator_main"}, nil
		},
	}
}

// waitFor polls cond up to timeout. Used pervasively because fsnotify
// is async: a write here surfaces an event a moment later.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func writeProposal(t *testing.T, proposalRoot, author, name, body string) string {
	t.Helper()
	dir := filepath.Join(proposalRoot, author, "proposed-skills", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func proposalFrontmatter(author, name string) string {
	return `---
name: ` + name + `
description: fixture proposal
thrum:
  proposed_by: "@` + author + `"
  trigger_reason: testing
---

# ` + name + `
`
}

func TestWatcher_NewPanicsOnMissingDep(t *testing.T) {
	t.Parallel()

	checks := []struct {
		name string
		opts WatcherOpts
	}{
		{name: "missing LibraryRoot", opts: WatcherOpts{ProposalRoot: "/tmp/p", Mirror: &fakeMirror{}, Staleness: &fakeStaleness{}, Supervisor: &fakeSupervisor{}, Resolver: func(_ context.Context) ([]string, error) { return nil, nil }}},
		{name: "missing ProposalRoot", opts: WatcherOpts{LibraryRoot: "/tmp/s", Mirror: &fakeMirror{}, Staleness: &fakeStaleness{}, Supervisor: &fakeSupervisor{}, Resolver: func(_ context.Context) ([]string, error) { return nil, nil }}},
		{name: "missing Mirror", opts: WatcherOpts{LibraryRoot: "/tmp/s", ProposalRoot: "/tmp/p", Staleness: &fakeStaleness{}, Supervisor: &fakeSupervisor{}, Resolver: func(_ context.Context) ([]string, error) { return nil, nil }}},
		{name: "missing Staleness", opts: WatcherOpts{LibraryRoot: "/tmp/s", ProposalRoot: "/tmp/p", Mirror: &fakeMirror{}, Supervisor: &fakeSupervisor{}, Resolver: func(_ context.Context) ([]string, error) { return nil, nil }}},
		{name: "missing Supervisor", opts: WatcherOpts{LibraryRoot: "/tmp/s", ProposalRoot: "/tmp/p", Mirror: &fakeMirror{}, Staleness: &fakeStaleness{}, Resolver: func(_ context.Context) ([]string, error) { return nil, nil }}},
		{name: "missing Resolver", opts: WatcherOpts{LibraryRoot: "/tmp/s", ProposalRoot: "/tmp/p", Mirror: &fakeMirror{}, Staleness: &fakeStaleness{}, Supervisor: &fakeSupervisor{}}},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("expected panic for %s, got none", c.name)
				}
			}()
			_ = NewWatcher(c.opts)
		})
	}
}

func TestWatcher_StartWalksExistingProposedSkills(t *testing.T) {
	t.Parallel()

	libraryRoot, proposalRoot := fixtureRoots(t)
	writeProposal(t, proposalRoot, "alice", "x1", proposalFrontmatter("alice", "x1"))
	writeProposal(t, proposalRoot, "bob", "y1", proposalFrontmatter("bob", "y1"))

	staleness := &fakeStaleness{}
	w := NewWatcher(defaultOpts(libraryRoot, proposalRoot, &fakeMirror{}, staleness, &fakeSupervisor{}))
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	if staleness.mintCount() != 2 {
		t.Errorf("expected 2 reconcile mints, got %d", staleness.mintCount())
	}
}

func TestWatcher_DetectsNewProposedSkill(t *testing.T) {
	t.Parallel()

	libraryRoot, proposalRoot := fixtureRoots(t)
	// Pre-create the author dir so the watch is registered when
	// Start fires (the fsnotify add-author-on-create branch is
	// tested separately by TestWatcher_AddsNewAuthorDir).
	if err := os.MkdirAll(filepath.Join(proposalRoot, "alice", "proposed-skills"), 0o750); err != nil {
		t.Fatalf("mkdir author dir: %v", err)
	}

	staleness := &fakeStaleness{}
	supervisor := &fakeSupervisor{}
	w := NewWatcher(defaultOpts(libraryRoot, proposalRoot, &fakeMirror{}, staleness, supervisor))
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	writeProposal(t, proposalRoot, "alice", "foo", proposalFrontmatter("alice", "foo"))

	waitFor(t, 1*time.Second, func() bool {
		return staleness.mintCount() >= 1 && supervisor.count() >= 1
	}, "proposal mint + supervisor notification")
}

func TestWatcher_DetectsRemovedProposedSkill(t *testing.T) {
	t.Parallel()

	libraryRoot, proposalRoot := fixtureRoots(t)
	skillMd := writeProposal(t, proposalRoot, "alice", "foo", proposalFrontmatter("alice", "foo"))

	staleness := &fakeStaleness{}
	w := NewWatcher(defaultOpts(libraryRoot, proposalRoot, &fakeMirror{}, staleness, &fakeSupervisor{}))
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	// Boot-time reconcile mints; clear that baseline so we can
	// assert the cancel below cleanly.
	if staleness.mintCount() != 1 {
		t.Fatalf("expected 1 boot-time mint, got %d", staleness.mintCount())
	}

	// Remove the entire proposal dir — matches production (a
	// completed proposal is `rm -rf`'d, not just the SKILL.md
	// inside). fsnotify watches the parent proposed-skills/ dir, so
	// the foo/ removal fires there.
	if err := os.RemoveAll(filepath.Dir(skillMd)); err != nil {
		t.Fatalf("remove proposal dir: %v", err)
	}
	waitFor(t, 1*time.Second, func() bool {
		return staleness.cancelCount() >= 1
	}, "proposal cancel")
}

func TestWatcher_DetectsNewPromotedSkill(t *testing.T) {
	t.Parallel()

	libraryRoot, proposalRoot := fixtureRoots(t)
	mirror := &fakeMirror{}
	w := NewWatcher(defaultOpts(libraryRoot, proposalRoot, mirror, &fakeStaleness{}, &fakeSupervisor{}))
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	skillDir := filepath.Join(libraryRoot, "demo")
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("body"), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	waitFor(t, 1*time.Second, func() bool {
		evs, _ := mirror.snapshot()
		for _, e := range evs {
			if e.SkillName == "demo" && (e.Kind == MirrorEventKindCreate || e.Kind == MirrorEventKindUpdate) {
				return true
			}
		}
		return false
	}, "mirror create/update for demo")
}

func TestWatcher_DetectsRemovedPromotedSkill(t *testing.T) {
	t.Parallel()

	libraryRoot, proposalRoot := fixtureRoots(t)
	// Pre-create the skill dir so Start watches it.
	skillDir := filepath.Join(libraryRoot, "doomed")
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("body"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	mirror := &fakeMirror{}
	w := NewWatcher(defaultOpts(libraryRoot, proposalRoot, mirror, &fakeStaleness{}, &fakeSupervisor{}))
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	if err := os.RemoveAll(skillDir); err != nil {
		t.Fatalf("remove skill dir: %v", err)
	}
	waitFor(t, 1*time.Second, func() bool {
		evs, _ := mirror.snapshot()
		for _, e := range evs {
			if e.SkillName == "doomed" && e.Kind == MirrorEventKindDelete {
				return true
			}
		}
		return false
	}, "mirror delete for doomed")
}

func TestWatcher_AddsNewAuthorDir(t *testing.T) {
	t.Parallel()

	libraryRoot, proposalRoot := fixtureRoots(t)
	staleness := &fakeStaleness{}
	w := NewWatcher(defaultOpts(libraryRoot, proposalRoot, &fakeMirror{}, staleness, &fakeSupervisor{}))
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	// Author dir doesn't exist at Start time. Create it + a proposal.
	if err := os.MkdirAll(filepath.Join(proposalRoot, "newbie", "proposed-skills"), 0o750); err != nil {
		t.Fatalf("mkdir new author: %v", err)
	}
	// Give the watcher a tick to pick up the new author dir.
	time.Sleep(80 * time.Millisecond)

	writeProposal(t, proposalRoot, "newbie", "z1", proposalFrontmatter("newbie", "z1"))

	waitFor(t, 1*time.Second, func() bool {
		return staleness.mintCount() >= 1
	}, "mint for new-author proposal")
}

func TestWatcher_FrontmatterInvalidStillNotifies(t *testing.T) {
	t.Parallel()

	libraryRoot, proposalRoot := fixtureRoots(t)
	// Pre-create the author dir + a malformed SKILL.md so the
	// boot-reconcile pass exercises the invalid-frontmatter path.
	badProposal := writeProposal(t, proposalRoot, "alice", "bad", `---
name: bad
description: [malformed YAML
thrum: bogus
---

body
`)
	_ = badProposal

	staleness := &fakeStaleness{}
	supervisor := &fakeSupervisor{}
	w := NewWatcher(defaultOpts(libraryRoot, proposalRoot, &fakeMirror{}, staleness, supervisor))
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	// Bad frontmatter doesn't block mint/notify — the watcher
	// surfaces the proposal anyway so a coordinator sees it and
	// requests a fix.
	if staleness.mintCount() != 1 {
		t.Errorf("expected 1 mint despite malformed frontmatter, got %d", staleness.mintCount())
	}
	if supervisor.count() != 1 {
		t.Errorf("expected 1 supervisor notification, got %d", supervisor.count())
	}
}

func TestWatcher_NoGoroutineLeakOnStop(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("testing.(*T).Parallel"),
	)

	libraryRoot, proposalRoot := fixtureRoots(t)
	w := NewWatcher(defaultOpts(libraryRoot, proposalRoot, &fakeMirror{}, &fakeStaleness{}, &fakeSupervisor{}))
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = w.Stop()
}

func TestWatcher_StartTwiceReturnsErr(t *testing.T) {
	t.Parallel()

	libraryRoot, proposalRoot := fixtureRoots(t)
	w := NewWatcher(defaultOpts(libraryRoot, proposalRoot, &fakeMirror{}, &fakeStaleness{}, &fakeSupervisor{}))
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer w.Stop()

	err := w.Start(context.Background())
	if err == nil || !errors.Is(err, errors.New("skills: watcher already started")) {
		// errors.Is on errors.New comparison returns false; we
		// settle for a non-nil error here (the exact match is
		// caller's choice).
		if err == nil {
			t.Fatalf("second Start: expected error, got nil")
		}
	}
}
