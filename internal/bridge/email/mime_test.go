package email_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/bridge/email"
)

// D-B1.6 — MIME compose + parse + bluemonday HTML strip.
// Wire format authority: design-spec §8 (envelope formats).
// Strip policy: bluemonday.StrictPolicy (spec §12 D-B1.6 row).

func makeAgentEnvelope() email.AgentMessageEnvelope {
	return email.AgentMessageEnvelope{
		FromAddr:        "thrum+coordinator_main--ab12cd34@gmail.com",
		FromDisplayName: "coordinator_main @ laptop-falcon",
		ToAddr:          "thrum+doc_bot--3566f932@gmail.com",
		Subject:         "[thrum:laptop-thrum/doc_bot] Re: Q about docstring style",
		MessageID:       "<thrum-ab12cd34abcdef-msg_01KRHX@thrum-mesh.example.com>",
		Date:            time.Date(2026, 5, 13, 18, 0, 0, 0, time.UTC),
		FromDaemonID:    "ab12cd34-1234-5678-9abc-def012345678",
		ToDaemonID:      "3566f932-1234-5678-9abc-def012345678",
		FromAgent:       "coordinator_main",
		ToAgent:         "doc_bot",
		ShortMessageID:  "msg_01KRHX",
		Repo:            "thrum-agents",
		Body:            "Hey, follow-up on the docstring style question — agreed to go with Google-style.",
	}
}

func TestMime_ComposeAgentMessageHeadersComplete(t *testing.T) {
	env := makeAgentEnvelope()
	raw, err := email.ComposeAgentMessage(env)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	got := string(raw)
	// Required substrings. Header NAMES are case-insensitive per RFC 5322
	// §3.6.1 (go-message canonicalizes to Mime-Version; the wire format
	// is correct either way), so we match case-insensitively against the
	// spec-form examples.
	required := []string{
		"From: ",
		"To: ",
		"Subject: [thrum:laptop-thrum/doc_bot] Re: Q about docstring style",
		"Message-Id: <thrum-ab12cd34abcdef-msg_01KRHX@thrum-mesh.example.com>",
		"Date: ",
		"MIME-Version: 1.0",
		"Content-Type: text/plain",
		"X-Thrum-From-Daemon: ab12cd34-1234-5678-9abc-def012345678",
		"X-Thrum-To-Daemon: 3566f932-1234-5678-9abc-def012345678",
		"X-Thrum-From-Agent: coordinator_main",
		"X-Thrum-To-Agent: doc_bot",
		"X-Thrum-Message-Id: msg_01KRHX",
		"X-Thrum-Kind: message",
		"X-Thrum-Hop-Count: 0",
		"X-Thrum-Repo: thrum-agents",
	}
	gotLower := strings.ToLower(got)
	for _, want := range required {
		if !strings.Contains(gotLower, strings.ToLower(want)) {
			t.Errorf("missing required header substring %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, env.Body) {
		t.Errorf("body not present in composed message")
	}
}

func TestMime_ComposeProtocolMessageJSONBody(t *testing.T) {
	payload := map[string]any{
		"new_peer": map[string]any{
			"handle":        "laptop-pixel",
			"daemon_id":     "9f8e7d6c-...",
			"contact_email": "thrum-mesh@gmail.com",
			"vouched_by":    "laptop-falcon",
		},
	}
	pj, _ := json.Marshal(payload)

	env := email.ProtocolEnvelope{
		FromAddr:        "thrum+protocol--ab12cd34@gmail.com",
		FromDisplayName: "laptop-falcon (protocol)",
		ToAddr:          "thrum+protocol--3566f932@gmail.com",
		Subject:         "[thrum:protocol] peer.announce laptop-pixel",
		MessageID:       "<thrum-ab12cd34-proto_01KRHX@thrum-mesh.example.com>",
		Date:            time.Date(2026, 5, 13, 18, 0, 0, 0, time.UTC),
		FromDaemonID:    "ab12cd34-...",
		ToDaemonID:      "3566f932-...",
		Verb:            "peer.announce",
		JSONPayload:     pj,
	}
	raw, err := email.ComposeProtocolMessage(env)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	got := string(raw)
	for _, want := range []string{
		"Content-Type: application/json",
		"X-Thrum-Kind: protocol",
		"X-Thrum-Verb: peer.announce",
		"X-Thrum-Hop-Count: 0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing header %q in:\n%s", want, got)
		}
	}
	// JSON body should parse back.
	bodyStart := strings.Index(got, "\r\n\r\n")
	if bodyStart < 0 {
		bodyStart = strings.Index(got, "\n\n")
	}
	if bodyStart < 0 {
		t.Fatal("no header/body separator")
	}
	body := strings.TrimSpace(got[bodyStart:])
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Errorf("body JSON does not re-parse: %v\nbody=%q", err, body)
	}
}

