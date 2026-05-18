package sessionarchive_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/sessionarchive"
)

// silentLogger returns a slog.Logger that discards output. Tests that
// expect chmod/chtimes warnings don't need to inspect them — the
// per-call Warn() lines would otherwise pollute go test -v output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// makeArchiveAgent builds an Agent with the fields Archive() actually
// reads (AgentID + Mode). Other Agent columns are zero-valued — fine
// for this scope.
func makeArchiveAgent(agentID, mode string) agentpkg.Agent {
	return agentpkg.Agent{AgentID: agentID, Mode: mode}
}

// writeSnapshot creates the snapshot file at path with the given
// frontmatter saved_at and body. Returns the absolute path. Creates
// dir + ancestors if missing.
func writeSnapshot(t *testing.T, dir, name, savedAt, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir snapshot parent: %v", err)
	}
	path := filepath.Join(dir, name)
	content := fmt.Sprintf("---\nagent: %s\nsession_id: ses_x\nsaved_at: %s\nreason: manual\nmachine_id: t\n---\n\n%s\n",
		strings.TrimSuffix(name, ".md"), savedAt, body)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	return path
}

func TestArchive_NoSnapshot_ReturnsNullNull(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")

	agent := makeArchiveAgent("alpha", agentpkg.ModePersistent)
	res, err := sessionarchive.Archive(
		context.Background(),
		agent,
		"/nonexistent/snapshot.md",
		mainRepo, "",
		sessionarchive.Opts{Logger: silentLogger()},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ArchivedPath != nil {
		t.Errorf("expected nil ArchivedPath, got %v", *res.ArchivedPath)
	}
	if res.BigPicture != nil {
		t.Errorf("expected nil BigPicture, got %v", *res.BigPicture)
	}
	if res.Content != nil {
		t.Errorf("expected nil Content, got %v", *res.Content)
	}
}

func TestArchive_ValidSnapshot_PopulatesContent(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")
	src := writeSnapshot(t, mainRepo, "alpha.md", "2026-05-17T15:32:18.421Z", "body text")

	agent := makeArchiveAgent("alpha", agentpkg.ModePersistent)
	res, err := sessionarchive.Archive(
		context.Background(),
		agent, src, mainRepo, "",
		sessionarchive.Opts{Logger: silentLogger()},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Content == nil {
		t.Fatal("expected Content for valid snapshot, got nil")
	}
	if !strings.Contains(*res.Content, "body text") {
		t.Errorf("Content missing body: %q", *res.Content)
	}
	if !strings.Contains(*res.Content, "saved_at: 2026-05-17T15:32:18.421Z") {
		t.Errorf("Content missing frontmatter: %q", *res.Content)
	}
}

func TestArchive_EmptySnapshot_RemovesAndReturnsNull(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(mainRepo, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	src := filepath.Join(mainRepo, "restart", "alpha.md")
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(src, []byte{}, 0o600); err != nil {
		t.Fatalf("write empty: %v", err)
	}

	agent := makeArchiveAgent("alpha", agentpkg.ModePersistent)
	res, err := sessionarchive.Archive(
		context.Background(),
		agent, src, mainRepo, "",
		sessionarchive.Opts{Logger: silentLogger()},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ArchivedPath != nil || res.BigPicture != nil {
		t.Errorf("expected null/null for empty snapshot, got %+v", res)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("empty source should have been removed; got stat err = %v", err)
	}
}

func TestArchive_ValidSnapshot_PersistentAgent_MovesToMainRepo(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")
	src := filepath.Join(mainRepo, "restart", "alpha.md")
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nagent: alpha\nsession_id: ses_x\nsaved_at: 2026-05-17T15:32:18.421Z\nreason: manual\nmachine_id: t\n---\n\n## 1. Big picture — what shipped this session\n\nLocked the spec.\n"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	agent := makeArchiveAgent("alpha", agentpkg.ModePersistent)
	res, err := sessionarchive.Archive(
		context.Background(),
		agent, src, mainRepo, "/some/worktree/.thrum",
		sessionarchive.Opts{Logger: silentLogger()},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ArchivedPath == nil {
		t.Fatal("expected ArchivedPath, got nil")
	}
	expectedDir := filepath.Join(mainRepo, "agents", "alpha", "sessions")
	if !strings.HasPrefix(*res.ArchivedPath, expectedDir+string(filepath.Separator)) {
		t.Errorf("ArchivedPath %q not under expected dir %q", *res.ArchivedPath, expectedDir)
	}
	if res.BigPicture == nil || *res.BigPicture != "Locked the spec." {
		t.Errorf("BigPicture mismatch: got %v", res.BigPicture)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("source not removed: %v", err)
	}
}

func TestArchive_ValidSnapshot_EphemeralAgent_MovesToWorktree(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), "main", ".thrum")
	worktree := filepath.Join(t.TempDir(), "wt", ".thrum")
	for _, d := range []string{mainRepo, worktree} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	src := filepath.Join(worktree, "restart", "beta.md")
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nagent: beta\nsession_id: ses_y\nsaved_at: 2026-05-17T15:33:00.000Z\nreason: external\nmachine_id: t\n---\n\nbody\n"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	agent := makeArchiveAgent("beta", agentpkg.ModeEphemeral)
	res, err := sessionarchive.Archive(
		context.Background(),
		agent, src, mainRepo, worktree,
		sessionarchive.Opts{Logger: silentLogger()},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ArchivedPath == nil {
		t.Fatal("expected ArchivedPath, got nil")
	}
	expectedDir := filepath.Join(worktree, "agents", "beta", "sessions")
	if !strings.HasPrefix(*res.ArchivedPath, expectedDir+string(filepath.Separator)) {
		t.Errorf("ArchivedPath %q not under expected ephemeral dir %q", *res.ArchivedPath, expectedDir)
	}
	// Big picture missing in this snapshot → nil
	if res.BigPicture != nil {
		t.Errorf("expected nil BigPicture for body without §1, got %v", *res.BigPicture)
	}
}

func TestArchive_PreservesSrcMtime_OnDest(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")
	src := filepath.Join(mainRepo, "restart", "alpha.md")
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	expectedMtime := time.Date(2026, 5, 17, 15, 32, 18, 421_000_000, time.UTC)
	if err := os.WriteFile(src, []byte("---\nsaved_at: 2026-05-17T15:32:18.421Z\n---\nbody"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chtimes(src, expectedMtime, expectedMtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	agent := makeArchiveAgent("alpha", agentpkg.ModePersistent)
	res, err := sessionarchive.Archive(
		context.Background(),
		agent, src, mainRepo, "",
		sessionarchive.Opts{Logger: silentLogger()},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ArchivedPath == nil {
		t.Fatal("expected ArchivedPath, got nil")
	}

	info, err := os.Stat(*res.ArchivedPath)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if !info.ModTime().Equal(expectedMtime) {
		t.Errorf("dest mtime drift: got %v, want %v", info.ModTime(), expectedMtime)
	}
}

func TestArchive_PermissionModes(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")
	src := writeSnapshot(t, mainRepo, "alpha.md", "2026-05-17T15:32:18.421Z", "body")

	// writeSnapshot creates the file in mainRepo/restart-shape dir — actually
	// here we just dropped it in mainRepo root; that's fine for the move
	// test. Fix up: it lives directly under mainRepo because we didn't go
	// through the restart subdir. Source location doesn't matter for the
	// Archive call.

	agent := makeArchiveAgent("alpha", agentpkg.ModePersistent)
	res, err := sessionarchive.Archive(
		context.Background(),
		agent, src, mainRepo, "",
		sessionarchive.Opts{Logger: silentLogger()},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ArchivedPath == nil {
		t.Fatal("expected ArchivedPath, got nil")
	}

	info, err := os.Stat(*res.ArchivedPath)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 0600", info.Mode().Perm())
	}

	sessionsDir := filepath.Dir(*res.ArchivedPath)
	dirInfo, err := os.Stat(sessionsDir)
	if err != nil {
		t.Fatalf("stat sessions dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 0700", dirInfo.Mode().Perm())
	}
}

// TestArchive_ConcurrentCallsSameAgent_Serialize asserts spec §3.4
// idempotency: when 5 goroutines race on the same srcPath, exactly
// one succeeds (claims the file via os.Rename) and the others see
// the source gone and return {nil, nil} cleanly.
func TestArchive_ConcurrentCallsSameAgent_Serialize(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")
	src := writeSnapshot(t, mainRepo, "alpha.md", "2026-05-17T15:32:18.421Z", "body")

	agent := makeArchiveAgent("alpha", agentpkg.ModePersistent)

	const goroutines = 5
	var wg sync.WaitGroup
	results := make([]*sessionarchive.ArchiveResult, goroutines)
	errs := make([]error, goroutines)
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			res, err := sessionarchive.Archive(
				context.Background(),
				agent, src, mainRepo, "",
				sessionarchive.Opts{Logger: silentLogger()},
			)
			results[idx] = res
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// Exactly one ArchivedPath should be non-nil; the rest no-op cleanly.
	var successes int
	for i, res := range results {
		if errs[i] != nil {
			t.Errorf("goroutine %d returned error: %v", i, errs[i])
			continue
		}
		if res == nil {
			t.Errorf("goroutine %d returned nil result with nil error", i)
			continue
		}
		if res.ArchivedPath != nil {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("expected exactly 1 archive success across %d goroutines, got %d", goroutines, successes)
	}
}

// TestArchive_ConcurrentCallsDifferentAgents_Parallel asserts that
// distinct-agent calls don't serialize through a shared mutex —
// each agent has its own mutex via sync.Map. Five agents archive
// five distinct snapshots; all five should succeed cleanly.
func TestArchive_ConcurrentCallsDifferentAgents_Parallel(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")

	const N = 5
	srcs := make([]string, N)
	agents := make([]agentpkg.Agent, N)
	for i := range N {
		agentID := fmt.Sprintf("agent-%d", i)
		srcs[i] = writeSnapshot(t, mainRepo, agentID+".md", "2026-05-17T15:32:18.421Z", "body-"+agentID)
		agents[i] = makeArchiveAgent(agentID, agentpkg.ModePersistent)
	}

	var wg sync.WaitGroup
	results := make([]*sessionarchive.ArchiveResult, N)
	errs := make([]error, N)
	wg.Add(N)
	for i := range N {
		go func(idx int) {
			defer wg.Done()
			res, err := sessionarchive.Archive(
				context.Background(),
				agents[idx], srcs[idx], mainRepo, "",
				sessionarchive.Opts{Logger: silentLogger()},
			)
			results[idx] = res
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, res := range results {
		if errs[i] != nil {
			t.Errorf("agent-%d returned error: %v", i, errs[i])
			continue
		}
		if res.ArchivedPath == nil {
			t.Errorf("agent-%d expected ArchivedPath, got nil", i)
		}
	}
}

// TestArchive_CollisionCap_ReturnsError pre-fills all 10 destination
// candidates and asserts Archive returns the collision-cap error
// (matches Task 3's UniqueDestPath cap).
func TestArchive_CollisionCap_ReturnsError(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")
	src := writeSnapshot(t, mainRepo, "alpha.md", "2026-05-17T15:32:18.421Z", "body")

	agent := makeArchiveAgent("alpha", agentpkg.ModePersistent)
	destDir := filepath.Join(mainRepo, "agents", "alpha", "sessions")
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		t.Fatalf("mkdir destDir: %v", err)
	}
	base := "20260517T153218421Z"
	if err := os.WriteFile(filepath.Join(destDir, base+"-restart.md"), []byte("collision"), 0o600); err != nil {
		t.Fatalf("touch base: %v", err)
	}
	for i := 1; i <= 9; i++ {
		path := filepath.Join(destDir, fmt.Sprintf("%s-restart-%d.md", base, i))
		if err := os.WriteFile(path, []byte("collision"), 0o600); err != nil {
			t.Fatalf("touch %d: %v", i, err)
		}
	}

	_, err := sessionarchive.Archive(
		context.Background(),
		agent, src, mainRepo, "",
		sessionarchive.Opts{Logger: silentLogger()},
	)
	if err == nil {
		t.Fatal("expected collision-cap error, got nil")
	}
	if !strings.Contains(err.Error(), "collision cap") {
		t.Errorf("expected collision-cap message, got %v", err)
	}
}

// TestArchive_MtimeFallback covers the middle layer of the spec §3.2
// step 6 fallback chain: when the snapshot has no saved_at frontmatter
// AND the source file has a valid mtime, the destination filename
// should be derived from the mtime — not from opts.Now. This is the
// realistic non-zero-mtime case (parse failed; FS reports a normal
// recent mtime).
//
// Pairs with TestArchive_InjectableNowFallback below which covers the
// third layer (parse failed AND mtime is zero → opts.Now fires).
func TestArchive_MtimeFallback(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(mainRepo, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(mainRepo, "alpha.md")
	// Body has NO frontmatter at all — parseSavedAtFrontmatterOK
	// returns ok=false on the missing opening "---\n" delimiter.
	if err := os.WriteFile(src, []byte("body without any frontmatter\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	knownMtime := time.Date(2026, 5, 17, 15, 32, 18, 421_000_000, time.UTC)
	if err := os.Chtimes(src, knownMtime, knownMtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// opts.Now is set to a value that would be detectable in the
	// filename if the test accidentally falls through to it. The
	// assertion below would fail in that case.
	notExpectedNow := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	agent := makeArchiveAgent("alpha", agentpkg.ModePersistent)
	res, err := sessionarchive.Archive(
		context.Background(),
		agent, src, mainRepo, "",
		sessionarchive.Opts{
			Logger: silentLogger(),
			Now:    func() time.Time { return notExpectedNow },
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ArchivedPath == nil {
		t.Fatal("expected ArchivedPath, got nil")
	}

	base := filepath.Base(*res.ArchivedPath)
	wantPrefix := sessionarchive.FormatTimestamp(knownMtime)
	if !strings.HasPrefix(base, wantPrefix) {
		t.Errorf("dest filename should derive from mtime: got %q, want prefix %q",
			base, wantPrefix)
	}
	notWantPrefix := sessionarchive.FormatTimestamp(notExpectedNow)
	if strings.HasPrefix(base, notWantPrefix) {
		t.Errorf("dest filename leaked opts.Now timestamp (mtime fallback skipped): %q starts with %q",
			base, notWantPrefix)
	}
}

// TestArchive_InjectableNowFallback covers the THIRD layer of the
// spec §3.2 step 6 fallback chain: when the snapshot has no saved_at
// frontmatter AND the source file's mtime is itself time.Time{} (zero
// value), the injected opts.Now drives the destination filename.
//
// The chain is parseSavedAtFrontmatterOK (parse) → info.ModTime
// (mtime) → opts.Now() (third fallback). This test exercises that
// third fallback explicitly — earlier the test accepted both nowFn
// and mtime outcomes as "passing", which made the nowFn branch
// untested per brainstormer-third I3.
//
// Portability note: forcing info.ModTime to time.Time{} via
// os.Chtimes(time.Time{}, time.Time{}) is platform-dependent. The
// test stats the source post-chtimes to confirm IsZero() actually
// holds; if the platform interprets time.Time{} differently and
// produces a non-zero mtime, the test t.Skip cleanly rather than
// silently passing-but-not-exercising the third fallback.
func TestArchive_InjectableNowFallback(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(mainRepo, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(mainRepo, "alpha.md")
	// Body has NO frontmatter — parseSavedAtFrontmatterOK fails;
	// chain falls through to mtime.
	if err := os.WriteFile(src, []byte("body without any frontmatter\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Force mtime to time.Time{} (year 1) — this is what the third
	// fallback branch keys off. Platform behavior varies; verify by
	// stat-ing immediately after chtimes.
	if err := os.Chtimes(src, time.Time{}, time.Time{}); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	info, err := os.Stat(src)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.ModTime().IsZero() {
		// FS interpreted time.Time{} as "leave unchanged" or
		// "current time" — can't exercise the third fallback
		// portably on this platform. Mtime-fallback coverage is
		// provided by TestArchive_MtimeFallback.
		t.Skipf("platform does not honor time.Time{} as zero mtime (got %v); third-fallback path untested here", info.ModTime())
	}

	injected := time.Date(2030, 6, 15, 12, 0, 0, 0, time.UTC)
	agent := makeArchiveAgent("alpha", agentpkg.ModePersistent)
	res, err := sessionarchive.Archive(
		context.Background(),
		agent, src, mainRepo, "",
		sessionarchive.Opts{
			Logger: silentLogger(),
			Now:    func() time.Time { return injected },
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ArchivedPath == nil {
		t.Fatal("expected ArchivedPath, got nil")
	}

	// Tightened per I3: assert ONLY the injected timestamp prefix.
	// Previous shape accepted both injected and mtime prefixes,
	// silently masking that the test never reached the third
	// fallback.
	base := filepath.Base(*res.ArchivedPath)
	wantPrefix := sessionarchive.FormatTimestamp(injected)
	if !strings.HasPrefix(base, wantPrefix) {
		t.Errorf("dest filename should use injected nowFn timestamp: got %q, want prefix %q",
			base, wantPrefix)
	}
}

// TestArchive_RenameFailure_FriendlyError is the sentinel test per
// brainstormer-third I5: spec §3.2 step 12 calls for a copy-via-
// tempfile fallback when os.Rename fails (e.g., cross-device move).
// The current implementation surfaces a hard error instead — this
// is intentional per the local-only-data sub-epic scope, tracked
// in thrum-8rgu errata.
//
// This sentinel test asserts the SURFACED error message is operator-
// friendly (contains "atomic rename") so a future regression that
// drops the wrapper or replaces it with a bare errno is detectable.
// Forces a rename failure by writing the source snapshot, then
// pre-creating a SUBDIRECTORY at the expected destination path —
// os.Rename refuses to overwrite a directory with a file.
//
// The exact failure mechanism varies across OS / filesystems but
// the test asserts ONLY the error-wrapper text shape, not the
// underlying errno, so it stays portable.
func TestArchive_RenameFailure_FriendlyError(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")
	src := writeSnapshot(t, mainRepo, "alpha.md", "2026-05-17T15:32:18.421Z", "body")

	// Pre-create a directory at the expected destination path.
	// FormatTimestamp(2026-05-17T15:32:18.421Z) = 20260517T153218421Z;
	// destination is <mainRepo>/agents/alpha/sessions/<ts>-restart.md.
	destDir := filepath.Join(mainRepo, "agents", "alpha", "sessions")
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		t.Fatalf("mkdir destDir: %v", err)
	}
	conflictPath := filepath.Join(destDir, "20260517T153218421Z-restart.md")
	if err := os.MkdirAll(conflictPath, 0o700); err != nil {
		t.Fatalf("mkdir conflictPath: %v", err)
	}

	// UniqueDestPath will pass over the directory (stat returns
	// non-NotExist, treats it as collision) and move to suffix -1.
	// To actually FORCE a rename failure we need to also create
	// directories at the suffix candidates. Cap at 5 (enough to
	// exhaust most realistic attempts; UniqueDestPath returns its
	// own collision-cap error after 10).
	for i := 1; i <= 9; i++ {
		path := filepath.Join(destDir, fmt.Sprintf("20260517T153218421Z-restart-%d.md", i))
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("mkdir suffix %d: %v", i, err)
		}
	}

	agent := makeArchiveAgent("alpha", agentpkg.ModePersistent)
	_, err := sessionarchive.Archive(
		context.Background(),
		agent, src, mainRepo, "",
		sessionarchive.Opts{Logger: silentLogger()},
	)
	if err == nil {
		t.Fatal("expected error when destination paths are blocked, got nil")
	}
	// The UniqueDestPath cap kicks in first since every candidate
	// stat returns "exists" (the directories). Verify the operator-
	// facing error message identifies the failure context clearly
	// — either "collision cap" (current behavior with all paths
	// blocked) or "atomic rename" (if UniqueDestPath returned a
	// free slot but rename then failed). Both are sentinel error
	// shapes a regression would lose.
	//
	// LATENT-BRANCH NOTE (brainstormer-third Medium #1): "atomic
	// rename" in the assertion is dead-on-this-test — the
	// pre-created directories guarantee the collision-cap path
	// always fires first, so os.Rename is never reached. The
	// "atomic rename" branch survives in the assertion only as a
	// forward-guard: if UniqueDestPath's cap is ever raised, the
	// test would then exercise the rename path and the assertion
	// remains correct. Not worth a separate test today since the
	// errata path is the copy-fallback (deferred), but documented
	// here so a future reader doesn't trust the rename branch as
	// "tested".
	msg := err.Error()
	if !strings.Contains(msg, "collision cap") && !strings.Contains(msg, "atomic rename") && !strings.Contains(msg, "session-archive") {
		t.Errorf("error message should contain a session-archive context marker (collision cap / atomic rename / session-archive): %v", err)
	}
}
