package reminders

import (
	"strings"
	"testing"
)

func TestTruncateSnapshot_UnderCap_Unchanged(t *testing.T) {
	s := strings.Repeat("a", 1024)
	if got := TruncateSnapshot(s); got != s {
		t.Errorf("snapshot under cap was modified (len got=%d want=%d)", len(got), len(s))
	}
}

func TestTruncateSnapshot_OverCap_Truncates(t *testing.T) {
	s := strings.Repeat("x", 30_000)
	got := TruncateSnapshot(s)
	if len(got) > MaxSnapshotBytes {
		t.Errorf("got %d bytes, exceeds cap %d", len(got), MaxSnapshotBytes)
	}
	if !strings.HasSuffix(got, truncationMarker) {
		t.Errorf("missing truncation marker; tail = %q", got[len(got)-len(truncationMarker)-5:])
	}
}

func TestTruncateSnapshot_ExactlyCap_Unchanged(t *testing.T) {
	s := strings.Repeat("y", MaxSnapshotBytes)
	if got := TruncateSnapshot(s); got != s {
		t.Errorf("snapshot at exact cap was modified (len got=%d want=%d)", len(got), MaxSnapshotBytes)
	}
}

// TestTruncateSnapshot_OneOverCap exercises the boundary at MaxSnapshotBytes+1
// — the smallest input that must truncate. Catches off-by-one if the
// comparison were `<` instead of `<=`.
func TestTruncateSnapshot_OneOverCap(t *testing.T) {
	s := strings.Repeat("z", MaxSnapshotBytes+1)
	got := TruncateSnapshot(s)
	if len(got) != MaxSnapshotBytes {
		t.Errorf("len got=%d want=%d", len(got), MaxSnapshotBytes)
	}
	if !strings.HasSuffix(got, truncationMarker) {
		t.Error("missing truncation marker on boundary-over input")
	}
}

// TestTruncateSnapshot_EmptyString covers the trivial case; truncation
// must not panic or wedge on zero-length input.
func TestTruncateSnapshot_EmptyString(t *testing.T) {
	if got := TruncateSnapshot(""); got != "" {
		t.Errorf("empty input returned %q", got)
	}
}
