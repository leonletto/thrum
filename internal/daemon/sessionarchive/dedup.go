package sessionarchive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// collisionCap is the total number of destination-path attempts:
// base + suffixes 1..9. Rapid restarts within the same millisecond
// produce identical FormatTimestamp values, so the move pipeline
// disambiguates with `-1` … `-9` filename suffixes. Beyond 10
// snapshots in one millisecond we return an error rather than
// silently appending more digits — that volume indicates a stuck
// loop the caller should surface, not a normal restart pattern.
const collisionCap = 10

// UniqueDestPath returns the first non-existent file path in dir
// under the convention `<timestamp>-restart.md`, then
// `<timestamp>-restart-1.md` through `<timestamp>-restart-9.md` if
// the base path is taken. Returns an error if all 10 candidates
// exist (signals a probable restart loop; archive caller logs +
// surfaces).
//
// Stat-only check; the caller is responsible for the actual file
// creation (rename in this package's Archive flow). A race between
// stat and rename is acceptable for this caller — concurrent
// archive of the same agent is gated by the per-agent mutex in
// Archive() (Task 5).
func UniqueDestPath(dir, timestamp string) (string, error) {
	base := filepath.Join(dir, timestamp+"-restart.md")
	if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
		return base, nil
	}
	for n := 1; n < collisionCap; n++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-restart-%d.md", timestamp, n))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("session-archive: collision cap reached for timestamp %q (10 attempts)", timestamp)
}
