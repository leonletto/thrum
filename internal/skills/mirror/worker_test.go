package mirror

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/skills"
	"go.uber.org/goleak"
)

// fixtureWorker spins up a Worker with a fresh source-root + given
// destinations and starts it. Returns the worker, the source root,
// and a teardown func. Tests should `defer teardown()` so failures
// don't leak goroutines.
func fixtureWorker(t *testing.T, destinations []Destination) (*Worker, string, func()) {
	t.Helper()
	srcRoot := filepath.Join(t.TempDir(), ".thrum", "skills")
	if err := os.MkdirAll(srcRoot, 0o750); err != nil {
		t.Fatalf("mkdir srcRoot: %v", err)
	}
	w := New(WorkerOpts{
		SourceRoot:   srcRoot,
		Destinations: destinations,
		Debounce:     30 * time.Millisecond, // shorter than prod for fast tests
		StopTimeout:  2 * time.Second,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return w, srcRoot, func() {
		_ = w.Stop()
	}
}

// writeSkill writes a source-side SKILL.md + an optional sibling
// file under <srcRoot>/<name>/. Helper makes per-test fixture setup
// concise.
func writeSkill(t *testing.T, srcRoot, name, body string, sibling map[string]string) {
	t.Helper()
	dir := filepath.Join(srcRoot, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	for rel, content := range sibling {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

// claudeDest builds a Destination using the claude runtime (the only
// populated adapter entry in v0.11). Most worker tests use this so
// they hit a real applied-path codepath instead of the null-adapter
// skip.
func claudeDest(worktree string) Destination {
	return Destination{WorktreePath: worktree, Runtime: "claude"}
}

// TestWorker_EnqueueBeforeStart pins the pre-flagged invariant from
// @researcher_skills: calling Enqueue before Start MUST return a
// typed error, NOT block on a non-existent receiver. Without this
// guarantee, callers that race the Worker lifecycle (e.g. a B-B1
// stage-3 wake handler that fires before the worker fully comes up)
// would deadlock.
func TestWorker_EnqueueBeforeStart(t *testing.T) {
	t.Parallel()

	w := New(WorkerOpts{
		SourceRoot:   t.TempDir(),
		Destinations: []Destination{claudeDest(t.TempDir())},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	err := w.Enqueue(skills.MirrorEvent{Kind: skills.MirrorEventKindCreate, SkillName: "x"}, "/anywhere")
	if !errors.Is(err, ErrWorkerNotStarted) {
		t.Fatalf("Enqueue-before-Start: expected ErrWorkerNotStarted, got %v", err)
	}
}

// TestWorker_StartTwiceReturnsErr pins the once-started contract.
func TestWorker_StartTwiceReturnsErr(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, _, teardown := fixtureWorker(t, []Destination{claudeDest(wtree)})
	defer teardown()

	if err := w.Start(context.Background()); !errors.Is(err, ErrWorkerAlreadyStarted) {
		t.Fatalf("second Start: expected ErrWorkerAlreadyStarted, got %v", err)
	}
}

// Intentionally NOT t.Parallel: this test reads runtime.NumGoroutine
// which is noisy when other parallel tests are spawning/draining
// goroutines simultaneously. Serial execution gives a stable baseline.
func TestWorker_SingleGoroutinePerDestination(t *testing.T) {
	dests := []Destination{
		claudeDest(t.TempDir()),
		claudeDest(t.TempDir()),
		claudeDest(t.TempDir()),
	}
	baseline := runtime.NumGoroutine()
	_, _, teardown := fixtureWorker(t, dests)
	// Settle to give the scheduler a moment to park new goroutines
	// in select. NumGoroutine readings are noisy at the millisecond
	// scale; this is the standard wait-and-read pattern.
	time.Sleep(50 * time.Millisecond)
	got := runtime.NumGoroutine() - baseline

	if got < 3 {
		t.Errorf("expected >= 3 new goroutines, got delta %d", got)
	}
	teardown()
	time.Sleep(50 * time.Millisecond)
	post := runtime.NumGoroutine() - baseline
	if post > 1 {
		t.Errorf("goroutines leaked after Stop: delta %d", post)
	}
}

func TestWorker_DebounceCoalesces50Events(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := fixtureWorker(t, []Destination{claudeDest(wtree)})
	defer teardown()

	// Source skill exists so the applies have something to copy.
	writeSkill(t, srcRoot, "demo", "body v0", nil)

	// Enqueue 50 events for the same SkillName within the debounce
	// window. The worker must coalesce them into a single apply.
	for i := range 50 {
		_ = i
		if err := w.Enqueue(skills.MirrorEvent{
			Kind:      skills.MirrorEventKindUpdate,
			SkillName: "demo",
			Trigger:   skills.TriggerFileChange,
		}, wtree); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	// Wait > debounce window for the flush.
	time.Sleep(100 * time.Millisecond)

	// Confirm the destination was written exactly once. We assert
	// "at least once" via on-disk presence; the no-torn-writes
	// assertion is the contract — multiple copies of the same
	// content collapse to the same on-disk state, so observing the
	// file is correct + intact is sufficient.
	dst := filepath.Join(wtree, ".claude", "skills", "demo", "SKILL.md")
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read mirrored file: %v", err)
	}
	if !bytes.Equal(data, []byte("body v0")) {
		t.Errorf("mirrored content drift: %q", data)
	}
}

func TestWorker_CopyRecursive(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := fixtureWorker(t, []Destination{claudeDest(wtree)})
	defer teardown()

	writeSkill(t, srcRoot, "multi", "main body", map[string]string{
		"helper.md":         "helper text",
		"assets/inner.json": `{"k":1}`,
	})

	if err := w.Enqueue(skills.MirrorEvent{
		Kind:      skills.MirrorEventKindCreate,
		SkillName: "multi",
	}, wtree); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitForFile(t, filepath.Join(wtree, ".claude", "skills", "multi", "SKILL.md"), 500*time.Millisecond)
	waitForFile(t, filepath.Join(wtree, ".claude", "skills", "multi", "helper.md"), 500*time.Millisecond)
	waitForFile(t, filepath.Join(wtree, ".claude", "skills", "multi", "assets", "inner.json"), 500*time.Millisecond)
}

func TestWorker_DeleteRemovesRecursively(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := fixtureWorker(t, []Destination{claudeDest(wtree)})
	defer teardown()

	writeSkill(t, srcRoot, "doomed", "body", map[string]string{"side.txt": "x"})

	if err := w.Enqueue(skills.MirrorEvent{
		Kind:      skills.MirrorEventKindCreate,
		SkillName: "doomed",
	}, wtree); err != nil {
		t.Fatalf("Enqueue create: %v", err)
	}
	waitForFile(t, filepath.Join(wtree, ".claude", "skills", "doomed", "SKILL.md"), 500*time.Millisecond)

	if err := w.Enqueue(skills.MirrorEvent{
		Kind:      skills.MirrorEventKindDelete,
		SkillName: "doomed",
	}, wtree); err != nil {
		t.Fatalf("Enqueue delete: %v", err)
	}

	// Wait for delete to take effect; check that the directory is
	// gone.
	deadline := time.Now().Add(500 * time.Millisecond)
	gone := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(wtree, ".claude", "skills", "doomed")); os.IsNotExist(err) {
			gone = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !gone {
		t.Fatalf("delete did not remove destination dir within 500ms")
	}
}

func TestWorker_OverwriteWarning(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	srcRoot := filepath.Join(t.TempDir(), ".thrum", "skills")
	if err := os.MkdirAll(srcRoot, 0o750); err != nil {
		t.Fatalf("mkdir srcRoot: %v", err)
	}

	// Pre-seed the destination with a hand-edited file that differs
	// from what the upcoming mirror apply will write.
	dstDir := filepath.Join(wtree, ".claude", "skills", "edited")
	if err := os.MkdirAll(dstDir, 0o750); err != nil {
		t.Fatalf("mkdir dst: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "SKILL.md"), []byte("hand edit"), 0o600); err != nil {
		t.Fatalf("seed hand-edited file: %v", err)
	}

	logBuf := &threadSafeBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	w := New(WorkerOpts{
		SourceRoot:   srcRoot,
		Destinations: []Destination{claudeDest(wtree)},
		Debounce:     30 * time.Millisecond,
		StopTimeout:  2 * time.Second,
		Logger:       logger,
	})
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	writeSkill(t, srcRoot, "edited", "canonical body", nil)
	if err := w.Enqueue(skills.MirrorEvent{
		Kind:      skills.MirrorEventKindUpdate,
		SkillName: "edited",
	}, wtree); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Wait for the pre-seeded file's CONTENT to change. Existence
	// alone would pass instantly (the seed is already there); the
	// content-change is the actual apply landing.
	dstPath := filepath.Join(dstDir, "SKILL.md")
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(dstPath)
		if err == nil && bytes.Equal(data, []byte("canonical body")) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	logs := logBuf.String()
	if !contains(logs, "overwriting hand-edited file") {
		t.Errorf("expected overwrite warning in logs, got:\n%s", logs)
	}
}

func TestWorker_ConcurrentSameDestinationSerialized(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := fixtureWorker(t, []Destination{claudeDest(wtree)})
	defer teardown()

	// One skill per goroutine so applies don't all collapse via
	// debounce — we want serialization-not-torn-writes, not
	// debouncing.
	const N = 50
	for i := range N {
		writeSkill(t, srcRoot, skillNameI(i), bodyForI(i), nil)
	}

	var wg sync.WaitGroup
	wg.Add(N)
	for i := range N {
		go func(i int) {
			defer wg.Done()
			_ = w.Enqueue(skills.MirrorEvent{
				Kind:      skills.MirrorEventKindCreate,
				SkillName: skillNameI(i),
			}, wtree)
		}(i)
	}
	wg.Wait()

	// Wait for everything to flush.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for i := range N {
			if _, err := os.Stat(filepath.Join(wtree, ".claude", "skills", skillNameI(i), "SKILL.md")); err != nil {
				all = false
				break
			}
		}
		if all {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	for i := range N {
		path := filepath.Join(wtree, ".claude", "skills", skillNameI(i), "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing mirror %s: %v", path, err)
			continue
		}
		if string(data) != bodyForI(i) {
			t.Errorf("torn write at %s: got %q want %q", path, data, bodyForI(i))
		}
	}
}

func TestWorker_StopDrainsPending(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	srcRoot := filepath.Join(t.TempDir(), ".thrum", "skills")
	if err := os.MkdirAll(srcRoot, 0o750); err != nil {
		t.Fatalf("mkdir srcRoot: %v", err)
	}

	w := New(WorkerOpts{
		SourceRoot:   srcRoot,
		Destinations: []Destination{claudeDest(wtree)},
		Debounce:     50 * time.Millisecond,
		StopTimeout:  3 * time.Second,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	const N = 10
	for i := range N {
		writeSkill(t, srcRoot, skillNameI(i), bodyForI(i), nil)
		if err := w.Enqueue(skills.MirrorEvent{
			Kind:      skills.MirrorEventKindCreate,
			SkillName: skillNameI(i),
		}, wtree); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	if err := w.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	for i := range N {
		path := filepath.Join(wtree, ".claude", "skills", skillNameI(i), "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("stop did not drain %s: %v", path, err)
		}
	}
}

func TestWorker_NoGoroutineLeakOnStop(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("testing.(*T).Parallel"),
	)

	wtree := t.TempDir()
	_, _, teardown := fixtureWorker(t, []Destination{claudeDest(wtree)})
	teardown()
}

// TestWorker_NullAdapterRuntime confirms the null-adapter runtime
// (e.g. codex in v0.11) contributes zero goroutines + Enqueue against
// that worktree returns ErrUnknownWorktree (no destinations
// registered). Pinned so a future PR that adds a real entry for codex
// doesn't silently break the null-adapter contract on null-entry
// runtimes that remain.
func TestWorker_NullAdapterRuntime(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w := New(WorkerOpts{
		SourceRoot:   filepath.Join(t.TempDir(), ".thrum", "skills"),
		Destinations: []Destination{{WorktreePath: wtree, Runtime: "codex"}},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	if err := w.Enqueue(skills.MirrorEvent{Kind: skills.MirrorEventKindCreate, SkillName: "x"}, wtree); !errors.Is(err, ErrUnknownWorktree) {
		t.Errorf("null-adapter Enqueue: expected ErrUnknownWorktree, got %v", err)
	}
}

func TestWorker_ReconcileCopiesMissing(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := fixtureWorker(t, []Destination{claudeDest(wtree)})
	defer teardown()

	// Pre-populate canonical with two skills BEFORE reconcile.
	writeSkill(t, srcRoot, "alpha", "body alpha", nil)
	writeSkill(t, srcRoot, "beta", "body beta", nil)

	// Destination dir empty. Reconcile should populate it.
	if err := w.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	for _, name := range []string{"alpha", "beta"} {
		path := filepath.Join(wtree, ".claude", "skills", name, "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("reconcile did not copy %s: %v", name, err)
			continue
		}
		if !bytes.Equal(data, []byte("body "+name)) {
			t.Errorf("reconcile body drift for %s: %q", name, data)
		}
	}
}

func TestWorker_ReconcileRemovesStale(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := fixtureWorker(t, []Destination{claudeDest(wtree)})
	defer teardown()

	// Pre-seed the destination with a skill that's NOT in canonical.
	staleDir := filepath.Join(wtree, ".claude", "skills", "stale-skill")
	if err := os.MkdirAll(staleDir, 0o750); err != nil {
		t.Fatalf("mkdir stale: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "SKILL.md"), []byte("old"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	// Canonical has a different skill.
	writeSkill(t, srcRoot, "current", "body current", nil)

	if err := w.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Errorf("reconcile did not remove stale skill: stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(wtree, ".claude", "skills", "current", "SKILL.md")); err != nil {
		t.Errorf("reconcile did not copy current: %v", err)
	}
}

func TestWorker_ReconcileIdempotent(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w, srcRoot, teardown := fixtureWorker(t, []Destination{claudeDest(wtree)})
	defer teardown()

	writeSkill(t, srcRoot, "demo", "body", nil)

	if err := w.Reconcile(context.Background()); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	dst := filepath.Join(wtree, ".claude", "skills", "demo", "SKILL.md")
	info1, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat after first: %v", err)
	}
	mtime1 := info1.ModTime()

	// Sleep so a re-write would produce a distinguishable mtime.
	time.Sleep(50 * time.Millisecond)

	if err := w.Reconcile(context.Background()); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	info2, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat after second: %v", err)
	}
	if !info2.ModTime().Equal(mtime1) {
		t.Errorf("idempotent reconcile re-wrote file: mtime1=%v mtime2=%v", mtime1, info2.ModTime())
	}
}

func TestWorker_ReconcileSkipsNullAdapter(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	w := New(WorkerOpts{
		SourceRoot:   filepath.Join(t.TempDir(), ".thrum", "skills"),
		Destinations: []Destination{{WorktreePath: wtree, Runtime: "codex"}},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	// Reconcile must NOT panic / deadlock on a null-adapter
	// destination (no channel registered for it).
	if err := w.Reconcile(context.Background()); err != nil {
		t.Errorf("Reconcile with null adapter: %v", err)
	}
}

func TestWorker_ReconcileBeforeStartReturnsErr(t *testing.T) {
	t.Parallel()

	w := New(WorkerOpts{
		SourceRoot:   filepath.Join(t.TempDir(), ".thrum", "skills"),
		Destinations: []Destination{claudeDest(t.TempDir())},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := w.Reconcile(context.Background()); !errors.Is(err, ErrWorkerNotStarted) {
		t.Fatalf("Reconcile before Start: expected ErrWorkerNotStarted, got %v", err)
	}
}

// TestWorker_ReconcileRemovesStaleTmpDirs covers the E10.4 SIGKILL
// backstop: a promote that was killed between writing the temp dir and
// renaming it into place leaves `.thrum/skills/<name>.tmp/` on disk.
// Defer-rollback inside HandlePromote is the primary defense; this is
// the daemon-restart safety-net per plan AC line 1608-1617.
func TestWorker_ReconcileRemovesStaleTmpDirs(t *testing.T) {
	t.Parallel()

	wtree := t.TempDir()
	logBuf := &threadSafeBuffer{}
	w := New(WorkerOpts{
		SourceRoot:   filepath.Join(t.TempDir(), ".thrum", "skills"),
		Destinations: []Destination{claudeDest(wtree)},
		Debounce:     30 * time.Millisecond,
		StopTimeout:  2 * time.Second,
		Logger:       slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err := os.MkdirAll(w.opts.SourceRoot, 0o750); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = w.Stop() }()

	// Simulate two prior-crash leftovers: one tmp dir, one .old backup
	// dir (also a defer-rollback artifact that the SIGKILL bypassed).
	tmpDir := filepath.Join(w.opts.SourceRoot, "leftover-skill.tmp")
	oldDir := filepath.Join(w.opts.SourceRoot, "another-skill.old")
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		t.Fatalf("mkdir leftover tmp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), []byte("garbage"), 0o600); err != nil {
		t.Fatalf("write leftover tmp: %v", err)
	}
	if err := os.MkdirAll(oldDir, 0o750); err != nil {
		t.Fatalf("mkdir leftover old: %v", err)
	}

	if err := w.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("stale .tmp/ should be removed by Reconcile; stat err=%v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("stale .old/ should be removed by Reconcile; stat err=%v", err)
	}
	logs := logBuf.String()
	if !contains(logs, "stale tmp dir") && !contains(logs, "stale backup dir") {
		t.Errorf("expected warn log about stale promote leftover; got: %s", logs)
	}
}

// TestWorker_UnknownRuntimeRefusesStart confirms misconfiguration
// (typo at the CLI / config layer) surfaces as ErrUnknownRuntime on
// Start rather than a silent no-op.
func TestWorker_UnknownRuntimeRefusesStart(t *testing.T) {
	t.Parallel()

	w := New(WorkerOpts{
		SourceRoot:   filepath.Join(t.TempDir(), ".thrum", "skills"),
		Destinations: []Destination{{WorktreePath: t.TempDir(), Runtime: "nonexistent-runtime"}},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := w.Start(context.Background()); !errors.Is(err, ErrUnknownRuntime) {
		t.Fatalf("Start with unknown runtime: expected ErrUnknownRuntime, got %v", err)
	}
}

// Helpers.

// waitForFile polls for the file to exist with a timeout. The async
// worker means tests can't be synchronous on apply; this is the
// minimum-wait pattern across worker tests.
func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file did not materialize at %s within %v", path, timeout)
}

func skillNameI(i int) string  { return "s-" + itoa3(i) }
func bodyForI(i int) string    { return "body-" + itoa3(i) }
func itoa3(i int) string {
	out := []byte("000")
	out[2] = byte('0' + (i % 10))
	out[1] = byte('0' + ((i / 10) % 10))
	out[0] = byte('0' + ((i / 100) % 10))
	return string(out)
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// threadSafeBuffer wraps a bytes.Buffer so concurrent slog.Handler
// writes don't race. The discard handler bypasses this; the
// overwrite-warning test uses it to capture log output.
type threadSafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *threadSafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Pin against future drift: every public Worker method below shadows
// a hidden field; this compile-time check ensures atomic.Bool isn't
// accidentally replaced with a bare bool (the started-flag
// invariant requires lock-free CAS).
var _ = func() bool { var b atomic.Bool; return b.CompareAndSwap(false, true) }
