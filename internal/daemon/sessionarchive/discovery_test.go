package sessionarchive_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/sessionarchive"
)

// setupSessions creates n placeholder session files in dir with
// increasing mtimes (so sort-descending picks the LAST one as
// "most recent"). Returns the most-recent file's mtime for the
// "YYYY-MM-DD" assertion.
func setupSessions(t *testing.T, dir string, n int) time.Time {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}
	base := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	var newest time.Time
	for i := range n {
		name := time.Now().UTC().Format("20060102T150405000000Z") +
			"-" + string(rune('a'+i)) + "-restart.md"
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
			t.Fatalf("write session %d: %v", i, err)
		}
		mtime := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("chtimes session %d: %v", i, err)
		}
		newest = mtime
	}
	return newest
}

func TestRenderDiscoveryHint_NoSessionsDir_ReturnsEmpty(t *testing.T) {
	// Directory simply doesn't exist — agent has never archived.
	hint := sessionarchive.RenderDiscoveryHint("/tmp/definitely-not-a-real-sessions-dir-xyz", nil)
	if hint != "" {
		t.Errorf("got %q, want empty", hint)
	}
}

func TestRenderDiscoveryHint_EmptySessionsDir_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir() // exists but empty
	hint := sessionarchive.RenderDiscoveryHint(dir, nil)
	if hint != "" {
		t.Errorf("got %q, want empty (empty dir)", hint)
	}
}

func TestRenderDiscoveryHint_NonRestartFilesIgnored_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	// Files that don't match the -restart.md suffix should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "random.md"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	hint := sessionarchive.RenderDiscoveryHint(dir, nil)
	if hint != "" {
		t.Errorf("got %q, want empty (no -restart.md entries)", hint)
	}
}

func TestRenderDiscoveryHint_WithSessions_NoBigPicture_OmitsLine2(t *testing.T) {
	dir := t.TempDir()
	setupSessions(t, dir, 2)

	hint := sessionarchive.RenderDiscoveryHint(dir, &sessionarchive.ArchiveResult{})

	if !strings.Contains(hint, "Past sessions: 2 saved") {
		t.Errorf("missing line 1 'Past sessions: 2 saved': %q", hint)
	}
	if !strings.Contains(hint, "(most recent 2026-05-17)") {
		t.Errorf("line 1 missing date stamp: %q", hint)
	}
	if !strings.Contains(hint, "at "+dir) {
		t.Errorf("line 1 missing sessionsDir suffix: %q", hint)
	}
	if strings.Contains(hint, "Last big picture") {
		t.Errorf("line 2 should be omitted when BigPicture is nil: %q", hint)
	}
}

func TestRenderDiscoveryHint_WithSessions_NilArchiveResult_OnlyLine1(t *testing.T) {
	dir := t.TempDir()
	setupSessions(t, dir, 1)

	// Nil archiveResult is a valid call shape (no-snapshot Archive returns
	// a result with nil BigPicture; some callers may pass nil here).
	hint := sessionarchive.RenderDiscoveryHint(dir, nil)
	if !strings.Contains(hint, "Past sessions: 1 saved") {
		t.Errorf("missing line 1: %q", hint)
	}
	if strings.Contains(hint, "Last big picture") {
		t.Errorf("line 2 should be omitted for nil archiveResult: %q", hint)
	}
}

func TestRenderDiscoveryHint_WithSessions_AndBigPicture_RendersBothLines(t *testing.T) {
	dir := t.TempDir()
	setupSessions(t, dir, 3)
	bigPicture := "Locked the spec."

	hint := sessionarchive.RenderDiscoveryHint(dir, &sessionarchive.ArchiveResult{
		BigPicture: &bigPicture,
	})

	if !strings.Contains(hint, "Past sessions: 3 saved") {
		t.Errorf("missing line 1: %q", hint)
	}
	if !strings.Contains(hint, "Last big picture: Locked the spec.") {
		t.Errorf("missing line 2: %q", hint)
	}
}

func TestRenderDiscoveryHint_LongBigPicture_TruncatedWithEllipsis(t *testing.T) {
	dir := t.TempDir()
	setupSessions(t, dir, 1)

	longBP := strings.Repeat("x", 200) // 200 runes, exceeds 120 threshold
	hint := sessionarchive.RenderDiscoveryHint(dir, &sessionarchive.ArchiveResult{BigPicture: &longBP})

	// Line 2 must contain the truncation marker.
	if !strings.Contains(hint, "…") {
		t.Errorf("expected truncation marker '…' in hint, got %q", hint)
	}
	// Generous upper bound — line 1 length varies with sessionsDir path.
	if len(hint) > 400 {
		t.Errorf("hint too long: %d bytes", len(hint))
	}
	// The truncated §1 body should be ~120 runes including the ellipsis.
	// Locate "Last big picture: " and measure what follows.
	_, body, ok := strings.Cut(hint, "Last big picture: ")
	if !ok {
		t.Fatalf("hint missing 'Last big picture:' prefix: %q", hint)
	}
	bodyRunes := []rune(body)
	if len(bodyRunes) > 121 { // 120 + 1 ellipsis is the upper bound
		t.Errorf("truncated body too long: %d runes (want ≤ 121)", len(bodyRunes))
	}
}

func TestRenderDiscoveryHint_EmptyBigPicturePointer_OmitsLine2(t *testing.T) {
	// Pointer is non-nil but the dereffed value is empty string.
	// Should treat as "no big picture" per the spec §7.2 contract.
	dir := t.TempDir()
	setupSessions(t, dir, 1)
	empty := ""
	hint := sessionarchive.RenderDiscoveryHint(dir, &sessionarchive.ArchiveResult{BigPicture: &empty})

	if !strings.Contains(hint, "Past sessions:") {
		t.Error("missing line 1 for empty big-picture case")
	}
	if strings.Contains(hint, "Last big picture") {
		t.Errorf("line 2 should be omitted for empty big-picture: %q", hint)
	}
}
