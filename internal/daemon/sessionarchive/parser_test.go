package sessionarchive_test

import (
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/sessionarchive"
)

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
