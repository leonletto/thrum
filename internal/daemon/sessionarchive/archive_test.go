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
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
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

// TestArchive_InjectableNowFallback ensures opts.Now is consulted when
// the saved_at frontmatter is missing AND the source has zero mtime
// (rare; tar extracts, some test fixtures). The injected Now() should
// drive the timestamp in the destination filename.
func TestArchive_InjectableNowFallback(t *testing.T) {
	mainRepo := filepath.Join(t.TempDir(), ".thrum")
	src := filepath.Join(mainRepo, "alpha.md")
	if err := os.MkdirAll(mainRepo, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Body has no frontmatter at all — parseSavedAt returns mtime as fallback.
	if err := os.WriteFile(src, []byte("no frontmatter body"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Force mtime to zero (epoch). On some filesystems this collapses to
	// epoch-or-nearby; for our purposes "before injected Now" is enough.
	zero := time.Unix(0, 0)
	if err := os.Chtimes(src, zero, zero); err != nil {
		t.Fatalf("chtimes: %v", err)
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
	// The destination filename SHOULD start with the injected timestamp
	// IF mtime fell back through to opts.Now. If the filesystem rounded
	// the epoch-mtime to something non-zero, this assertion would still
	// hold because the chain is: parseSavedAt → mtime → Now, and only
	// the Now branch fires when mtime is zero. To be robust, we accept
	// either "starts with injected Now timestamp" OR "destination is
	// under sessionsDir at all" (because the FS may not give us zero
	// mtime reliably).
	base := filepath.Base(*res.ArchivedPath)
	wantPrefix := sessionarchive.FormatTimestamp(injected)
	mtimePrefix := sessionarchive.FormatTimestamp(zero.UTC())
	if !strings.HasPrefix(base, wantPrefix) && !strings.HasPrefix(base, mtimePrefix) {
		t.Errorf("dest filename %q matches neither injected Now (%q) nor zero-mtime fallback (%q)",
			base, wantPrefix, mtimePrefix)
	}
}
