package sessionarchive_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/sessionarchive"
)

func TestUniqueDestPath_NoCollision(t *testing.T) {
	dir := t.TempDir()
	got, err := sessionarchive.UniqueDestPath(dir, "20260517T153218421Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, "20260517T153218421Z-restart.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUniqueDestPath_WithCollision_Suffixes1Through9(t *testing.T) {
	dir := t.TempDir()
	base := "20260517T153218421Z"
	// Create base + 8 colliding files; the 9th suffix should be free.
	mustTouchFile(t, filepath.Join(dir, base+"-restart.md"))
	for i := 1; i <= 8; i++ {
		mustTouchFile(t, filepath.Join(dir, fmt.Sprintf("%s-restart-%d.md", base, i)))
	}
	got, err := sessionarchive.UniqueDestPath(dir, base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, base+"-restart-9.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUniqueDestPath_FirstCollision_PicksSuffix1(t *testing.T) {
	dir := t.TempDir()
	base := "20260517T153218421Z"
	mustTouchFile(t, filepath.Join(dir, base+"-restart.md"))

	got, err := sessionarchive.UniqueDestPath(dir, base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, base+"-restart-1.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUniqueDestPath_AtCollisionCap_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	base := "20260517T153218421Z"
	mustTouchFile(t, filepath.Join(dir, base+"-restart.md"))
	for i := 1; i <= 9; i++ {
		mustTouchFile(t, filepath.Join(dir, fmt.Sprintf("%s-restart-%d.md", base, i)))
	}

	_, err := sessionarchive.UniqueDestPath(dir, base)
	if err == nil {
		t.Fatal("expected error at collision cap (10 attempts), got nil")
	}
}

func mustTouchFile(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path) // #nosec G304 -- path under t.TempDir()
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	_ = f.Close()
}
