package sessionarchive

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// sessionEntry is the projection of a directory entry needed for
// discovery-hint rendering. Unexported — the package surface only
// returns the rendered string.
type sessionEntry struct {
	Filename  string
	Timestamp time.Time
}

// RenderDiscoveryHint returns 0, 1, or 2 lines summarizing the
// agent's past sessions for inclusion in prime output.
//
// Format (spec §7.2):
//
//	Line 1 (always, when sessions > 0):
//	  Past sessions: N saved (most recent YYYY-MM-DD) at <sessionsDir>
//	Line 2 (only when archiveResult.BigPicture is non-empty):
//	  Last big picture: <body truncated to ~120 chars + …>
//
// Returns the empty string when:
//   - sessionsDir doesn't exist (agent has never archived)
//   - sessionsDir exists but contains no *-restart.md files
//   - filesystem error reading the directory
//
// `archiveResult` may be nil; the call still succeeds with line 1
// only. This matters for callers that immediately follow a no-
// snapshot Archive (the result has nil BigPicture).
func RenderDiscoveryHint(sessionsDir string, archiveResult *ArchiveResult) string {
	sessions, err := listSessions(sessionsDir)
	if err != nil || len(sessions) == 0 {
		return ""
	}
	mostRecent := sessions[0]
	line1 := fmt.Sprintf("Past sessions: %d saved (most recent %s) at %s",
		len(sessions),
		mostRecent.Timestamp.Format("2006-01-02"),
		sessionsDir,
	)

	if archiveResult == nil || archiveResult.BigPicture == nil || *archiveResult.BigPicture == "" {
		return line1
	}
	return line1 + "\nLast big picture: " + truncateForHint(*archiveResult.BigPicture, 120)
}

// truncateForHint truncates s to roughly max display characters,
// suffixing the Unicode ellipsis "…" (U+2026, 3 bytes UTF-8) when
// truncation occurred. Uses rune count so the visual length is
// stable regardless of multi-byte chars in the §1 body.
func truncateForHint(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// listSessions reads the agent's sessions/ directory and returns
// entries sorted descending by mtime. Filenames must end in
// "-restart.md" — other entries are ignored. Returns (nil, nil)
// for a missing directory (not an error; agent has never archived).
func listSessions(dir string) ([]sessionEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sessions []sessionEntry
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), "-restart.md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		sessions = append(sessions, sessionEntry{
			Filename:  e.Name(),
			Timestamp: info.ModTime(),
		})
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Timestamp.After(sessions[j].Timestamp)
	})
	return sessions, nil
}
