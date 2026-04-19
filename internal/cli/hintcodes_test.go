package cli

import (
	"regexp"
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

// TestCatalogSize locks the pilot catalog size. Bump when adding codes
// in a deliberate review. Pilot catalog is 8:
// 6 tmux.create (session-exists, not-a-worktree, identity-exists-alive,
// identity-exists-stale, next-launch, identity-replaced) +
// send.recipient-stale + init.next-quickstart.
func TestCatalogSize(t *testing.T) {
	const expected = 8
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
