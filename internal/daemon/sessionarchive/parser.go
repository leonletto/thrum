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
// Single-pass scanner; no YAML library dependency.
//
// Implementation note: this thin wrapper exists for callers that already
// have a sensible fallback value (mtime, current time) and want a single
// time.Time back. Callers that need to KNOW whether the parse succeeded
// — to chain a multi-step fallback (parse → mtime → now) and observe
// where each layer landed — should use parseSavedAtFrontmatterOK
// directly. Archive() uses the OK form so its three-layer fallback
// chain is explicit and testable per spec §3.2 step 6.
func ParseSavedAtFrontmatter(content string, fallback time.Time) time.Time {
	if ts, ok := parseSavedAtFrontmatterOK(content); ok {
		return ts
	}
	return fallback
}

// parseSavedAtFrontmatterOK is the inner OK-returning form of
// ParseSavedAtFrontmatter. Returns (parsedTime, true) when the
// frontmatter block parses cleanly AND contains a valid RFC 3339
// saved_at value. Returns (time.Time{}, false) on any failure mode:
// missing opening "---\n", missing closing "---\n", missing key,
// malformed value.
//
// Package-private because external callers should keep using the
// fallback-wrapping form. Internal callers (notably archive.go's
// Archive function) use this directly so the multi-step fallback
// chain is observable.
func parseSavedAtFrontmatterOK(content string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return time.Time{}, false
	}
	block, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return time.Time{}, false
	}
	for line := range strings.SplitSeq(block, "\n") {
		value, ok := strings.CutPrefix(line, "saved_at:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return time.Time{}, false
		}
		return parsed, true
	}
	return time.Time{}, false
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
