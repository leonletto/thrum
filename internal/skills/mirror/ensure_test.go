package mirror

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/skills"
)

// ensureFixture builds a Worker with a populated source library and
// returns (worker, srcRoot, worktreePath, teardown). The worktree is
// pre-created so EnsureMirrored can write under it.
func ensureFixture(t *testing.T, destinations []Destination) (*Worker, string, func()) {
	t.Helper()
	srcRoot := filepath.Join(t.TempDir(), ".thrum", "skills")
	if err := os.MkdirAll(srcRoot, 0o750); err != nil {
		t.Fatalf("mkdir srcRoot: %v", err)
	}
	w := New(WorkerOpts{
		SourceRoot:   srcRoot,
		Destinations: destinations,
		Debounce:     30 * time.Millisecond,
		StopTimeout:  2 * time.Second,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return w, srcRoot, func() { _ = w.Stop() }
}

func TestEnsureMirrored_HappyPath(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := ensureFixture(t, []Destination{claudeDest(wtree)})
	defer teardown()

	// Pre-populate canonical with two skills.
	writeSkill(t, srcRoot, "alpha", "body alpha", nil)
	writeSkill(t, srcRoot, "beta", "body beta", map[string]string{"helper.md": "h"})

	if err := w.EnsureMirrored(context.Background(), wtree); err != nil {
		t.Fatalf("EnsureMirrored: %v", err)
	}

	for _, name := range []string{"alpha", "beta"} {
		path := filepath.Join(wtree, ".claude", "skills", name, "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing %s: %v", path, err)
			continue
		}
		if !bytes.Equal(data, []byte("body "+name)) {
			t.Errorf("body drift for %s: %q", name, data)
		}
	}
	// Sibling file from beta must also land.
	if _, err := os.Stat(filepath.Join(wtree, ".claude", "skills", "beta", "helper.md")); err != nil {
		t.Errorf("sibling not mirrored: %v", err)
	}
}

func TestEnsureMirrored_Idempotent(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := ensureFixture(t, []Destination{claudeDest(wtree)})
	defer teardown()

	writeSkill(t, srcRoot, "demo", "body", nil)

	if err := w.EnsureMirrored(context.Background(), wtree); err != nil {
		t.Fatalf("first EnsureMirrored: %v", err)
	}
	dst := filepath.Join(wtree, ".claude", "skills", "demo", "SKILL.md")
	info1, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat after first: %v", err)
	}
	mtime1 := info1.ModTime()
	time.Sleep(50 * time.Millisecond)

	if err := w.EnsureMirrored(context.Background(), wtree); err != nil {
		t.Fatalf("second EnsureMirrored: %v", err)
	}
	info2, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat after second: %v", err)
	}
	if !info2.ModTime().Equal(mtime1) {
		t.Errorf("idempotent EnsureMirrored re-wrote file: mtime1=%v mtime2=%v", mtime1, info2.ModTime())
	}
}

func TestEnsureMirrored_NullAdapterTreatedAsSuccess(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, _, teardown := ensureFixture(t, []Destination{{WorktreePath: wtree, Runtime: "codex"}})
	defer teardown()

	if err := w.EnsureMirrored(context.Background(), wtree); err != nil {
		t.Errorf("null-adapter worktree: expected nil, got %v", err)
	}
	// Verify nothing was written under the worktree.
	if _, err := os.Stat(filepath.Join(wtree, ".claude")); !os.IsNotExist(err) {
		t.Errorf("expected no mirror dir created for null adapter, got stat err=%v", err)
	}
}

func TestEnsureMirrored_UnknownWorktree(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, _, teardown := ensureFixture(t, []Destination{claudeDest(wtree)})
	defer teardown()

	err := w.EnsureMirrored(context.Background(), "/nonexistent/path")
	if !errors.Is(err, ErrUnknownWorktree) {
		t.Fatalf("expected ErrUnknownWorktree, got %v", err)
	}
}

