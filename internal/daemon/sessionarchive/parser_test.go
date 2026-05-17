package sessionarchive_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/sessionarchive"
)

// snapshotWithBigPicture builds a minimal-but-valid snapshot string
// with the given §1 heading and body inserted under it.
func snapshotWithBigPicture(t *testing.T, heading, body string) string {
	t.Helper()
	return fmt.Sprintf("---\nagent: test\nsession_id: ses_x\nsaved_at: 2026-05-17T15:32:18.421Z\nreason: manual\nmachine_id: t\n---\n\n%s\n\n%s\n", heading, body)
}

// snapshotWithoutBigPicture builds a valid snapshot whose body has no
// §1 section at all.
func snapshotWithoutBigPicture(t *testing.T) string {
	t.Helper()
	return "---\nagent: test\nsession_id: ses_x\nsaved_at: 2026-05-17T15:32:18.421Z\nreason: manual\nmachine_id: t\n---\n\n# Restart Snapshot — test\n\nno §1 section in this body\n"
}

func TestParseSavedAtFrontmatter_ValidFrontmatter(t *testing.T) {
	content := `---
agent: researcher_agents
session_id: ses_01KRPWGJN828SB80H9RGJ7EBP5
saved_at: 2026-05-17T15:32:18.421Z
reason: external
machine_id: leon-m1pro.local
---

# Restart Snapshot

body content here
`
	fallback := time.Time{}
	got := sessionarchive.ParseSavedAtFrontmatter(content, fallback)
	want := time.Date(2026, 5, 17, 15, 32, 18, 421_000_000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseSavedAtFrontmatter_MissingFrontmatter_UsesFallback(t *testing.T) {
	content := "no frontmatter here just body"
	fallback := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got := sessionarchive.ParseSavedAtFrontmatter(content, fallback)
	if !got.Equal(fallback) {
		t.Errorf("got %v, want fallback %v", got, fallback)
	}
}

func TestParseSavedAtFrontmatter_MalformedYAML_UsesFallback(t *testing.T) {
	content := "---\nnot valid yaml: : :\nsaved_at: not-a-date\n---\nbody"
	fallback := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got := sessionarchive.ParseSavedAtFrontmatter(content, fallback)
	if !got.Equal(fallback) {
		t.Errorf("got %v, want fallback %v", got, fallback)
	}
}

func TestParseSavedAtFrontmatter_MissingSavedAtKey_UsesFallback(t *testing.T) {
	// Frontmatter present + valid but no saved_at key — return fallback per spec §4.4.
	content := `---
agent: foo
reason: manual
---
body
`
	fallback := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	got := sessionarchive.ParseSavedAtFrontmatter(content, fallback)
	if !got.Equal(fallback) {
		t.Errorf("got %v, want fallback %v", got, fallback)
	}
}

func TestParseSavedAtFrontmatter_NoClosingDelimiter_UsesFallback(t *testing.T) {
	// Opens frontmatter but never closes — defensive fallback.
	content := "---\nagent: foo\nsaved_at: 2026-05-17T15:32:18.421Z\nbody continues forever without closing delimiter"
	fallback := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got := sessionarchive.ParseSavedAtFrontmatter(content, fallback)
	if !got.Equal(fallback) {
		t.Errorf("got %v, want fallback %v (unclosed frontmatter must not parse)", got, fallback)
	}
}

func TestParseSavedAtFrontmatter_PreservesNanos(t *testing.T) {
	// 9-digit nanosecond fraction must round-trip.
	content := "---\nsaved_at: 2026-05-17T15:32:18.123456789Z\n---\nbody"
	fallback := time.Time{}
	got := sessionarchive.ParseSavedAtFrontmatter(content, fallback)
	want := time.Date(2026, 5, 17, 15, 32, 18, 123_456_789, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v (nano precision)", got, want)
	}
}

// ---------- parseBigPicture tests (Task 4 / spec §6A.4) ----------

func TestParseBigPicture_EMDashHeading_RawFalse_Normalized(t *testing.T) {
	content := snapshotWithBigPicture(t, "## 1. Big picture — what shipped this session", "Locked the spec.\nTwo cycles.")
	got := sessionarchive.ParseBigPicture([]byte(content), false)
	want := "Locked the spec. Two cycles."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseBigPicture_ASCIIDashHeading_RawFalse_Normalized(t *testing.T) {
	content := snapshotWithBigPicture(t, "## 1. Big picture -- what shipped this session", "Locked the spec.\nTwo cycles.")
	got := sessionarchive.ParseBigPicture([]byte(content), false)
	want := "Locked the spec. Two cycles."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseBigPicture_RawTrue_PreservesLineBreaks(t *testing.T) {
	body := "Locked the spec.\nTwo cycles."
	content := snapshotWithBigPicture(t, "## 1. Big picture — what shipped this session", body)
	got := sessionarchive.ParseBigPicture([]byte(content), true)
	if got != body {
		t.Errorf("got %q, want %q", got, body)
	}
}

func TestParseBigPicture_MissingSection_ReturnsEmpty(t *testing.T) {
	content := snapshotWithoutBigPicture(t)
	got := sessionarchive.ParseBigPicture([]byte(content), false)
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestParseBigPicture_EmptyBody_ReturnsEmpty(t *testing.T) {
	content := snapshotWithBigPicture(t, "## 1. Big picture — what shipped this session", "")
	got := sessionarchive.ParseBigPicture([]byte(content), false)
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestParseBigPicture_TerminatesAtNextHeading(t *testing.T) {
	content := "---\nagent: test\n---\n## 1. Big picture — what shipped this session\n\nBodyline1.\n\n## 2. Resume Plan\n\nShould not appear.\n"
	got := sessionarchive.ParseBigPicture([]byte(content), false)
	if !strings.HasPrefix(got, "Bodyline1") {
		t.Errorf("got %q does not start with expected prefix", got)
	}
	if strings.Contains(got, "Should not appear") {
		t.Errorf("got %q leaked content from next section", got)
	}
}

// TestParseBigPicture_TerminatesAtNextHRBlock covers the `---\n` block
// terminator (acceptance criterion explicitly distinct from the `## `
// terminator above).
func TestParseBigPicture_TerminatesAtNextHRBlock(t *testing.T) {
	content := "---\nagent: test\n---\n## 1. Big picture — what shipped this session\n\nBodyline1.\n\n---\n\nMore content past HR.\n"
	got := sessionarchive.ParseBigPicture([]byte(content), false)
	if !strings.HasPrefix(got, "Bodyline1") {
		t.Errorf("got %q does not start with expected prefix", got)
	}
	if strings.Contains(got, "More content past HR") {
		t.Errorf("got %q leaked content past HR block", got)
	}
}

func TestParseBigPicture_BothModesExtractSameBody(t *testing.T) {
	body := "Locked the spec.\n\nMultiline content with paragraphs."
	content := snapshotWithBigPicture(t, "## 1. Big picture — what shipped this session", body)

	raw := sessionarchive.ParseBigPicture([]byte(content), true)
	normalized := sessionarchive.ParseBigPicture([]byte(content), false)

	rawWords := strings.Fields(raw)
	normWords := strings.Fields(normalized)
	if !reflect.DeepEqual(rawWords, normWords) {
		t.Errorf("body content differs between modes:\nraw:        %v\nnormalized: %v", rawWords, normWords)
	}
}

// TestParseBigPicture_ASCIIVariant_CorrectByteOffset is the F1 regression
// pinned by the prompt. The ASCII variant heading takes 46 bytes; the
// em-dash variant takes 47 (em-dash = 3 bytes UTF-8). A buggy
// implementation that hard-codes len(headingEM) for both branches
// overshoots by 1 byte on the ASCII path. With the newline-override
// path also intact, the visible manifestation is masked unless the
// heading is at EOF — but the test as specified in the plan is the
// required acceptance criterion regardless. Stronger variant
// (TestParseBigPicture_ASCIIVariant_HeadingAtEOF_NoOvershoot below)
// covers the case where the bug would actually manifest.
func TestParseBigPicture_ASCIIVariant_CorrectByteOffset(t *testing.T) {
	body := "X-first-char-must-be-preserved"
	content := "---\nagent: t\n---\n## 1. Big picture -- what shipped this session\n" + body + "\n"
	got := sessionarchive.ParseBigPicture([]byte(content), true)
	if got != body {
		t.Errorf("ASCII offset bug regression: got %q, want %q", got, body)
	}
}

// TestParseBigPicture_ASCIIVariant_HeadingAtEOF_NoOvershoot exercises
// the matchedLen-based start branch directly (no trailing newline)
// where the F1 1-byte overshoot WOULD actually manifest. A buggy
// implementation here would either skip the first body byte or panic
// with an out-of-bounds slice. Belt-and-suspenders alongside the
// pinned acceptance test above.
func TestParseBigPicture_ASCIIVariant_HeadingAtEOF_NoOvershoot(t *testing.T) {
	// Heading is the last thing in the content; no trailing newline.
	content := "---\nagent: t\n---\n## 1. Big picture -- what shipped this session"
	got := sessionarchive.ParseBigPicture([]byte(content), true)
	// With correct len(headingASCII), start lands exactly at len(content);
	// content[start:] = "" → TrimSpace = "". A buggy overshoot would
	// produce content[len(content)+1:] which Go runtime-panics. So
	// reaching this point with got=="" proves no overshoot.
	if got != "" {
		t.Errorf("EOF heading should yield empty body; got %q", got)
	}
}
