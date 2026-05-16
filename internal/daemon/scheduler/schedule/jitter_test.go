package schedule

import (
	"testing"
	"time"
)

// TestJitter_Deterministic: identical inputs produce identical offsets so
// daemon restart does not shift already-registered jobs.
func TestJitter_Deterministic(t *testing.T) {
	period := 5 * time.Minute
	a := DeterministicJitter("docs-bot", "daemon-abc", period, 0)
	b := DeterministicJitter("docs-bot", "daemon-abc", period, 0)
	if a != b {
		t.Errorf("jitter not deterministic: %v vs %v", a, b)
	}
}

func TestJitter_Bounded_3PercentDefault(t *testing.T) {
	period := 100 * time.Minute
	j := DeterministicJitter("docs-bot", "daemon-abc", period, 0)
	max := 3 * time.Minute // 3% of 100m
	if j < -max || j > max {
		t.Errorf("jitter %v out of [-3m, 3m] for 100m period", j)
	}
}

// TestJitter_SubMinutePeriodStaysProportional: 3% of 30s = 0.9s; the 60s cap
// only fires for periods > 1 minute.
func TestJitter_SubMinutePeriodStaysProportional(t *testing.T) {
	period := 30 * time.Second
	j := DeterministicJitter("docs-bot", "daemon-abc", period, 0)
	if j.Abs() > time.Second {
		t.Errorf("sub-minute jitter %v exceeds proportional bound of ~0.9s", j)
	}
}

// TestJitter_LongPeriodCappedAt60s: 3% of 1h = 108s; cap clamps to 60s.
func TestJitter_LongPeriodCappedAt60s(t *testing.T) {
	period := time.Hour
	j := DeterministicJitter("docs-bot", "daemon-abc", period, 0)
	if j.Abs() > 60*time.Second {
		t.Errorf("long-period jitter %v exceeded 60s cap", j)
	}
}

// TestJitter_DifferentJobsDifferentDaemons sanity-checks that the hash
// actually mixes both inputs. Pathological hash collisions are possible but
// unlikely.
func TestJitter_DifferentJobsDifferentDaemons(t *testing.T) {
	period := 10 * time.Minute
	j1 := DeterministicJitter("job-a", "daemon-1", period, 0)
	j2 := DeterministicJitter("job-b", "daemon-1", period, 0)
	j3 := DeterministicJitter("job-a", "daemon-2", period, 0)
	if j1 == j2 && j2 == j3 {
		t.Error("jitter doesn't mix job_id + daemon_id meaningfully")
	}
}

// TestJitter_PerJobOverride: explicit override bounds jitter regardless of
// period.
func TestJitter_PerJobOverride(t *testing.T) {
	period := 10 * time.Minute
	j := DeterministicJitter("docs-bot", "daemon-abc", period, 30*time.Second)
	if j.Abs() > 30*time.Second {
		t.Errorf("per-job override 30s ignored; got %v", j)
	}
}

// TestJitter_OneShotSuppressed: period=0 (one-shot @at / @once) returns 0
// regardless of inputs — canonical §4.1.1: jitter would shift the
// user-specified instant.
func TestJitter_OneShotSuppressed(t *testing.T) {
	j := DeterministicJitter("docs-bot", "daemon-abc", 0, 0)
	if j != 0 {
		t.Errorf("one-shot jitter must be 0, got %v", j)
	}
	// Even with an override, period=0 suppresses.
	j2 := DeterministicJitter("docs-bot", "daemon-abc", 0, 30*time.Second)
	if j2 != 0 {
		t.Errorf("one-shot jitter must be 0 even with override; got %v", j2)
	}
}
