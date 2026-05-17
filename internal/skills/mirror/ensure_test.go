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
