package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/worktree"
)

// TestPrintRedirectConfirmations_ThrumOnly asserts that when the main
// repo has no .beads/ directory (and therefore EnsureRedirects did not
// write a beads redirect), only the thrum line is printed.
func TestPrintRedirectConfirmations_ThrumOnly(t *testing.T) {
	tmp := t.TempDir()
	mainRepo := filepath.Join(tmp, "main")
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wt, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := worktree.EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("EnsureRedirects: %v", err)
	}

	var buf bytes.Buffer
	PrintRedirectConfirmations(&buf, wt)
	got := buf.String()
	if !strings.Contains(got, "Thrum redirect configured") {
		t.Errorf("missing thrum line: %q", got)
	}
	if strings.Contains(got, "Beads redirect configured") {
		t.Errorf("unexpected beads line when main repo has no .beads/: %q", got)
	}
}

// TestPrintRedirectConfirmations_BothWhenBeadsPresent asserts that
// when the main repo has .beads/ and EnsureRedirects wires up the
// beads redirect, both confirmation lines appear. Regression guard
// for thrum-ufv5.13 (Step 10B.2 expects explicit confirmation).
func TestPrintRedirectConfirmations_BothWhenBeadsPresent(t *testing.T) {
	tmp := t.TempDir()
	mainRepo := filepath.Join(tmp, "main")
	wt := filepath.Join(tmp, "wt")
	for _, d := range []string{
		filepath.Join(mainRepo, ".thrum"),
		filepath.Join(mainRepo, ".beads"),
		wt,
	} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	if err := worktree.EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("EnsureRedirects: %v", err)
	}

	// Sanity: the beads redirect file should now exist; the helper
	// is driven by that artifact's presence.
	if _, err := os.Stat(filepath.Join(wt, ".beads", "redirect")); err != nil {
		t.Fatalf("EnsureRedirects did not create beads redirect: %v", err)
	}

	var buf bytes.Buffer
	PrintRedirectConfirmations(&buf, wt)
	got := buf.String()
	if !strings.Contains(got, "Thrum redirect configured") {
		t.Errorf("missing thrum line: %q", got)
	}
	if !strings.Contains(got, "Beads redirect configured") {
		t.Errorf("missing beads line despite .beads/ present: %q", got)
	}
}
