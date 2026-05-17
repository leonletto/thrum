package mirror

import (
	"errors"
	"sort"
	"sync"
	"testing"
)

func TestAdapter_LookupClaude(t *testing.T) {
	t.Parallel()

	entry, err := Lookup("claude")
	if err != nil {
		t.Fatalf("Lookup(claude): unexpected error %v", err)
	}
	if entry == nil {
		t.Fatalf("Lookup(claude): expected populated entry, got nil")
	}
	if entry.MirrorPath != ".claude/skills" {
		t.Errorf("MirrorPath = %q, want %q", entry.MirrorPath, ".claude/skills")
	}
	if entry.ReloadCommand != "/reload-plugins" {
		t.Errorf("ReloadCommand = %q, want %q", entry.ReloadCommand, "/reload-plugins")
	}
}

func TestAdapter_LookupNullEntry(t *testing.T) {
	t.Parallel()

	// Every null-entry runtime must return (nil, nil) so the worker
	// distinguishes "registered but no mirror surface in v0.11" from
	// "unknown runtime name". codex is the canonical fixture.
	for _, runtime := range []string{"codex", "opencode", "kiro", "cursor"} {
		entry, err := Lookup(runtime)
		if err != nil {
			t.Errorf("Lookup(%s): unexpected error %v", runtime, err)
		}
		if entry != nil {
			t.Errorf("Lookup(%s): expected nil entry, got %+v", runtime, entry)
		}
	}
}

func TestAdapter_LookupUnknown(t *testing.T) {
	t.Parallel()

	_, err := Lookup("nonexistent-runtime")
	if !errors.Is(err, ErrUnknownRuntime) {
		t.Fatalf("Lookup(nonexistent-runtime): expected ErrUnknownRuntime, got %v", err)
	}
}

// TestAdapter_LookupConcurrent confirms the read-only-map guarantee
// AC requires. 100 goroutines call Lookup concurrently; race detector
// flags any unsynchronized write. The test is the pinning regression
// for "no init() side effects, no runtime mutation" — if a future
// change introduces a write path (e.g. a hot-reload registration),
// this test will surface the data race immediately.
func TestAdapter_LookupConcurrent(t *testing.T) {
	t.Parallel()

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _ = Lookup("claude")
			_, _ = Lookup("codex")
			_, _ = Lookup("unknown")
		}()
	}
	wg.Wait()
}

// TestAdapter_KnownRuntimes asserts the diagnostics-facing surface
// returns every registered runtime including null-entry ones. Pinned
// so a future PR that adds a runtime can't accidentally hide it from
// the config-validator path.
func TestAdapter_KnownRuntimes(t *testing.T) {
	t.Parallel()

	got := KnownRuntimes()
	sort.Strings(got)
	want := []string{"claude", "codex", "cursor", "kiro", "opencode"}
	if len(got) != len(want) {
		t.Fatalf("KnownRuntimes len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("KnownRuntimes[%d] = %q, want %q", i, got[i], w)
		}
	}
}
