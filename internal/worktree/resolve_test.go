package worktree

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizeWorktreePath_AbsoluteExisting(t *testing.T) {
	dir := t.TempDir()
	got, err := NormalizeWorktreePath(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The helper resolves symlinks, so the returned path must equal
	// the symlink-resolved form of the input.
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got != want {
		t.Fatalf("expected symlink-resolved %q, got %q", want, got)
	}
}

func TestNormalizeWorktreePath_Relative_ErrorsOut(t *testing.T) {
	_, err := NormalizeWorktreePath("./relative/path")
	if err == nil {
		t.Fatal("expected error for relative input, got nil")
	}
}

func TestNormalizeWorktreePath_BareName_ErrorsOut(t *testing.T) {
	// This is the exact x6e8.2 symptom: GetWorktreeName returns the basename,
	// which is not absolute. The helper must refuse.
	_, err := NormalizeWorktreePath("thrum")
	if err == nil {
		t.Fatal("expected error for bare-name input, got nil")
	}
}

func TestNormalizeWorktreePath_Empty_ErrorsOut(t *testing.T) {
	_, err := NormalizeWorktreePath("")
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}

func TestNormalizeWorktreePath_NonExistent_ErrorsOut(t *testing.T) {
	_, err := NormalizeWorktreePath("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}
}

func TestNormalizeWorktreePath_TrailingSlash_Cleaned(t *testing.T) {
	dir := t.TempDir()
	got, err := NormalizeWorktreePath(dir + string(os.PathSeparator))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != filepath.Clean(got) {
		t.Fatalf("expected cleaned path, got %q", got)
	}
}

// --- CanonicalizeWorktreePath tests ---

// TestCanonicalizeWorktreePath_Success verifies that a path reached via a
// symlink is resolved to the canonical target.
func TestCanonicalizeWorktreePath_Success(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	target := t.TempDir()
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	got := CanonicalizeWorktreePath(link)

	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks(target): %v", err)
	}
	if got != want {
		t.Fatalf("CanonicalizeWorktreePath(%q) = %q, want %q (resolved target)", link, got, want)
	}
}

// TestCanonicalizeWorktreePath_NonexistentPath_FailOpen verifies that a
// non-existent path is returned unchanged (fail-open semantics).
func TestCanonicalizeWorktreePath_NonexistentPath_FailOpen(t *testing.T) {
	nonexistent := "/nonexistent/path/thrum-g8e8-test"
	got := CanonicalizeWorktreePath(nonexistent)
	if got != nonexistent {
		t.Fatalf("CanonicalizeWorktreePath(%q) = %q, want %q (unchanged on EvalSymlinks failure)", nonexistent, got, nonexistent)
	}
}

// TestCanonicalizeWorktreePath_AlreadyCanonical verifies that a path with no
// symlinks passes through unchanged (modulo filepath.Clean).
func TestCanonicalizeWorktreePath_AlreadyCanonical(t *testing.T) {
	dir := t.TempDir()
	// t.TempDir() already resolves to a canonical path on most systems,
	// but run EvalSymlinks to get the definitive canonical form.
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	got := CanonicalizeWorktreePath(canonical)
	if got != canonical {
		t.Fatalf("CanonicalizeWorktreePath(%q) = %q, want %q (already canonical)", canonical, got, canonical)
	}
}