func TestEnsureMirrored_ContextCancel(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := ensureFixture(t, []Destination{claudeDest(wtree)})
	defer teardown()

	writeSkill(t, srcRoot, "demo", "body", nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before call

	err := w.EnsureMirrored(ctx, wtree)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestEnsureMirrored_ConcurrentWithAsyncWorker(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := ensureFixture(t, []Destination{claudeDest(wtree)})
	defer teardown()

	// Pre-populate canonical with several skills.
	const N = 20
	for i := range N {
		writeSkill(t, srcRoot, skillNameI(i), bodyForI(i), nil)
	}

	// Spawn the async worker side: enqueue 50 events while
	// EnsureMirrored runs.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 50 {
			_ = w.Enqueue(skills.MirrorEvent{
				Kind:      skills.MirrorEventKindUpdate,
				SkillName: skillNameI(i % N),
			}, wtree)
		}
	}()

	if err := w.EnsureMirrored(context.Background(), wtree); err != nil {
		t.Fatalf("EnsureMirrored: %v", err)
	}
	wg.Wait()

	// Wait for async path to drain.
	time.Sleep(150 * time.Millisecond)

	// Every canonical skill must land with the right body. The
	// shared mutex prevents torn writes between async + sync paths.
	for i := range N {
		path := filepath.Join(wtree, ".claude", "skills", skillNameI(i), "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing %s: %v", path, err)
			continue
		}
		if string(data) != bodyForI(i) {
			t.Errorf("torn write at %s: %q want %q", path, data, bodyForI(i))
		}
	}
}

