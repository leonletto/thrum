// Package email implements the v0.11 email transport substrate. It mirrors
// internal/bridge/telegram in shape (lifecycle, msgmap, wsclient) and ships
// the mesh + plus-addressing surface under the mailbox-access trust root
// (Scope B). Cryptographic peer identity is deferred to E18 / v0.11.x.
package email

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-message"
	"github.com/microcosm-cc/bluemonday"
)

// ErrMimeMalformed is returned by ParseInbound when the input cannot be
// interpreted as an RFC 5322 / MIME message. Callers (inbound routing)
// drop the message and log; they do NOT crash.
var ErrMimeMalformed = errors.New("malformed MIME input")

// AgentMessageEnvelope describes a cross-daemon agent message. Field
// names track design-spec §8 — the FromAddr/ToAddr carry plus-addressed
// mailboxes (thrum+<agent>--<short>@host) and the X-Thrum-* headers
// carry routing metadata.
type AgentMessageEnvelope struct {
	FromAddr        string
	FromDisplayName string
	ToAddr          string
	Subject         string

	MessageID  string // full <thrum-...@host> form for Message-Id
	InReplyTo  string // optional, full <...@host> form
	References []string

	Date time.Time

	FromDaemonID   string
	ToDaemonID     string
	FromAgent      string
	ToAgent        string
	ShortMessageID string // X-Thrum-Message-Id (e.g., msg_01KRHX)
	HopCount       int    // X-Thrum-Hop-Count; zero default (origination)
	Repo           string

	Body string // text/plain UTF-8
}

// ProtocolEnvelope describes a peer.* protocol chatter message.
// Per spec §8 protocol chatter is always point-to-point; HopCount is
// always 0 on outbound. JSONPayload is the pre-serialized body.
type ProtocolEnvelope struct {
	FromAddr        string
	FromDisplayName string
	ToAddr          string
	Subject         string
	MessageID       string
	Date            time.Time

	FromDaemonID string
	ToDaemonID   string
	Verb         string // X-Thrum-Verb (e.g., peer.announce)

	JSONPayload []byte
}

// ParsedMessage is the parsed form of an inbound message. Headers is
// the canonical-case header map (e.g., "X-Thrum-Kind"); Body is the
// text/plain body (HTML-stripped via bluemonday StrictPolicy if the
// input was HTML-only).
type ParsedMessage struct {
	Headers map[string]string
	Body    string
	Kind    string // "message" | "protocol"
	Verb    string // populated when Kind=protocol
}

// ComposeAgentMessage builds an RFC 5322 message for a cross-daemon
// agent-to-agent send. All required X-Thrum-* headers are populated
// from the envelope.
func ComposeAgentMessage(env AgentMessageEnvelope) ([]byte, error) {
	hdr := newHeader()
	writeAddrHeader(hdr, "From", env.FromAddr, env.FromDisplayName)
	writeAddrHeader(hdr, "To", env.ToAddr, "")
	hdr.Set("Subject", env.Subject)
	hdr.Set("Message-Id", env.MessageID)
	hdr.Set("Date", env.Date.UTC().Format(time.RFC1123Z))
	hdr.Set("MIME-Version", "1.0")
	hdr.Set("Content-Type", `text/plain; charset="utf-8"`)

	if env.InReplyTo != "" {
		hdr.Set("In-Reply-To", env.InReplyTo)
	}
	if len(env.References) > 0 {
		hdr.Set("References", strings.Join(env.References, " "))
	}

	hdr.Set("X-Thrum-From-Daemon", env.FromDaemonID)
	hdr.Set("X-Thrum-To-Daemon", env.ToDaemonID)
	hdr.Set("X-Thrum-From-Agent", env.FromAgent)
	hdr.Set("X-Thrum-To-Agent", env.ToAgent)
	hdr.Set("X-Thrum-Message-Id", env.ShortMessageID)
	hdr.Set("X-Thrum-Kind", "message")
	hdr.Set("X-Thrum-Hop-Count", strconv.Itoa(env.HopCount))
	hdr.Set("X-Thrum-Repo", env.Repo)

	return assemble(hdr, []byte(env.Body))
}

// ComposeProtocolMessage builds a protocol chatter message
// (Content-Type: application/json). HopCount is always 0 on outbound
// per spec §8.
func ComposeProtocolMessage(env ProtocolEnvelope) ([]byte, error) {
	if !json.Valid(env.JSONPayload) {
		return nil, fmt.Errorf("compose protocol: payload is not valid JSON")
	}
	hdr := newHeader()
	writeAddrHeader(hdr, "From", env.FromAddr, env.FromDisplayName)
	writeAddrHeader(hdr, "To", env.ToAddr, "")
	hdr.Set("Subject", env.Subject)
	hdr.Set("Message-Id", env.MessageID)
	hdr.Set("Date", env.Date.UTC().Format(time.RFC1123Z))
	hdr.Set("MIME-Version", "1.0")
	hdr.Set("Content-Type", `application/json; charset="utf-8"`)

	hdr.Set("X-Thrum-From-Daemon", env.FromDaemonID)
	hdr.Set("X-Thrum-To-Daemon", env.ToDaemonID)
	hdr.Set("X-Thrum-Kind", "protocol")
	hdr.Set("X-Thrum-Verb", env.Verb)
	hdr.Set("X-Thrum-Hop-Count", "0")

	return assemble(hdr, env.JSONPayload)
}

