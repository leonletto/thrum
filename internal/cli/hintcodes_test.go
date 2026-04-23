package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// codeRe matches the command.subcommand.slug format. One to three dots,
// lowercase letters or hyphens only between them.
var codeRe = regexp.MustCompile(`^[a-z]+(\.[a-z-]+){1,3}$`)

// TestAllCodesMatchFormat ensures every entry in AllHintCodes follows the
// stable dotted-lowercase shape. Grep/dedup tooling relies on this.
func TestAllCodesMatchFormat(t *testing.T) {
	for _, code := range AllHintCodes {
		if !codeRe.MatchString(code) {
			t.Errorf("hint code %q does not match format %s", code, codeRe.String())
		}
	}
}

// TestNoDuplicateCodes ensures every code in AllHintCodes is unique.
func TestNoDuplicateCodes(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range AllHintCodes {
		if seen[c] {
			t.Errorf("duplicate hint code %q in AllHintCodes", c)
		}
		seen[c] = true
	}
}

// TestCatalogSize locks the catalog size. Bump when adding codes in a
// deliberate review. Current catalog is 12:
// 6 tmux.create (session-exists, not-a-worktree, identity-exists-alive,
// identity-exists-stale, next-launch, identity-replaced),
// send.recipient-stale, init.next-quickstart, and
// 4 snapshot.save (no-jsonl, no-pid, extract-failed, jsonl-not-found)
// added for thrum-ufv5.7 to surface silent failures of
// thrum tmux snapshot save, where jsonl-not-found arrived in the
// dual-review fixup to distinguish typo'd --jsonl paths from read/parse
// failures.
func TestCatalogSize(t *testing.T) {
	const expected = 12
	if got := len(AllHintCodes); got != expected {
		t.Errorf("AllHintCodes size = %d, want %d (update this test deliberately when catalog grows)", got, expected)
	}
}

// TestRecipientStaleThresholdIsPositive guards against an accidental zero-threshold.
func TestRecipientStaleThresholdIsPositive(t *testing.T) {
	if RecipientStaleThreshold <= 0 {
		t.Errorf("RecipientStaleThreshold = %v, want > 0", RecipientStaleThreshold)
	}
}

// constNameForCode maps each catalog string to the exported Go const name
// that carries it. Parallel list (duplication intentional) so the L3 grep
// stays grep-friendly; the alternative (reflection over the package) would
// obscure the linkage.
var constNameForCode = map[string]string{
	HintTmuxCreateSessionExists:       "HintTmuxCreateSessionExists",
	HintTmuxCreateNotAWorktree:        "HintTmuxCreateNotAWorktree",
	HintTmuxCreateIdentityExistsAlive: "HintTmuxCreateIdentityExistsAlive",
	HintTmuxCreateIdentityExistsStale: "HintTmuxCreateIdentityExistsStale",
	HintTmuxCreateNextLaunch:          "HintTmuxCreateNextLaunch",
	HintTmuxCreateIdentityReplaced:    "HintTmuxCreateIdentityReplaced",
	HintSendRecipientStale:            "HintSendRecipientStale",
	HintInitNextQuickstart:            "HintInitNextQuickstart",
	HintSnapshotSaveNoJSONL:           "HintSnapshotSaveNoJSONL",
	HintSnapshotSaveNoPID:             "HintSnapshotSaveNoPID",
	HintSnapshotSaveExtractFailed:     "HintSnapshotSaveExtractFailed",
	HintSnapshotSaveJSONLNotFound:     "HintSnapshotSaveJSONLNotFound",
}

// TestEveryCodeHasSourceReference ensures every code in AllHintCodes is
// referenced by at least one hint_sources_*.go file. Catches the drift
// where a code is added to the registry but no source emits it.
func TestEveryCodeHasSourceReference(t *testing.T) {
	sourceBlob := readPackageFiles(t, "hint_sources_")
	for _, code := range AllHintCodes {
		name, ok := constNameForCode[code]
		if !ok {
			t.Errorf("constNameForCode missing entry for %q", code)
			continue
		}
		if !bytes.Contains(sourceBlob, []byte(name)) {
			t.Errorf("hint code %q (const %s) is not referenced by any hint_sources_*.go file", code, name)
		}
	}
}

// TestEveryCodeHasUnitTestReference ensures every code in AllHintCodes is
// referenced by at least one hint_*_test.go file. Catches the drift where
// a code is added without a positive-fire test row.
func TestEveryCodeHasUnitTestReference(t *testing.T) {
	testBlob := readPackageFiles(t, "hint_")
	for _, code := range AllHintCodes {
		name, ok := constNameForCode[code]
		if !ok {
			continue
		}
		if !bytes.Contains(testBlob, []byte(name)) {
			t.Errorf("hint code %q (const %s) is not referenced by any hint_*_test.go file", code, name)
		}
	}
}

// readPackageFiles returns the concatenated bytes of all files in the
// current package matching the given prefix. For L3 tests: prefix
// "hint_sources_" returns all source files; prefix "hint_" + suffix
// "_test.go" requires an extra filter — we do suffix filtering inline.
func readPackageFiles(t *testing.T, prefix string) []byte {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read cwd: %v", err)
	}
	var out bytes.Buffer
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		// For source-reference check we want NON-test files; for test
		// reference check we want test files. The "hint_sources_" prefix
		// makes source files exclusive; for the broader "hint_" prefix
		// we need to restrict to _test.go since both are present.
		if prefix == "hint_" && !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		if prefix == "hint_sources_" && strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out.Write(b)
		out.WriteByte('\n')
	}
	if out.Len() == 0 {
		t.Fatalf("readPackageFiles(%q) matched zero files — test is broken", prefix)
	}
	return out.Bytes()
}
