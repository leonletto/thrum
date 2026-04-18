package guard

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitInit runs a minimal `git init` inside dir so G2's git-root walk
// has something to find. A plain .git directory would work too, but
// invoking git keeps the fixture honest.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "--quiet")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func TestG2_InGitRepo_Proceeds(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	if err := G2(ModeStrict, dir, false, nil); err != nil {
		t.Errorf("want nil in git repo, got %v", err)
	}
}

func TestG2_NotInGitRepo_Refuses(t *testing.T) {
	// Use a nested temp dir under os.TempDir() so the walk can't
	// accidentally find /Users/leon/... repo roots upstream of the
	// test env.
	dir := t.TempDir()
	err := G2(ModeStrict, dir, false, nil)
	if err == nil {
		t.Fatal("want error for non-git dir")
	}
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *Error, got %T", err)
	}
	if gErr.Guard != "non_git_bootstrap" {
		t.Errorf("guard=%q", gErr.Guard)
	}
}

func TestG2_NotInGitRepo_ForceProceeds(t *testing.T) {
	dir := t.TempDir()
	if err := G2(ModeStrict, dir, true, nil); err != nil {
		t.Errorf("--force should proceed in non-git dir, got %v", err)
	}
}

func TestG2_OffMode_NoOp(t *testing.T) {
	dir := t.TempDir()
	if err := G2(ModeOff, dir, false, nil); err != nil {
		t.Errorf("off mode should proceed, got %v", err)
	}
}

func TestG2_WarnMode_Proceeds(t *testing.T) {
	dir := t.TempDir()
	if err := G2(ModeWarn, dir, false, nil); err != nil {
		t.Errorf("warn mode should proceed, got %v", err)
	}
}

func TestG2_NestedSubdirOfGitRepo_Proceeds(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := G2(ModeStrict, nested, false, nil); err != nil {
		t.Errorf("nested subdir of git repo should pass, got %v", err)
	}
}
