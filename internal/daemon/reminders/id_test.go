package reminders

import (
	"regexp"
	"strings"
	"testing"
)

func TestMintID_Shape(t *testing.T) {
	re := regexp.MustCompile(`^reminder-[a-z0-9_]+-\d{3}-\d{4}$`)
	id := MintID("docs_bot")
	if !re.MatchString(id) {
		t.Errorf("malformed id: %q", id)
	}
}

func TestMintID_AgentPrefixPreserved(t *testing.T) {
	id := MintID("coordinator_main")
	if !strings.HasPrefix(id, "reminder-coordinator_main-") {
		t.Errorf("missing prefix: %q", id)
	}
}

func TestParseID_ExtractsAgent(t *testing.T) {
	agent, num, err := ParseID("reminder-docs_bot-123-4567")
	if err != nil {
		t.Fatal(err)
	}
	if agent != "docs_bot" {
		t.Errorf("agent = %q, want docs_bot", agent)
	}
	if num != "1234567" {
		t.Errorf("num = %q, want 1234567", num)
	}
}

func TestParseID_RejectsMalformed(t *testing.T) {
	bad := []string{
		"reminder-foo",            // missing numeric
		"reminder-foo-12-3456",    // wrong digit grouping (2-4 instead of 3-4)
		"reminder-foo-123-456",    // wrong digit grouping (3-3 instead of 3-4)
		"foo-bar-123-4567",        // wrong prefix
		"reminder--123-4567",      // empty agent
		"reminder-foo-abc-defg",   // non-numeric
		"reminder-Foo-123-4567",   // uppercase not allowed
		"reminder-foo-bar-1234567",// missing hyphen split
		"",                        // empty
		"reminder-foo-1234-567",   // swapped digit grouping (4-3)
	}
	for _, s := range bad {
		if _, _, err := ParseID(s); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
}

// TestMintID_UniqueAcrossManyMints sanity-checks the entropy source: 10k
// mints in a 10^7 numeric space should collide at well under 1%. Birthday
// paradox lower bound: 10000^2 / (2 * 10^7) ≈ 5 expected collisions.
func TestMintID_UniqueAcrossManyMints(t *testing.T) {
	seen := map[string]bool{}
	collisions := 0
	for range 10000 {
		id := MintID("agent")
		if seen[id] {
			collisions++
		}
		seen[id] = true
	}
	if collisions > 100 {
		t.Errorf("too many collisions: %d (want <100)", collisions)
	}
}

// TestParseID_RoundTrip verifies MintID output parses cleanly. Belt-and-
// suspenders coverage: any future regex tightening that breaks self-parsing
// shows up here.
func TestParseID_RoundTrip(t *testing.T) {
	id := MintID("agent_foo")
	agent, num, err := ParseID(id)
	if err != nil {
		t.Fatalf("round-trip failed: id=%q err=%v", id, err)
	}
	if agent != "agent_foo" {
		t.Errorf("agent = %q, want agent_foo", agent)
	}
	if len(num) != 7 {
		t.Errorf("num length = %d, want 7", len(num))
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			t.Errorf("non-digit in num %q", num)
			break
		}
	}
}
