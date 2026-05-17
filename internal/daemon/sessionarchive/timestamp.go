package sessionarchive

import (
	"strings"
	"time"
)

// FormatTimestamp renders t as the canonical session-archive filename
// timestamp: YYYYMMDDTHHMMSSmmmZ (compact RFC 3339 millis, no colons,
// no dots). Caller's time is normalized to UTC.
//
// Spec §5.x filename grammar requires exactly 17 characters (16 +
// trailing Z) so the lexical sort over directory entries equals the
// chronological sort — load-bearing for `thrum agent sessions list`
// ordering without per-file frontmatter parsing.
//
// Example:
//
//	in:  2026-05-17T15:32:18.421Z (UTC)
//	out: 20260517T153218421Z
func FormatTimestamp(t time.Time) string {
	s := t.UTC().Format("20060102T150405.000Z")
	return strings.ReplaceAll(s, ".", "")
}