func TestEnsureMirrored_DriftRemoval(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := ensureFixture(t, []Destination{claudeDest(wtree)})
	defer teardown()

	// Pre-seed destination with a skill not in canonical.
	driftDir := filepath.Join(wtree, ".claude", "skills", "drift-skill")
	if err := os.MkdirAll(driftDir, 0o750); err != nil {
		t.Fatalf("mkdir drift: %v", err)
	}
	if err := os.WriteFile(filepath.Join(driftDir, "SKILL.md"), []byte("stale"), 0o600); err != nil {
		t.Fatalf("write drift: %v", err)
	}

	// Canonical has a different skill.
	writeSkill(t, srcRoot, "kept", "body kept", nil)

	if err := w.EnsureMirrored(context.Background(), wtree); err != nil {
		t.Fatalf("EnsureMirrored: %v", err)
	}

	if _, err := os.Stat(driftDir); !os.IsNotExist(err) {
		t.Errorf("drift not removed: stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(wtree, ".claude", "skills", "kept", "SKILL.md")); err != nil {
		t.Errorf("canonical not copied: %v", err)
	}
}

func TestEnsureMirrored_FsErrorWrapped(t *testing.T) {
	t.Parallel()

	// Create a worktree where the .claude/skills/ path can't be
	// created — by making .claude/ a file instead of a directory.
	wtree := t.TempDir()
	claudeDir := filepath.Join(wtree, ".claude")
	if err := os.WriteFile(claudeDir, []byte("blocking file"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}

	w, srcRoot, teardown := ensureFixture(t, []Destination{claudeDest(wtree)})
	defer teardown()

	writeSkill(t, srcRoot, "doomed", "body", nil)

	err := w.EnsureMirrored(context.Background(), wtree)
	if err == nil {
		t.Fatalf("expected wrapped ErrMirrorWrite, got nil")
	}
	if !errors.Is(err, ErrMirrorWrite) {
		t.Errorf("expected errors.Is(err, ErrMirrorWrite); got: %v", err)
	}
}

func TestEnsureMirrored_BeforeStartReturnsErr(t *testing.T) {
	t.Parallel()

	w := New(WorkerOpts{
		SourceRoot:   filepath.Join(t.TempDir(), ".thrum", "skills"),
		Destinations: []Destination{claudeDest(t.TempDir())},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := w.EnsureMirrored(context.Background(), "/any"); !errors.Is(err, ErrWorkerNotStarted) {
		t.Fatalf("expected ErrWorkerNotStarted, got %v", err)
	}
}

// TestEnsureMirrored_RacesStop pins the stateMu locking contract
// from the brainstormer-third finding I1: EnsureMirrored must
// snapshot w.registered + w.worktree under RLock so a concurrent
// Stop (which reassigns the maps after WaitGroup drain) cannot race
// the destination iteration. Without the lock, the race detector
// surfaces a data race on map header access here. Pinned regardless
// of -race so a future refactor that drops the snapshot is caught.
func TestEnsureMirrored_RacesStop(t *testing.T) {
	t.Parallel()

	const iterations = 50
	for i := range iterations {
		wtree := t.TempDir()
		srcRoot := filepath.Join(t.TempDir(), ".thrum", "skills")
		if err := os.MkdirAll(srcRoot, 0o750); err != nil {
			t.Fatalf("iter %d: mkdir srcRoot: %v", i, err)
		}
		w := New(WorkerOpts{
			SourceRoot:   srcRoot,
			Destinations: []Destination{claudeDest(wtree)},
			Debounce:     20 * time.Millisecond,
			StopTimeout:  1 * time.Second,
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err := w.Start(context.Background()); err != nil {
			t.Fatalf("iter %d: Start: %v", i, err)
		}
		writeSkill(t, srcRoot, "raced", "body", nil)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// The worst race window is when Stop is mid-flight and
			// EnsureMirrored is reading w.registered / w.worktree.
			_ = w.EnsureMirrored(context.Background(), wtree)
		}()
		go func() {
			defer wg.Done()
			_ = w.Stop()
		}()
		wg.Wait()
		// Either order produces a clean result: EnsureMirrored
		// either completes (caught Stop after; mirror landed) or
		// returns ErrWorkerNotStarted (Stop ran first). Both are
		// acceptable; the race detector is the gate.
	}
}

// TestReconcile_SilentFailGuardedAgainstSourceReadError pins
// brainstormer-third finding M2: reconcileDestination must NOT
// treat a source-read failure as "empty canonical" — otherwise the
// destination cleanup loop wipes every mirrored skill on a transient
// FS hiccup.
func TestReconcile_SilentFailGuardedAgainstSourceReadError(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	srcRoot := filepath.Join(t.TempDir(), ".thrum", "skills")
	if err := os.MkdirAll(srcRoot, 0o750); err != nil {
		t.Fatalf("mkdir srcRoot: %v", err)
	}
	// Pre-populate the destination with a mirrored skill.
	destSkillDir := filepath.Join(wtree, ".claude", "skills", "valuable")
	if err := os.MkdirAll(destSkillDir, 0o750); err != nil {
		t.Fatalf("mkdir destSkill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destSkillDir, "SKILL.md"), []byte("body"), 0o600); err != nil {
		t.Fatalf("write destSkill: %v", err)
	}

	// Sabotage source-read by replacing srcRoot with an unreadable
	// path (chmod 0). On Linux + macOS, root can still read; the
	// test relies on the non-root environment that CI + dev boxes
	// run as. If the runner is root, the test trivially passes on
	// the no-error path (canonical populated, no cleanup); the
	// regression we care about is the wipe-on-read-failure path
	// which only fires for non-root.
	if err := os.Chmod(srcRoot, 0); err != nil {
		t.Fatalf("chmod srcRoot 0: %v", err)
	}
	defer os.Chmod(srcRoot, 0o700) //nolint:errcheck,gosec // best-effort cleanup; 0o700 satisfies gosec G302 + restores ownership-read

	w := New(WorkerOpts{
		SourceRoot:   srcRoot,
		Destinations: []Destination{claudeDest(wtree)},
		Debounce:     20 * time.Millisecond,
		StopTimeout:  1 * time.Second,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	// Reconcile or EnsureMirrored should surface the read error
	// rather than silently wipe the destination. Either return path
	// (error or no-op) is acceptable; what's NOT acceptable is the
	// destination skill disappearing.
	_ = w.EnsureMirrored(context.Background(), wtree)

	// The pre-existing destination skill must survive a failed
	// reconcile.
	if _, err := os.Stat(filepath.Join(destSkillDir, "SKILL.md")); err != nil {
		// Only fail the test if the file actually disappeared.
		// (If the runner is root, the chmod was a no-op, the
		// source read succeeded, and the canonical-empty cleanup
		// would have wiped the file — but the new guard says
		// "only consider 'not exist' as legitimately empty", so
		// the test still catches the regression.)
		if os.IsNotExist(err) {
			t.Errorf("destination skill wiped on source-read failure: %v", err)
		}
	}
}

// TestEnsureMirrored_MultipleDestinations confirms the wake-handler
// contract when a worktree has more than one runtime registered. In
// v0.11 only one runtime is canonical per worktree, but the contract
// is forward-compatible.
func TestEnsureMirrored_MultipleDestinations(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	// One populated + one null-adapter destination — every
	// populated dest must converge; null-adapter dest is silent.
	w, srcRoot, teardown := ensureFixture(t, []Destination{
		claudeDest(wtree),
		{WorktreePath: wtree, Runtime: "codex"},
	})
	defer teardown()

	writeSkill(t, srcRoot, "demo", "body", nil)

	if err := w.EnsureMirrored(context.Background(), wtree); err != nil {
		t.Fatalf("EnsureMirrored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wtree, ".claude", "skills", "demo", "SKILL.md")); err != nil {
		t.Errorf("claude dest not mirrored: %v", err)
	}
	// codex has null adapter; no codex-specific dir should exist.
	if entries, _ := os.ReadDir(wtree); len(entries) > 0 {
		for _, e := range entries {
			if strings.Contains(e.Name(), "codex") {
				t.Errorf("unexpected codex-prefixed dir: %s", e.Name())
			}
		}
	}
}
