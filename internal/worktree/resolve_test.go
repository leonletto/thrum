package worktree

import (
	"os"
	"path/filepath"
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
