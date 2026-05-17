package email

import "strings"

// GenerateMessageId builds an RFC 2822 Message-Id for an outbound thrum
// message. Format (design-spec §8):
//
//	<thrum-<daemonShort>-<msgID>@<host>>
//
// daemonShort is the routing identifier (typically the 8-char prefix of
// the full daemon UUID); msgID is the canonical thrum message id
// (typically a ULID like msg_01KRHX...); host is the mail-domain
// component the receiving mailbox lives under.
//
// Uniqueness is the caller's responsibility — this helper is a pure
// formatter. msgIDs supplied by the daemon's ULID generator already
// guarantee uniqueness; this function only ensures the wire format.
func GenerateMessageId(daemonShort, msgID, host string) string {
	return "<thrum-" + daemonShort + "-" + msgID + "@" + host + ">"
}

// ParseMessageId reverses GenerateMessageId, returning the daemonShort
// + msgID components. ok=false for anything that doesn't match the
// thrum-prefixed format — non-thrum Message-Ids (operator replies from
// a normal mail client) parse as ok=false, which the inbound routing
// path treats as "not threaded to a known thrum message; fall back to
// supervisor relay".
//
// The parse assumes daemonShort contains no '-' (the segment after
// 'thrum-' is split on the first '-'). msgID may contain '_' or other
// characters; everything between the first '-' after 'thrum-' and the
// '@' is treated as msgID.
func ParseMessageId(s string) (daemonShort, msgID string, ok bool) {
	if len(s) < 2 || s[0] != '<' || s[len(s)-1] != '>' {
		return "", "", false
	}
	inner := s[1 : len(s)-1]

	atIdx := strings.LastIndex(inner, "@")
	if atIdx < 0 {
		return "", "", false
	}
	localPart := inner[:atIdx]

	const prefix = "thrum-"
	if !strings.HasPrefix(localPart, prefix) {
		return "", "", false
	}
	rest := localPart[len(prefix):]

	daemonShort, msgID, ok = strings.Cut(rest, "-")
	if !ok || daemonShort == "" || msgID == "" {
		return "", "", false
	}
	return daemonShort, msgID, true
}
