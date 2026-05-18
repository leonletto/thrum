//go:build integration

// Package skills mirror_lifecycle_test exercises the real fsnotify
// → mirror.Worker → on-disk apply pipeline against a temp repo + temp
// worktree. Unit tests in internal/skills/mirror/worker_test.go and
// internal/skills/watcher_test.go drive the components in isolation
// with fake channels and synthetic events; this file proves the
// kernel-mediated seam:
//
//   - A canonical SKILL.md write under .thrum/skills/<name>/ surfaces
//     to <worktree>/.claude/skills/<name>/SKILL.md within the spec'd
//     end-to-end deadline (spec §19 E9 AC #1: "within 1 s" — the test
//     uses 2s + Eventually to absorb CI variance per coordinator-
//     approved scoping).
//   - An out-of-band canonical change while the Worker was stopped
//     converges on Worker.Reconcile after restart (spec §12.3.1 +
//     plan E9.4 daemon-restart reconcile contract).
//
// Tests are race-safe and goleak-instrumented (Worker spawns one
// goroutine per destination; Watcher spawns one fsnotify run loop).
package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/leonletto/thrum/internal/skills"
	"github.com/leonletto/thrum/internal/skills/mirror"
)

// mirrorTestStubs holds the no-op collaborators the watcher requires
// to be non-nil. Mirror lifecycle tests don't exercise the proposal /
// supervisor / chain paths — those are owned by promote_flow_test.go
// and staleness_integration_test.go.
type mirrorTestStubs struct{}

func (mirrorTestStubs) SendSupervisorMessage(_ context.Context, _, _, _ string) error {
	return nil
}

