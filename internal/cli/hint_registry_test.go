package cli

import (
	"errors"
	"maps"
	"strings"
	"testing"
)

// resetHintRegistry clears the package-level registry. Test-only helper.
//
// init() calls in hint_sources_*.go register tmux.create, send, and init
// at package load time. Those registrations are visible to integration-style
// tests that rely on Collect() returning the real hint sources. A test that
// simply zeroed the map would leak an empty registry into later tests.
// t.Cleanup re-registers the production sources when the calling test ends,
// restoring the original state for any subsequent test in the same package.
func resetHintRegistry(t *testing.T) {
	t.Helper()
	hintRegistryMu.Lock()
	// Snapshot before zeroing so Cleanup can put them back.
	previous := make(map[string]HintSource, len(hintRegistry))
	maps.Copy(previous, hintRegistry)
	hintRegistry = map[string]HintSource{}
	hintRegistryMu.Unlock()

	t.Cleanup(func() {
		hintRegistryMu.Lock()
		defer hintRegistryMu.Unlock()
		hintRegistry = previous
	})
}

func TestRegisterAndCollect(t *testing.T) {
	resetHintRegistry(t)
	RegisterHintSource("test.cmd", func(ctx HintCtx) []Hint {
		return []Hint{{Code: "test.cmd.fired", Severity: SeverityInfo, Message: "m"}}
	})
	hs := Collect(HintCtx{Command: "test.cmd"})
	if len(hs) != 1 || hs[0].Code != "test.cmd.fired" {
		t.Fatalf("Collect returned %+v", hs)
	}
}

func TestCollectUnknownCommandReturnsNil(t *testing.T) {
	resetHintRegistry(t)
	if hs := Collect(HintCtx{Command: "does.not.exist"}); hs != nil {
		t.Errorf("expected nil for unknown command, got %+v", hs)
	}
}

func TestRegisterOverwritesSameCommand(t *testing.T) {
	resetHintRegistry(t)
	RegisterHintSource("test.cmd", func(HintCtx) []Hint { return []Hint{{Code: "first"}} })
	RegisterHintSource("test.cmd", func(HintCtx) []Hint { return []Hint{{Code: "second"}} })
	hs := Collect(HintCtx{Command: "test.cmd"})
	if len(hs) != 1 || hs[0].Code != "second" {
		t.Errorf("re-registration did not overwrite: got %+v", hs)
	}
}

func TestHandlePreActionAllowsEmpty(t *testing.T) {
	if err := HandlePreAction(nil, false); err != nil {
		t.Errorf("empty hints must not block: %v", err)
	}
	if err := HandlePreAction([]Hint{}, false); err != nil {
		t.Errorf("empty hints must not block: %v", err)
	}
}

func TestHandlePreActionBlocksWarnWithoutForce(t *testing.T) {
	hs := []Hint{{Code: "a.b", Severity: SeverityWarn, Message: "blocks"}}
	err := HandlePreAction(hs, false)
	if err == nil {
		t.Fatal("expected abort, got nil")
	}
	var he *HintAbortError
	if !errors.As(err, &he) {
		t.Errorf("expected *HintAbortError, got %T (%v)", err, err)
	}
	if len(he.Hints) != 1 || he.Hints[0].Code != "a.b" {
		t.Errorf("abort error did not carry blocking hints: %+v", he.Hints)
	}
}

func TestHandlePreActionAllowsInfoWithoutForce(t *testing.T) {
	hs := []Hint{{Code: "a.b", Severity: SeverityInfo, Message: "tip"}}
	if err := HandlePreAction(hs, false); err != nil {
		t.Errorf("info hint must not block: %v", err)
	}
}

func TestHandlePreActionForceOverridesAllowedWarn(t *testing.T) {
	hs := []Hint{{Code: "a.b", Severity: SeverityWarn, Message: "recoverable", AllowForce: true}}
	if err := HandlePreAction(hs, true); err != nil {
		t.Errorf("--force must override AllowForce=true warn: %v", err)
	}
}

func TestHandlePreActionForceDoesNotOverrideHardRefusal(t *testing.T) {
	hs := []Hint{{Code: "a.b", Severity: SeverityWarn, Message: "hard-no", AllowForce: false}}
	if err := HandlePreAction(hs, true); err == nil {
		t.Error("--force must NOT override AllowForce=false warn (hard refusal)")
	}
}

// Mixed severities: info should never block even when a warn alongside it does.
// Abort must carry only the blocking warn.
func TestHandlePreActionAbortCarriesOnlyBlockers(t *testing.T) {
	hs := []Hint{
		{Code: "info.one", Severity: SeverityInfo, Message: "i"},
		{Code: "warn.one", Severity: SeverityWarn, Message: "w", AllowForce: false},
		{Code: "warn.ok", Severity: SeverityWarn, Message: "w2", AllowForce: true}, // passes with force
	}
	err := HandlePreAction(hs, true)
	if err == nil {
		t.Fatal("expected abort")
	}
	var he *HintAbortError
	if !errors.As(err, &he) {
		t.Fatalf("expected *HintAbortError, got %T", err)
	}
	if len(he.Hints) != 1 || he.Hints[0].Code != "warn.one" {
		t.Errorf("abort must carry only the un-force-able blocker, got %+v", he.Hints)
	}
}

func TestHintAbortErrorMessageListsCodes(t *testing.T) {
	err := &HintAbortError{Hints: []Hint{
		{Code: "a.b"},
		{Code: "c.d"},
	}}
	got := err.Error()
	if got == "" {
		t.Error("HintAbortError.Error() returned empty")
	}
	for _, want := range []string{"a.b", "c.d"} {
		if !strings.Contains(got, want) {
			t.Errorf("HintAbortError message %q does not contain %q", got, want)
		}
	}
}

