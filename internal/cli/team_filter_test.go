package cli

import (
	"strings"
	"testing"
)

// sampleMembers returns a mixed set covering both daemons, multiple hosts, and
// the legacy/unknown set (empty origin_daemon and/or hostname).
func sampleMembers() []TeamMember {
	return []TeamMember{
		{AgentID: "a", OriginDaemon: "daemon-1", Hostname: "host-a"},
		{AgentID: "b", OriginDaemon: "daemon-1", Hostname: "host-a"},
		{AgentID: "c", OriginDaemon: "daemon-2", Hostname: "host-b"},
		{AgentID: "d", OriginDaemon: "", Hostname: ""},       // fully legacy
		{AgentID: "e", OriginDaemon: "", Hostname: "host-c"}, // daemon unknown, host known
	}
}

func TestFilterByDaemon(t *testing.T) {
	members := sampleMembers()

	got := FilterByDaemon(members, "daemon-1")
	if len(got) != 2 {
		t.Fatalf("daemon-1: want 2, got %d", len(got))
	}
	for _, m := range got {
		if m.OriginDaemon != "daemon-1" {
			t.Errorf("unexpected member %s with daemon %q", m.AgentID, m.OriginDaemon)
		}
	}

	// Exact match: empty daemonID selects the legacy/unknown set.
	got = FilterByDaemon(members, "")
	if len(got) != 2 {
		t.Fatalf("empty daemon: want 2 (legacy set), got %d", len(got))
	}

	// No match returns an empty (non-nil) slice, not a crash.
	got = FilterByDaemon(members, "daemon-nope")
	if len(got) != 0 {
		t.Fatalf("no-match: want 0, got %d", len(got))
	}
	if got == nil {
		t.Error("no-match: want non-nil empty slice")
	}
}

func TestFilterByHost(t *testing.T) {
	members := sampleMembers()

	got := FilterByHost(members, "host-a")
	if len(got) != 2 {
		t.Fatalf("host-a: want 2, got %d", len(got))
	}

	got = FilterByHost(members, "host-b")
	if len(got) != 1 || got[0].AgentID != "c" {
		t.Fatalf("host-b: want [c], got %+v", got)
	}

	// Empty hostname selects members with no hostname recorded.
	got = FilterByHost(members, "")
	if len(got) != 1 || got[0].AgentID != "d" {
		t.Fatalf("empty host: want [d], got %+v", got)
	}

	got = FilterByHost(members, "host-nope")
	if len(got) != 0 {
		t.Fatalf("no-match: want 0, got %d", len(got))
	}
}

func TestAggregateByDaemon_CountsSumToTotal(t *testing.T) {
	members := sampleMembers()
	rows := AggregateByDaemon(members)

	total := 0
	for _, r := range rows {
		total += r.AgentCount
	}
	if total != len(members) {
		t.Fatalf("counts must sum to total: want %d, got %d", len(members), total)
	}
}

func TestAggregateByDaemon_Buckets(t *testing.T) {
	rows := AggregateByDaemon(sampleMembers())

	// Expect 3 rows: daemon-1, daemon-2, unknown.
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d: %+v", len(rows), rows)
	}

	byID := map[string]DaemonAggregate{}
	for _, r := range rows {
		byID[r.DaemonID] = r
	}

	if r := byID["daemon-1"]; r.AgentCount != 2 || r.Hostname != "host-a" {
		t.Errorf("daemon-1: want count=2 host=host-a, got %+v", r)
	}
	if r := byID["daemon-2"]; r.AgentCount != 1 || r.Hostname != "host-b" {
		t.Errorf("daemon-2: want count=1 host=host-b, got %+v", r)
	}
	// Unknown bucket holds the two empty-origin_daemon members. Hostname is the
	// first non-empty host seen for that bucket ("" for d, then host-c for e).
	unk, ok := byID[UnknownDaemonBucket]
	if !ok {
		t.Fatalf("want an %q bucket, rows=%+v", UnknownDaemonBucket, rows)
	}
	if unk.AgentCount != 2 {
		t.Errorf("unknown bucket: want count=2, got %d", unk.AgentCount)
	}
	if unk.Hostname != "host-c" {
		t.Errorf("unknown bucket: want first non-empty host=host-c, got %q", unk.Hostname)
	}
}

func TestAggregateByDaemon_UnknownLast(t *testing.T) {
	rows := AggregateByDaemon(sampleMembers())
	if rows[len(rows)-1].DaemonID != UnknownDaemonBucket {
		t.Errorf("unknown bucket must sort last, got order: %+v", rows)
	}
	// Known daemons sorted ascending.
	if rows[0].DaemonID != "daemon-1" || rows[1].DaemonID != "daemon-2" {
		t.Errorf("known daemons must sort ascending, got: %+v", rows)
	}
}

func TestAggregateByDaemon_Empty(t *testing.T) {
	rows := AggregateByDaemon(nil)
	if len(rows) != 0 {
		t.Fatalf("empty input: want 0 rows, got %d", len(rows))
	}
}

func TestFormatDaemonAggregates(t *testing.T) {
	out := FormatDaemonAggregates(AggregateByDaemon(sampleMembers()))

	for _, want := range []string{"DAEMON", "HOST", "AGENTS", "daemon-1", "daemon-2", UnknownDaemonBucket, "total"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// The unknown bucket has no hostname for member d but inherits host-c from e;
	// the total line mirrors the member count.
	if !strings.Contains(out, "total") {
		t.Errorf("missing total line:\n%s", out)
	}
}

func TestFormatDaemonAggregates_Empty(t *testing.T) {
	out := FormatDaemonAggregates(nil)
	if !strings.Contains(out, "No agents") {
		t.Errorf("empty: want 'No agents', got %q", out)
	}
}
