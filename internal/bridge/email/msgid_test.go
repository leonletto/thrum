package email_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/bridge/email"
)

// D-B1.7 — Message-Id format pin per design-spec §8. The thrum-prefixed
// Message-Id encodes routing-grade identity (daemon-id-short + thrum
// msg-id) inside an RFC-2822 envelope so inbound replies can be threaded
// even after a server round-trip strips X-Thrum-* custom headers.

func TestMsgid_GenerateValidFormat(t *testing.T) {
	got := email.GenerateMessageId("ab12cd34", "msg_01KRHX", "thrum-mesh.example.com")

	// RFC 2822 Message-Id: <local-part@domain> where local-part contains
	// no whitespace and no angle brackets. Our local-part is
	// "thrum-<short>-<msgid>".
	re := regexp.MustCompile(`^<thrum-[A-Za-z0-9]+-[A-Za-z0-9_]+@[A-Za-z0-9.-]+>$`)
	if !re.MatchString(got) {
		t.Errorf("Message-Id %q does not match canonical format", got)
	}

	for _, want := range []string{"ab12cd34", "msg_01KRHX", "thrum-mesh.example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("Message-Id %q missing expected component %q", got, want)
		}
	}
}

func TestMsgid_ParseRoundTrip(t *testing.T) {
	id := email.GenerateMessageId("ab12cd34", "msg_01KRHX", "thrum-mesh.example.com")
	daemonShort, msgID, ok := email.ParseMessageId(id)
	if !ok {
		t.Fatalf("ParseMessageId(%q) returned ok=false", id)
	}
	if daemonShort != "ab12cd34" {
		t.Errorf("daemonShort=%q, want ab12cd34", daemonShort)
	}
	if msgID != "msg_01KRHX" {
		t.Errorf("msgID=%q, want msg_01KRHX", msgID)
	}
}

func TestMsgid_ParseRejectsNonThrumIds(t *testing.T) {
	cases := []string{
		"<user@example.com>",                // not thrum-prefixed
		"<thrum-onlyone@host.com>",          // missing msg segment
		"thrum-ab12cd34-msg@host.com",       // missing angle brackets
		"",                                  // empty
	}
	for _, c := range cases {
		if _, _, ok := email.ParseMessageId(c); ok {
			t.Errorf("ParseMessageId(%q) should reject, got ok=true", c)
		}
	}
}

func TestMsgid_UniquenessAcross1000Calls(t *testing.T) {
	// GenerateMessageId with a unique msgID component each call produces
	// distinct ids — the function is pure passthrough on msgID, so
	// uniqueness is fundamentally the caller's responsibility. This test
	// confirms the helper doesn't accidentally collapse distinct inputs.
	seen := make(map[string]struct{}, 1000)
	for i := range 1000 {
		// Synthesize msgID with an embedded counter — caller's typical
		// pattern uses ULIDs/Snowflakes which trivially achieve this.
		msgID := "msg_" + ulidLike(i)
		id := email.GenerateMessageId("ab12cd34", msgID, "host.example.com")
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate Message-Id at i=%d: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

// ulidLike emits a deterministic-but-distinct 16-char string for test
// uniqueness checks. Mirrors the shape of msg_01KRHX... without pulling
// in the real ULID library — the email package treats the msgID
// component as an opaque token, so any unique-per-call string suffices.
func ulidLike(seed int) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var b [16]byte
	v := uint64(seed) + 0x123456789abcdef
	for i := 15; i >= 0; i-- {
		b[i] = alphabet[v%32]
		v /= 32
	}
	return string(b[:])
}