func (mirrorTestStubs) MintProposalReminder(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (mirrorTestStubs) CancelProposalReminder(_ context.Context, _ string) error {
	return nil
}

func fixedResolver(agents []string) skills.ChainResolver {
	return func(_ context.Context) ([]string, error) {
		return agents, nil
	}
}

// mirrorFixture owns the temp directories + the real Worker + Watcher.
// Caller obtains it with newMirrorFixture, then drives canonical
// changes against fixture.libraryRoot — the real fsnotify + Worker
// pipeline replicates them into <worktree>/.claude/skills/.
type mirrorFixture struct {
	t            *testing.T
	repoRoot     string
	worktreeRoot string
	libraryRoot  string // <repoRoot>/.thrum/skills
	proposalRoot string // <repoRoot>/.thrum/agents
	mirrorDir    string // <worktreeRoot>/.claude/skills
	worker       *mirror.Worker
	watcher      *skills.Watcher
}

func newMirrorFixture(t *testing.T) *mirrorFixture {
	t.Helper()
	repoRoot := t.TempDir()
	worktreeRoot := t.TempDir()
	libraryRoot := filepath.Join(repoRoot, ".thrum", "skills")
	proposalRoot := filepath.Join(repoRoot, ".thrum", "agents")
	mirrorDir := filepath.Join(worktreeRoot, ".claude", "skills")

	mustMkdir(t, libraryRoot)
	mustMkdir(t, proposalRoot)
	mustMkdir(t, filepath.Join(worktreeRoot, ".claude"))

	worker := mirror.New(mirror.WorkerOpts{
		SourceRoot: libraryRoot,
		Destinations: []mirror.Destination{
			{WorktreePath: worktreeRoot, Runtime: "claude"},
		},
		Debounce:    100 * time.Millisecond, // tight so the test completes fast
		StopTimeout: 2 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Worker.Start: %v", err)
	}
	t.Cleanup(func() {
		if err := worker.Stop(); err != nil {
			t.Errorf("Worker.Stop: %v", err)
		}
	})

	stubs := mirrorTestStubs{}
	watcher := skills.NewWatcher(skills.WatcherOpts{
		LibraryRoot:  libraryRoot,
		ProposalRoot: proposalRoot,
		Worktrees:    []string{worktreeRoot},
		Mirror:       worker,
		Staleness:    stubs,
		Supervisor:   stubs,
		Resolver:     fixedResolver([]string{"@coordinator_main"}),
	})

	if err := watcher.Start(ctx); err != nil {
		t.Fatalf("Watcher.Start: %v", err)
	}
	t.Cleanup(func() {
		if err := watcher.Stop(); err != nil {
			t.Errorf("Watcher.Stop: %v", err)
		}
	})

	return &mirrorFixture{
		t:            t,
		repoRoot:     repoRoot,
		worktreeRoot: worktreeRoot,
		libraryRoot:  libraryRoot,
		proposalRoot: proposalRoot,
		mirrorDir:    mirrorDir,
		worker:       worker,
		watcher:      watcher,
	}
}

// writeCanonicalSkill creates a SKILL.md at .thrum/skills/<name>/SKILL.md
// with the supplied YAML frontmatter and body. Returns the absolute
// canonical path.
func (f *mirrorFixture) writeCanonicalSkill(name, fmYAML, body string) string {
	f.t.Helper()
	dir := filepath.Join(f.libraryRoot, name)
	mustMkdir(f.t, dir)
	abs := filepath.Join(dir, "SKILL.md")
	content := "---\n" + fmYAML + "\n---\n" + body + "\n"
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		f.t.Fatalf("write canonical SKILL.md: %v", err)
	}
	return abs
}

// mirrorPath returns the expected mirror location for the supplied
// canonical skill name under this fixture's worktree.
func (f *mirrorFixture) mirrorPath(name string) string {
	return filepath.Join(f.mirrorDir, name, "SKILL.md")
}

func TestMirrorLifecycle_CanonicalWriteAppearsInMirror(t *testing.T) {
	// goleak registered via t.Cleanup so it runs AFTER the fixture's
	// Worker.Stop / Watcher.Stop cleanups (defer runs before t.Cleanup;
	// LIFO within t.Cleanup means goleak — registered first — runs last).
	t.Cleanup(func() {
		goleak.VerifyNone(t,
			goleak.IgnoreTopFunction("github.com/fsnotify/fsnotify.(*Watcher).readEvents"),
		)
	})
	f := newMirrorFixture(t)

	// Drop a canonical SKILL.md. The Watcher (real fsnotify) must see
	// the Create, enqueue to the Worker, which must apply within the
	// debounce + apply window.
	f.writeCanonicalSkill("widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  promoted_by: '@coord'\n  trigger_reason: 'integration test'",
		"WIDGET BODY V1")

	expected := f.mirrorPath("widget")
	eventually(t, 2*time.Second, 50*time.Millisecond, func() bool {
		data, err := os.ReadFile(expected)
		if err != nil {
			return false
		}
		return strings.Contains(string(data), "WIDGET BODY V1")
	}, "mirror at %s did not appear with V1 body within deadline", expected)
}

func TestMirrorLifecycle_CanonicalUpdatePropagates(t *testing.T) {
	t.Cleanup(func() {
		goleak.VerifyNone(t,
			goleak.IgnoreTopFunction("github.com/fsnotify/fsnotify.(*Watcher).readEvents"),
		)
	})
	f := newMirrorFixture(t)

	abs := f.writeCanonicalSkill("widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  promoted_by: '@coord'\n  trigger_reason: 'integration test'",
		"WIDGET BODY V1")
	expected := f.mirrorPath("widget")
	eventually(t, 2*time.Second, 50*time.Millisecond, func() bool {
		data, err := os.ReadFile(expected)
		return err == nil && strings.Contains(string(data), "WIDGET BODY V1")
	}, "V1 mirror did not appear")

	// Modify the canonical file. Watcher emits Update; Worker re-applies
	// the (debounced) new content.
	content := "---\nname: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  promoted_by: '@coord'\n  trigger_reason: 'integration test'\n---\nWIDGET BODY V2 UPDATED\n"
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		t.Fatalf("rewrite canonical: %v", err)
	}

	eventually(t, 2*time.Second, 50*time.Millisecond, func() bool {
		data, err := os.ReadFile(expected)
		return err == nil && strings.Contains(string(data), "WIDGET BODY V2 UPDATED")
	}, "mirror did not update to V2 body within deadline")
}

