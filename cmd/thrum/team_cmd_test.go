package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/cli"
)

// teamJSONSample is a mixed member set covering two daemons plus the
// legacy/unknown set (empty origin_daemon) so the JSON-shape assertions
// exercise both the filter and aggregate payloads.
func teamJSONSample() []cli.TeamMember {
	return []cli.TeamMember{
		{AgentID: "a", OriginDaemon: "d_1", Hostname: "host-a"},
		{AgentID: "b", OriginDaemon: "d_1", Hostname: "host-a"},
		{AgentID: "c", OriginDaemon: "d_2", Hostname: "host-b"},
		{AgentID: "d", OriginDaemon: "", Hostname: ""},
	}
}

// TestTeamCmd_JSONForms covers AC4: all four new forms (--daemon, --host,
// local, daemons) honor --json. The three filter forms (--daemon/--host/local)
// share the emitFilteredTeam path; daemons emits the aggregate slice directly.
// Each case asserts the emitted stdout is a single valid JSON document of the
// expected shape, including the zero-result filter (which must still emit valid
// JSON, not crash or print the no-match prose).
func TestTeamCmd_JSONForms(t *testing.T) {
	orig := flagJSON
	flagJSON = true
	t.Cleanup(func() { flagJSON = orig })

	members := teamJSONSample()

	t.Run("filter-nonempty", func(t *testing.T) {
		out, _ := captureStdStreams(t, func() {
			if err := emitFilteredTeam(cli.FilterByDaemon(members, "d_1"), "daemon", "d_1"); err != nil {
				t.Fatalf("emitFilteredTeam: %v", err)
			}
		})
		var resp cli.TeamListResponse
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if len(resp.Members) != 2 {
			t.Errorf("want 2 filtered members in JSON, got %d", len(resp.Members))
		}
		// shared_messages is team-global and must not ride a filtered view.
		if resp.SharedMessages != nil {
			t.Errorf("filtered JSON must omit shared_messages, got %+v", resp.SharedMessages)
		}
	})

	t.Run("filter-empty", func(t *testing.T) {
		out, _ := captureStdStreams(t, func() {
			if err := emitFilteredTeam(cli.FilterByDaemon(members, "d_nope"), "daemon", "d_nope"); err != nil {
				t.Fatalf("emitFilteredTeam: %v", err)
			}
		})
		if !json.Valid([]byte(out)) {
			t.Fatalf("zero-result filter must still emit valid JSON, got:\n%s", out)
		}
		var resp cli.TeamListResponse
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if len(resp.Members) != 0 {
			t.Errorf("want 0 members, got %d", len(resp.Members))
		}
	})

	t.Run("daemons", func(t *testing.T) {
		out, _ := captureStdStreams(t, func() {
			if err := cli.EmitJSON(cli.AggregateByDaemon(members)); err != nil {
				t.Fatalf("EmitJSON: %v", err)
			}
		})
		var rows []cli.DaemonAggregate
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		// Counts must sum to the member total even through the JSON round-trip.
		total := 0
		for _, r := range rows {
			total += r.AgentCount
		}
		if total != len(members) {
			t.Errorf("JSON aggregate counts must sum to %d, got %d", len(members), total)
		}
	})
}

// TestTeamCmd_MutualExclusion covers AC6: `thrum team --daemon X --host Y`
// errors cleanly (the two filters are mutually exclusive). The guard fires in
// RunE before any daemon round-trip, so the command tree need not be wired to a
// live daemon for this assertion.
func TestTeamCmd_MutualExclusion(t *testing.T) {
	cmd := teamCmd()
	cmd.SetArgs([]string{"--daemon", "d_1", "--host", "host-a"})
	// Swallow cobra's usage/error output so the test log stays clean.
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err := cmd.Execute()
	if err == nil {
		t.Fatal("want error when both --daemon and --host are set, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should mention mutual exclusion, got: %v", err)
	}
}
