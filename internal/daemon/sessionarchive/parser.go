package sessionarchive

import (
	"regexp"
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

// ParseBigPicture extracts the §1 "Big picture — what shipped this
// session" body from a snapshot per spec §6A.4.
//
// raw=false (default for CLI list / discovery hint / JSON output)
// collapses internal whitespace runs (newlines, tabs, multi-spaces)
// to a single space. raw=true (CLI --verbose) preserves original
// line breaks + leading whitespace.
//
// Returns empty string when the §1 section is missing, malformed,
// or has an empty body.
//
// The heading admits two variants — em-dash "—" (3 bytes UTF-8)
// and ASCII "--" (2 bytes) — so agent-authored snapshots typed
// without IME help still parse. The matchedLen-tracking branch is
// the F1 regression fix from spec v3: hard-coding len(headingEM)
// for both branches overshoots the ASCII path by 1 byte when the
// heading-as-EOF fallback fires (no newline after the heading).
func ParseBigPicture(content []byte, raw bool) string {
	return extractBigPicture(string(content), raw)
}

func extractBigPicture(content string, raw bool) string {
	const headingEM = "## 1. Big picture — what shipped this session"
	const headingASCII = "## 1. Big picture -- what shipped this session"

	var idx int
	var matchedLen int
	if i := strings.Index(content, headingEM); i != -1 {
		idx = i
		matchedLen = len(headingEM)
	} else if i := strings.Index(content, headingASCII); i != -1 {
		idx = i
		matchedLen = len(headingASCII)
	} else {
		return ""
	}

	// Move past the heading line. If there's a newline after the
	// heading, use it; otherwise (heading at EOF) fall back to
	// matchedLen — body will be empty in that case.
	start := idx + matchedLen
	if newline := strings.IndexByte(content[idx:], '\n'); newline != -1 {
		start = idx + newline + 1
	}

	end := len(content)
	rest := content[start:]
	if nextH := strings.Index(rest, "\n## "); nextH != -1 {
		end = start + nextH
	}
	if nextHR := strings.Index(rest, "\n---\n"); nextHR != -1 && start+nextHR < end {
		end = start + nextHR
	}

	body := strings.TrimSpace(content[start:end])
	if raw {
		return body
	}
	return collapseWhitespace(body)
}

// whitespaceRun compiles once at package init (not per call) so the
// hot CLI list / discovery-hint paths don't pay the regex-compile cost.
var whitespaceRun = regexp.MustCompile(`\s+`)

func collapseWhitespace(s string) string {
	return whitespaceRun.ReplaceAllString(s, " ")
}