func TestMirrorLifecycle_RestartReconcileConverges(t *testing.T) {
	// Restart-reconcile uses Worker only — no Watcher. This simulates
	// a daemon restart where fsnotify events for the out-of-band change
	// never fire (the daemon was down when the change landed).
	t.Cleanup(func() { goleak.VerifyNone(t) })

	repoRoot := t.TempDir()
	worktreeRoot := t.TempDir()
	libraryRoot := filepath.Join(repoRoot, ".thrum", "skills")
	mirrorDir := filepath.Join(worktreeRoot, ".claude", "skills")
	mustMkdir(t, libraryRoot)
	mustMkdir(t, filepath.Join(worktreeRoot, ".claude"))

	// Phase 1: a canonical skill exists when the daemon was last up.
	// Simulate the pre-restart state by pre-populating the mirror to
	// match.
	mustMkdir(t, filepath.Join(libraryRoot, "alpha"))
	if err := os.WriteFile(filepath.Join(libraryRoot, "alpha", "SKILL.md"),
		[]byte("---\nname: alpha\ndescription: a\nthrum:\n  proposed_by: '@a'\n  promoted_by: '@c'\n  trigger_reason: 't'\n---\nALPHA\n"), 0o600); err != nil {
		t.Fatalf("write canonical alpha: %v", err)
	}
	mustMkdir(t, filepath.Join(mirrorDir, "alpha"))
	if err := os.WriteFile(filepath.Join(mirrorDir, "alpha", "SKILL.md"),
		[]byte("---\nname: alpha\ndescription: a\n---\nALPHA-STALE\n"), 0o600); err != nil {
		t.Fatalf("seed stale mirror: %v", err)
	}

	// Out-of-band change: another canonical skill landed while the
	// daemon was down. The mirror doesn't know about it yet.
	mustMkdir(t, filepath.Join(libraryRoot, "beta"))
	if err := os.WriteFile(filepath.Join(libraryRoot, "beta", "SKILL.md"),
		[]byte("---\nname: beta\ndescription: b\nthrum:\n  proposed_by: '@a'\n  promoted_by: '@c'\n  trigger_reason: 't'\n---\nBETA\n"), 0o600); err != nil {
		t.Fatalf("write canonical beta: %v", err)
	}

	// Phase 2: daemon restart. Build a fresh Worker, Start, Reconcile.
	worker := mirror.New(mirror.WorkerOpts{
		SourceRoot: libraryRoot,
		Destinations: []mirror.Destination{
			{WorktreePath: worktreeRoot, Runtime: "claude"},
		},
		Debounce:    100 * time.Millisecond,
		StopTimeout: 2 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Worker.Start: %v", err)
	}
	defer func() {
		if err := worker.Stop(); err != nil {
			t.Errorf("Worker.Stop: %v", err)
		}
	}()

	// Reconcile is the contract that drift correction relies on at boot.
	if err := worker.Reconcile(ctx); err != nil {
		t.Fatalf("Worker.Reconcile: %v", err)
	}

	// beta must now be in the mirror.
	betaMirror := filepath.Join(mirrorDir, "beta", "SKILL.md")
	eventually(t, 2*time.Second, 50*time.Millisecond, func() bool {
		data, err := os.ReadFile(betaMirror)
		return err == nil && strings.Contains(string(data), "BETA")
	}, "beta mirror did not converge after Reconcile")

	// alpha must have been refreshed from canonical (the stale
	// "ALPHA-STALE" body must be gone).
	alphaMirror := filepath.Join(mirrorDir, "alpha", "SKILL.md")
	eventually(t, 2*time.Second, 50*time.Millisecond, func() bool {
		data, err := os.ReadFile(alphaMirror)
		if err != nil {
			return false
		}
		return strings.Contains(string(data), "ALPHA") && !strings.Contains(string(data), "ALPHA-STALE")
	}, "alpha mirror did not refresh from canonical after Reconcile")
}

// --- helpers ---

// eventually polls fn until it returns true or the deadline elapses.
// Modeled on testify's assert.Eventually without the testify dep —
// keeps the integration suite from pulling a fixture-only dependency.
// On timeout, fails the test with the supplied formatted message.
func eventually(t *testing.T, timeout, interval time.Duration, fn func() bool, msg string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(interval)
	}
	if !fn() {
		t.Fatalf(msg, args...)
	}
}

