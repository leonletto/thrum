package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupClaudeMD_PrintOnly_NoSideEffects covers the default (no --apply)
// mode: the template is written to Out verbatim and no file is created.
func TestSetupClaudeMD_PrintOnly_NoSideEffects(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer

	res, err := SetupClaudeMD(SetupClaudeMDOptions{Dir: dir, Out: &out})
	if err != nil {
		t.Fatalf("SetupClaudeMD: %v", err)
	}
	if res.Mode != ModePrinted {
		t.Errorf("Mode = %q, want %q", res.Mode, ModePrinted)
	}
	if !bytes.Equal(out.Bytes(), claudeMDTemplate) {
		t.Errorf("out bytes differ from embedded template")
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("CLAUDE.md must not be created in print-only mode, got stat err: %v", err)
	}
}

// TestSetupClaudeMD_Apply_CreatesFreshFile covers --apply on a directory with
// no existing CLAUDE.md: the file is created containing only the template.
func TestSetupClaudeMD_Apply_CreatesFreshFile(t *testing.T) {
	dir := t.TempDir()

	res, err := SetupClaudeMD(SetupClaudeMDOptions{Dir: dir, Apply: true})
	if err != nil {
		t.Fatalf("SetupClaudeMD: %v", err)
	}
	if res.Mode != ModeCreated {
		t.Errorf("Mode = %q, want %q", res.Mode, ModeCreated)
	}
	got, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, claudeMDTemplate) {
		t.Errorf("fresh file content differs from embedded template")
	}
}