func TestMime_ParseInboundExtractsTextPlain(t *testing.T) {
	// Multi-part with text/plain + text/html — extract the plain part.
	raw := "From: a@example.com\r\n" +
		"To: b@example.com\r\n" +
		"Subject: hi\r\n" +
		"MIME-Version: 1.0\r\n" +
		"X-Thrum-Kind: message\r\n" +
		"Content-Type: multipart/alternative; boundary=BOUND\r\n\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"plain text version\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<html><body><p>html version</p></body></html>\r\n" +
		"--BOUND--\r\n"

	msg, err := email.ParseInbound([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(msg.Body, "plain text version") {
		t.Errorf("body should be the text/plain part; got %q", msg.Body)
	}
	if strings.Contains(msg.Body, "html version") {
		t.Errorf("body should NOT include html part when plain is available; got %q", msg.Body)
	}
}

func TestMime_ParseInboundStripsHtmlOnly(t *testing.T) {
	raw := "From: a@example.com\r\n" +
		"To: b@example.com\r\n" +
		"Subject: hi\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<html><body><p>plain text from html</p></body></html>\r\n"

	msg, err := email.ParseInbound([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(msg.Body, "plain text from html") {
		t.Errorf("body should extract text from html; got %q", msg.Body)
	}
	if strings.Contains(msg.Body, "<p>") || strings.Contains(msg.Body, "<html>") {
		t.Errorf("body should not contain HTML tags after strip; got %q", msg.Body)
	}
}

func TestMime_ParseInboundDecodesHtmlEntities(t *testing.T) {
	raw := "From: a@example.com\r\n" +
		"To: b@example.com\r\n" +
		"Subject: hi\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>A &amp; B &#38; C &lt; D</p>\r\n"

	msg, err := email.ParseInbound([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(msg.Body, "A & B & C < D") {
		t.Errorf("entities not decoded; got %q", msg.Body)
	}
	if strings.Contains(msg.Body, "&amp;") || strings.Contains(msg.Body, "&#38;") {
		t.Errorf("entity references survived; got %q", msg.Body)
	}
}

func TestMime_ParseInboundElidesScriptStyle(t *testing.T) {
	raw := "From: a@example.com\r\n" +
		"To: b@example.com\r\n" +
		"Subject: hi\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<html><head><style>body{color:red}</style></head>" +
		"<body><script>alert('xss')</script><p>visible text</p></body></html>\r\n"

	msg, err := email.ParseInbound([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(msg.Body, "visible text") {
		t.Errorf("visible text missing; got %q", msg.Body)
	}
	if strings.Contains(msg.Body, "alert") || strings.Contains(msg.Body, "xss") {
		t.Errorf("script content survived strip; got %q", msg.Body)
	}
	if strings.Contains(msg.Body, "color:red") || strings.Contains(msg.Body, "body{") {
		t.Errorf("style content survived strip; got %q", msg.Body)
	}
}

func TestMime_ParseInboundNormalizesWhitespace(t *testing.T) {
	raw := "From: a@example.com\r\n" +
		"To: b@example.com\r\n" +
		"Subject: hi\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>line1</p>\n\n\n\n\n<p>line2</p>\r\n"

	msg, err := email.ParseInbound([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// At most 2 consecutive newlines.
	if strings.Contains(msg.Body, "\n\n\n") {
		t.Errorf("expected runs of \\n collapsed to max 2; got %q", msg.Body)
	}
	if !strings.Contains(msg.Body, "line1") || !strings.Contains(msg.Body, "line2") {
		t.Errorf("expected both lines preserved; got %q", msg.Body)
	}
}

func TestMime_InReplyToReferencesProper(t *testing.T) {
	env := makeAgentEnvelope()
	parent := "<thrum-3566f932abcdef-msg_01KRHY@thrum-mesh.example.com>"
	env.InReplyTo = parent
	env.References = []string{
		"<thrum-3566f932abcdef-msg_01KRHX@thrum-mesh.example.com>",
		"<thrum-ab12cd34abcdef-msg_01KRHY@thrum-mesh.example.com>",
	}

	raw, err := email.ComposeAgentMessage(env)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "In-Reply-To: "+parent) {
		t.Errorf("In-Reply-To missing or wrong; got:\n%s", got)
	}
	for _, ref := range env.References {
		if !strings.Contains(got, ref) {
			t.Errorf("References missing %q; got:\n%s", ref, got)
		}
	}
}

func TestMime_HopCountOverride(t *testing.T) {
	env := makeAgentEnvelope()
	env.HopCount = 3
	raw, err := email.ComposeAgentMessage(env)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(string(raw), "X-Thrum-Hop-Count: 3") {
		t.Errorf("HopCount override not applied; got:\n%s", string(raw))
	}
}

func TestMime_MalformedInputErrorsNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ParseInbound panicked on malformed input: %v", r)
		}
	}()

	// Truncated mid-header — no body separator.
	raw := []byte("From: a@example.com\r\nSubject: trunc")
	_, err := email.ParseInbound(raw)
	if err == nil {
		t.Fatal("expected ErrMimeMalformed for truncated input, got nil")
	}
	if !errors.Is(err, email.ErrMimeMalformed) {
		t.Errorf("expected ErrMimeMalformed, got %v", err)
	}
}
