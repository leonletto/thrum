package sessionarchive

import (
	"strings"
	"time"
)

// ParseSavedAtFrontmatter extracts the `saved_at` field from a snapshot
// file's YAML frontmatter (spec §4.4). Returns the fallback on any
// failure mode (missing frontmatter, missing closing delimiter, missing
// key, malformed RFC 3339 value).
//
// Grammar (spec §4.4):
//  1. content must start with "---\n"
//  2. lines up to the next "---\n" delimiter are <key>: <value> pairs
//  3. locate "saved_at" (case-sensitive), trim surrounding whitespace
//  4. parse as RFC 3339 nano; malformed → fallback
//
// Single-pass scanner; no YAML library dependency. The downstream
// caller (HandleSessionArchive) chains: parseSavedAt(content, mtime),
// then mtime→time.Now() if mtime itself can't be read.
//
// parseBigPicture lives alongside this function (added by Task 4
// thrum-6qmf.15.9); both consumers operate on the same frontmatter
// block but parseBigPicture descends into the body section instead.
func ParseSavedAtFrontmatter(content string, fallback time.Time) time.Time {
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return fallback
	}
	block, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return fallback
	}
	for line := range strings.SplitSeq(block, "\n") {
		value, ok := strings.CutPrefix(line, "saved_at:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return fallback
		}
		return parsed
	}
	return fallback
}