// ParseInbound parses a raw MIME message and returns the canonical
// headers plus a text/plain body. HTML-only bodies are stripped via
// bluemonday.StrictPolicy + entity decoded + whitespace normalized.
// Malformed input returns ErrMimeMalformed (wrapped); never panics.
func ParseInbound(raw []byte) (*ParsedMessage, error) {
	defer func() {
		// Last-resort recover: the go-message library is well-tested
		// but the daemon's inbound path MUST NOT crash on hostile or
		// truncated input — protocol-level dedup runs after parse, so
		// a panic here would hang the bridge.
		_ = recover()
	}()

	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrMimeMalformed)
	}

	// Require a header/body separator. RFC 5322 mandates CRLF CRLF; we also
	// accept LF LF since real MTAs sometimes normalize. Truncated input
	// without a separator is rejected here rather than silently producing
	// an empty-body parse — the L4 dedup step would otherwise see a
	// Message-Id-less ghost and drop it without an error trail.
	if !bytes.Contains(raw, []byte("\r\n\r\n")) && !bytes.Contains(raw, []byte("\n\n")) {
		return nil, fmt.Errorf("%w: missing header/body separator", ErrMimeMalformed)
	}

	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		// Empty Content-Type / charset warnings come back as message.UnknownCharsetError —
		// safe to proceed if entity is non-nil. Truncated input yields a hard
		// io.ErrUnexpectedEOF or similar that we surface as malformed.
		if entity == nil {
			return nil, fmt.Errorf("%w: %v", ErrMimeMalformed, err)
		}
	}

	headers := make(map[string]string)
	hf := entity.Header.Fields()
	for hf.Next() {
		// Canonical-case the key; later duplicates win (RFC 5322 doesn't
		// strictly forbid them but our consumers expect one value each).
		headers[mimeKey(hf.Key())] = hf.Value()
	}

	body, err := extractBody(entity)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMimeMalformed, err)
	}

	return &ParsedMessage{
		Headers: headers,
		Body:    body,
		Kind:    headers["X-Thrum-Kind"],
		Verb:    headers["X-Thrum-Verb"],
	}, nil
}

// --- internal helpers ---

func newHeader() *message.Header {
	var h message.Header
	return &h
}

// writeAddrHeader formats an address header. RFC 5322 quoted-display-name
// form is delegated to net/mail.Address — keeps Unicode + special chars
// safe without a hand-rolled quoter.
func writeAddrHeader(h *message.Header, field, addr, display string) {
	if display == "" {
		h.Set(field, addr)
		return
	}
	a := mail.Address{Name: display, Address: addr}
	h.Set(field, a.String())
}

// assemble emits a raw message with CRLF line endings, header block
// followed by body.
func assemble(h *message.Header, body []byte) ([]byte, error) {
	entity, err := message.New(*h, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("assemble entity: %w", err)
	}
	var buf bytes.Buffer
	if err := entity.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("assemble write: %w", err)
	}
	return buf.Bytes(), nil
}

// mimeKey canonicalizes header keys to the standard X-Thrum-* casing
// without depending on Go's textproto package (which lowercases the
// X-Thrum- prefix incorrectly for our tests). Falls back to
// CanonicalMIMEHeaderKey for non-Thrum headers.
func mimeKey(k string) string {
	// go-message returns keys already in canonical case; we keep an
	// explicit guard here so future swap-outs don't surprise callers.
	if strings.EqualFold(k[:min(len(k), 8)], "x-thrum-") {
		// Re-canonicalize: X-Thrum-Kind, X-Thrum-Hop-Count, etc.
		parts := strings.Split(k, "-")
		for i, p := range parts {
			if len(p) == 0 {
				continue
			}
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
		// "Thrum" should stay capitalized as written.
		return strings.Join(parts, "-")
	}
	return k
}

// extractBody walks a parsed entity and returns the plain-text body.
// Multi-part: prefer text/plain over text/html. text/html-only: bluemonday
// strip + entity decode + whitespace normalize.
func extractBody(entity *message.Entity) (string, error) {
	mediaType, _, err := entity.Header.ContentType()
	if err != nil {
		// Default to text/plain per RFC 2046 §4.1.2.
		mediaType = "text/plain"
	}

	if mr := entity.MultipartReader(); mr != nil {
		return extractMultipart(mr)
	}

	bodyBytes, err := io.ReadAll(entity.Body)
	if err != nil {
		return "", err
	}

	switch mediaType {
	case "text/plain":
		return string(bodyBytes), nil
	case "text/html":
		return stripHTML(bodyBytes), nil
	default:
		// Unknown single-part media type: pass through raw. Inbound
		// routing will decide whether to drop or relay.
		return string(bodyBytes), nil
	}
}

func extractMultipart(mr message.MultipartReader) (string, error) {
	var plain, htmlBody string

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		mediaType, _, _ := part.Header.ContentType()
		bodyBytes, err := io.ReadAll(part.Body)
		if err != nil {
			return "", err
		}

		switch mediaType {
		case "text/plain":
			if plain == "" {
				plain = string(bodyBytes)
			}
		case "text/html":
			if htmlBody == "" {
				htmlBody = string(bodyBytes)
			}
		}
	}

	if plain != "" {
		return plain, nil
	}
	if htmlBody != "" {
		return stripHTML([]byte(htmlBody)), nil
	}
	return "", nil
}

// stripHTML applies bluemonday StrictPolicy (all tags + attributes
// elided), decodes HTML entities, and collapses runs of newlines to
// max 2. The order matters: strip first (which removes <script> /
// <style> bodies entirely), then decode entities in the surviving text.
var (
	htmlSanitizer    = bluemonday.StrictPolicy()
	multiNewlineExpr = regexp.MustCompile(`\n{3,}`)
)

func stripHTML(in []byte) string {
	stripped := htmlSanitizer.SanitizeBytes(in)
	decoded := html.UnescapeString(string(stripped))
	normalized := multiNewlineExpr.ReplaceAllString(decoded, "\n\n")
	return strings.TrimSpace(normalized)
}
