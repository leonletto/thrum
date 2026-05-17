package schedule

import (
	"crypto/sha256"
	"encoding/binary"
	"time"
)

// DeterministicJitter computes a deterministic ±jitter offset for the given
// job + daemon + period. Same inputs always produce the same offset, so
// daemon restarts do not shift fire-times for already-registered jobs.
//
// Per spec §4.3 + A-B1 Q2.3 brainstorm:
//   - override == 0 → default ±3% of period; cap at 60s for periods > 1
//     minute (60s of jitter on a 6-hour job is plenty; 3% would be ~11
//     minutes which is operator-confusing). Sub-minute periods keep the
//     proportional value (always < 60s).
//   - override > 0 → bound to ±override exactly.
//   - period == 0 → suppressed (returns 0). Reactor passes period=0 for
//     one-shot @at / @once jobs since shifting a user-specified instant
//     would break expectations (canonical §4.1.1).
//
// Mixing is via SHA-256 of (jobID || 0x00 || daemonID). The first 8 bytes of
// the digest are mapped uniformly into [-bound, +bound).
func DeterministicJitter(jobID, daemonID string, period, override time.Duration) time.Duration {
	if period == 0 {
		return 0
	}
	bound := override
	if bound == 0 {
		bound = period * 3 / 100 // 3% default
		// Cap at 60s for periods > 1 minute. Sub-minute periods keep the
		// proportional value since it's always smaller than the cap anyway.
		if period > time.Minute && bound > 60*time.Second {
			bound = 60 * time.Second
		}
	}

	h := sha256.New()
	_, _ = h.Write([]byte(jobID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(daemonID))
	sum := h.Sum(nil)

	raw := binary.BigEndian.Uint64(sum[:8])
	span := int64(bound * 2)
	if span == 0 {
		return 0
	}
	offset := int64(raw%uint64(span)) - int64(bound)
	return time.Duration(offset)
}