// TestSetupClaudeMD_Apply_AppendsToExistingFile covers --apply against a
// file that exists but has no Thrum block: the block is appended with a
// blank-line separator and all pre-existing content is preserved verbatim.
func TestSetupClaudeMD_Apply_AppendsToExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	prior := "# Project notes\n\nSome existing guidance.\n"
	if err := os.WriteFile(path, []byte(prior), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	res, err := SetupClaudeMD(SetupClaudeMDOptions{Dir: dir, Apply: true})
	if err != nil {
		t.Fatalf("SetupClaudeMD: %v", err)
	}
	if res.Mode != ModeAppended {
		t.Errorf("Mode = %q, want %q", res.Mode, ModeAppended)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	gotStr := string(got)
	if !strings.HasPrefix(gotStr, prior) {
		t.Errorf("prior content not preserved at head; got prefix:\n%s", gotStr[:min(len(gotStr), 100)])
	}
	if !strings.Contains(gotStr, claudeMDBeginMarker) || !strings.Contains(gotStr, claudeMDEndMarker) {
		t.Errorf("block markers missing from appended file")
	}
}

// TestSetupClaudeMD_Apply_RefusesWhenBlockExists covers --apply without
// --force against a file that already has the Thrum block: returns
// ErrClaudeMDBlockExists and leaves the file untouched.
func TestSetupClaudeMD_Apply_RefusesWhenBlockExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	prior := "# Header\n\n<!-- BEGIN THRUM -->\nold content\n<!-- END THRUM -->\n"
	if err := os.WriteFile(path, []byte(prior), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := SetupClaudeMD(SetupClaudeMDOptions{Dir: dir, Apply: true})
	if !errors.Is(err, ErrClaudeMDBlockExists) {
		t.Fatalf("expected ErrClaudeMDBlockExists, got: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != prior {
		t.Errorf("file modified despite refused apply; got:\n%s", got)
	}
}

// TestSetupClaudeMD_ApplyForce_ReplacesBlock covers --apply --force: the
// existing block is replaced with the current template, pre-existing
// content above and below the block is preserved byte-for-byte.
func TestSetupClaudeMD_ApplyForce_ReplacesBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	prior := "# Header\n\n<!-- BEGIN THRUM -->\nOLD CONTENT\n<!-- END THRUM -->\n\n## Footer section\nmore notes\n"
	if err := os.WriteFile(path, []byte(prior), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	res, err := SetupClaudeMD(SetupClaudeMDOptions{Dir: dir, Apply: true, Force: true})
	if err != nil {
		t.Fatalf("SetupClaudeMD: %v", err)
	}
	if res.Mode != ModeReplaced {
		t.Errorf("Mode = %q, want %q", res.Mode, ModeReplaced)
	}
	got, _ := os.ReadFile(path)
	gotStr := string(got)
	if !strings.HasPrefix(gotStr, "# Header\n\n") {
		t.Errorf("prior header not preserved; got:\n%s", gotStr)
	}
	if !strings.HasSuffix(gotStr, "\n## Footer section\nmore notes\n") {
		t.Errorf("prior footer not preserved; got:\n%s", gotStr)
	}
	if strings.Contains(gotStr, "OLD CONTENT") {
		t.Errorf("stale block content still present; got:\n%s", gotStr)
	}
	if !strings.Contains(gotStr, claudeMDBeginMarker) || !strings.Contains(gotStr, claudeMDEndMarker) {
		t.Errorf("block markers missing post-replace; got:\n%s", gotStr)
	}
}

// TestSetupClaudeMD_ApplyForce_NoMarkersStillAppends covers the --apply
// --force fall-through case: if markers aren't present, behavior matches
// plain --apply (append). Force should never change an append into anything
// stranger; it only unlocks the "replace existing block" path.
func TestSetupClaudeMD_ApplyForce_NoMarkersStillAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	prior := "pre-existing notes\n"
	if err := os.WriteFile(path, []byte(prior), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	res, err := SetupClaudeMD(SetupClaudeMDOptions{Dir: dir, Apply: true, Force: true})
	if err != nil {
		t.Fatalf("SetupClaudeMD: %v", err)
	}
	if res.Mode != ModeAppended {
		t.Errorf("Mode = %q, want %q", res.Mode, ModeAppended)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), prior) {
		t.Errorf("prior content not preserved")
	}
	if !strings.Contains(string(got), claudeMDBeginMarker) {
		t.Errorf("block not appended")
	}
}

// TestSetupClaudeMD_ApplyForce_Idempotent is the critical contract test:
// running --apply --force twice must produce byte-identical file content on
// the second run. Protects against a common drift bug where repeated
// installs compound whitespace or marker duplication.
func TestSetupClaudeMD_ApplyForce_Idempotent(t *testing.T) {
	t.Run("fresh_file", func(t *testing.T) { assertIdempotent(t, "") })
	t.Run("file_with_prior_content", func(t *testing.T) {
		assertIdempotent(t, "# Project\n\nSome notes\n")
	})
	t.Run("file_with_content_and_footer", func(t *testing.T) {
		assertIdempotent(t, "# Project\n\nSome notes\n\n## Extra\ntrailing stuff\n")
	})
	t.Run("file_no_trailing_newline", func(t *testing.T) {
		assertIdempotent(t, "# Project\n\nSome notes")
	})
}

func assertIdempotent(t *testing.T, prior string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	if prior != "" {
		if err := os.WriteFile(path, []byte(prior), 0600); err != nil {
			t.Fatalf("seed WriteFile: %v", err)
		}
	}

	if _, err := SetupClaudeMD(SetupClaudeMDOptions{Dir: dir, Apply: true, Force: true}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("first ReadFile: %v", err)
	}

	if _, err := SetupClaudeMD(SetupClaudeMDOptions{Dir: dir, Apply: true, Force: true}); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("second ReadFile: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("not idempotent; first run:\n%s\n\nsecond run:\n%s", first, second)
	}
}

// TestSetupClaudeMD_TemplateHasMarkers sanity-checks the embedded template:
// both markers must be present, otherwise the replace path loops forever on
// subsequent --force runs (idempotency depends on the rewritten block being
// re-findable).
func TestSetupClaudeMD_TemplateHasMarkers(t *testing.T) {
	if !bytes.Contains(claudeMDTemplate, []byte(claudeMDBeginMarker)) {
		t.Errorf("embedded template missing BEGIN marker")
	}
	if !bytes.Contains(claudeMDTemplate, []byte(claudeMDEndMarker)) {
		t.Errorf("embedded template missing END marker")
	}
	if !bytes.HasSuffix(claudeMDTemplate, []byte("\n")) {
		t.Errorf("embedded template must end with a newline for idempotency")
	}
}
