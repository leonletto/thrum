package main

import (
	"testing"
)

// TestParseAgentRunContext_HappyPath pins the canonical JSON shape
// per spec §7.4 wake-message wire format: the lean-prime skill
// (E6.2 writer; this CLI reader) persists job_id + run_id as a
// two-field flat object. Drift in either key name silently breaks
// `thrum job done` defaulting because the absent field stays empty
// and the CLI errors out demanding the flag.
func TestParseAgentRunContext_HappyPath(t *testing.T) {
	raw := []byte(`{"job_id": "docs-bot-2day", "run_id": "docs-bot-2day-g3-1747353600"}`)
	ctx, err := parseAgentRunContext(raw)
	if err != nil {
		t.Fatalf("parse err = %v; want nil", err)
	}
	if ctx.JobID != "docs-bot-2day" {
		t.Errorf("JobID = %q; want docs-bot-2day", ctx.JobID)
	}
	if ctx.RunID != "docs-bot-2day-g3-1747353600" {
		t.Errorf("RunID = %q; want docs-bot-2day-g3-1747353600", ctx.RunID)
	}
}

// TestParseAgentRunContext_TolerantOfExtraFields pins forward-compat:
// E6.2's lean-prime skill writer may add fields over time (e.g.
// scheduled_at, primer summary). Reader must NOT fail when
// encountering unknown keys — the CLI keeps working across schema
// extensions.
func TestParseAgentRunContext_TolerantOfExtraFields(t *testing.T) {
	raw := []byte(`{
		"job_id": "j1",
		"run_id": "r1",
		"scheduled_at": "2026-05-15T09:00:00-07:00",
		"primer": "do the thing",
		"prior_run_summary": null
	}`)
	ctx, err := parseAgentRunContext(raw)
	if err != nil {
		t.Fatalf("parse err = %v; want nil (tolerant of extra fields)", err)
	}
	if ctx.JobID != "j1" || ctx.RunID != "r1" {
		t.Errorf("JobID=%q RunID=%q; want j1/r1", ctx.JobID, ctx.RunID)
	}
}

// TestParseAgentRunContext_EmptyFieldsPreserved pins the empty-field
// semantics: a partial run_context.json (e.g. lean-prime skill mid-
// write) parses to a struct with empty strings rather than an error.
// The CLI then surfaces the empty RunID as its own canonical error
// downstream ("run context found but RunID empty"), giving the
// operator a tighter diagnostic than a JSON parse failure.
func TestParseAgentRunContext_EmptyFieldsPreserved(t *testing.T) {
	raw := []byte(`{"job_id": "", "run_id": ""}`)
	ctx, err := parseAgentRunContext(raw)
	if err != nil {
		t.Fatalf("parse err = %v; want nil for empty fields", err)
	}
	if ctx.JobID != "" || ctx.RunID != "" {
		t.Errorf("expected empty fields; got JobID=%q RunID=%q", ctx.JobID, ctx.RunID)
	}
}

// TestParseAgentRunContext_InvalidJSON pins the parse-failure path:
// malformed JSON returns an error wrapping the json.Unmarshal
// failure so log lines + CLI output include the underlying parser
// diagnostic.
func TestParseAgentRunContext_InvalidJSON(t *testing.T) {
	cases := []string{
		"",
		"{not json",
		"[]", // wrong shape (array, not object)
		`{"job_id": 42}`, // wrong type for job_id
	}
	for _, raw := range cases {
		_, err := parseAgentRunContext([]byte(raw))
		if err == nil {
			t.Errorf("err = nil for invalid input %q; want non-nil", raw)
		}
	}
}

// TestJobCmd_HasDoneSubcommand pins the canonical CLI tree shape:
// `thrum job` is a group with `done` as a subcommand. Future
// commands under the group (list, history, etc.) extend this; the
// test guards against an accidental top-level promotion of `done`
// that would break the documented `thrum job done` invocation.
func TestJobCmd_HasDoneSubcommand(t *testing.T) {
	root := jobCmd()
	if root.Use != "job" {
		t.Errorf("root.Use = %q; want 'job'", root.Use)
	}
	found := false
	for _, sub := range root.Commands() {
		if sub.Use == "done" {
			found = true
			break
		}
	}
	if !found {
		t.Error("`done` subcommand not found under `thrum job`")
	}
}

// TestJobDoneCmd_DeclaresExpectedFlags pins the CLI flag surface
// per Task 34 AC. A future renamed flag (e.g. --summary →
// --reason) would break agent-facing documentation + the
// agent.wake message body convention; the test catches the
// rename at compile time.
func TestJobDoneCmd_DeclaresExpectedFlags(t *testing.T) {
	cmd := jobDoneCmd()
	for _, name := range []string{"summary", "job-id", "run-id"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not declared on `thrum job done`", name)
		}
	}
}
