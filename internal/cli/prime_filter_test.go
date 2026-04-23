package cli

import (
	"strings"
	"testing"
)

const projectStateFixture = `# Project State — Thrum

**Last Updated:** 2026-04-20 **Phase:** test fixture.

---

## Current State Summary

Summary body.

### Architecture Health

Arch subsection — inherits parent.

### Key Architecture Decisions

Decisions subsection — inherits parent.

## Recent Sessions

Long narrative block. Sessions 42, 41, 40...

### Session 42

Details.

## Open Epics / Active Work

Epic table body.

## What's Queued / Next Steps

Queue notes.

## Key Architecture Files

Files table.

## Worktree Layout

Worktree snapshot.
`

func TestFilterProjectStateSections(t *testing.T) {
	cases := []struct {
		name           string
		role           string
		mustContain    []string
		mustNotContain []string
	}{
		{
			name: "coordinator passthrough",
			role: "coordinator",
			mustContain: []string{
				"# Project State — Thrum",
				"## Current State Summary",
				"## Recent Sessions",
				"Long narrative block",
				"## Open Epics / Active Work",
				"## What's Queued / Next Steps",
				"## Key Architecture Files",
				"## Worktree Layout",
			},
		},
		{
			name: "implementer drops narrative sections",
			role: "implementer",
			mustContain: []string{
				"# Project State — Thrum",
				"**Last Updated:**",
				"## Current State Summary",
				"### Architecture Health",
				"### Key Architecture Decisions",
				"## Open Epics / Active Work",
				"Epic table body",
				"## Key Architecture Files",
				"## Worktree Layout",
			},
			mustNotContain: []string{
				"## Recent Sessions",
				"Long narrative block",
				"### Session 42",
				"## What's Queued / Next Steps",
				"Queue notes",
			},
		},
		{
			name: "tester filtered same as implementer",
			role: "tester",
			mustContain: []string{
				"## Current State Summary",
				"## Open Epics / Active Work",
			},
			mustNotContain: []string{
				"## Recent Sessions",
				"## What's Queued / Next Steps",
			},
		},
		{
			name:        "researcher filtered same as implementer",
			role:        "researcher",
			mustContain: []string{"## Current State Summary"},
			mustNotContain: []string{
				"## Recent Sessions",
				"## What's Queued / Next Steps",
			},
		},
		{
			name: "empty role fails closed to filtered",
			role: "",
			mustContain: []string{
				"# Project State — Thrum",
				"## Current State Summary",
			},
			mustNotContain: []string{
				"## Recent Sessions",
				"## What's Queued / Next Steps",
			},
		},
		{
			name:           "unknown role fails closed to filtered",
			role:           "orchestrator",
			mustContain:    []string{"## Current State Summary"},
			mustNotContain: []string{"## Recent Sessions"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(filterProjectStateSections([]byte(projectStateFixture), tc.role))
			for _, s := range tc.mustContain {
				if !strings.Contains(got, s) {
					t.Errorf("expected substring %q in output, not found.\nOutput:\n%s", s, got)
				}
			}
			for _, s := range tc.mustNotContain {
				if strings.Contains(got, s) {
					t.Errorf("forbidden substring %q present in output.\nOutput:\n%s", s, got)
				}
			}
		})
	}
}

func TestFilterProjectStateSections_MissingAllowlistedSection(t *testing.T) {
	// Input is missing several allowlisted sections. Filter should
	// not error — it just yields whatever is present from the
	// allowlist plus the H1 preamble.
	input := `# Project State — Thrum

Preamble line.

---

## Current State Summary

Only summary exists; other allowlisted sections absent.

## Recent Sessions

Should be dropped for non-coordinator.
`
	got := string(filterProjectStateSections([]byte(input), "implementer"))
	if !strings.Contains(got, "# Project State — Thrum") {
		t.Error("expected H1 header preserved")
	}
	if !strings.Contains(got, "## Current State Summary") {
		t.Error("expected present allowlisted section preserved")
	}
	if strings.Contains(got, "## Recent Sessions") {
		t.Error("expected non-allowlisted section dropped")
	}
}

func TestFilterProjectStateSections_CoordinatorExactPassthrough(t *testing.T) {
	// Coordinator path must return the exact same bytes — byte-for-byte.
	got := filterProjectStateSections([]byte(projectStateFixture), "coordinator")
	if string(got) != projectStateFixture {
		t.Errorf("coordinator passthrough modified bytes; len(got)=%d len(input)=%d",
			len(got), len(projectStateFixture))
	}
}

func TestFilterProjectStateSections_PreambleOnly(t *testing.T) {
	// Input with no H2 headings at all — filter must keep everything.
	input := "# Project State — Thrum\n\nJust a preamble, no sections.\n"
	got := string(filterProjectStateSections([]byte(input), "implementer"))
	if got != input {
		t.Errorf("preamble-only input should pass through unchanged.\nGot:\n%s\nWant:\n%s", got, input)
	}
}

func TestFilterProjectStateSections_TrailingNewlineBehavior(t *testing.T) {
	tail := func(b []byte) string {
		if len(b) <= 8 {
			return string(b)
		}
		return string(b[len(b)-8:])
	}

	// Input without trailing newline should not gain one.
	input := "# Project State — Thrum\n\n## Current State Summary\n\nBody"
	got := filterProjectStateSections([]byte(input), "implementer")
	if len(got) == 0 || got[len(got)-1] == '\n' {
		t.Errorf("output should not end with newline when input lacks one; got tail %q", tail(got))
	}

	// Input WITH trailing newline should keep exactly one.
	input2 := "# Project State — Thrum\n\n## Current State Summary\n\nBody\n"
	got2 := filterProjectStateSections([]byte(input2), "implementer")
	if len(got2) == 0 || got2[len(got2)-1] != '\n' {
		t.Errorf("output should end with single newline when input has one; got tail %q", tail(got2))
	}
}

func TestFilterProjectStateSections_CRLFLineEndings(t *testing.T) {
	// Defensive: Windows/CRLF inputs must still match allowlist keys.
	// Without \r stripping, every heading becomes "## Current State Summary\r"
	// and the allowlist lookup misses, producing only the H1 preamble.
	input := "# Project State — Thrum\r\n\r\n## Current State Summary\r\n\r\nBody\r\n## Recent Sessions\r\n\r\nDrop me\r\n"
	got := string(filterProjectStateSections([]byte(input), "implementer"))
	if !strings.Contains(got, "## Current State Summary") {
		t.Error("CRLF-terminated allowlisted H2 should survive filter")
	}
	if !strings.Contains(got, "Body") {
		t.Error("CRLF-terminated allowlisted body should survive filter")
	}
	if strings.Contains(got, "## Recent Sessions") {
		t.Error("CRLF-terminated non-allowlisted H2 should be dropped")
	}
	if strings.Contains(got, "Drop me") {
		t.Error("CRLF-terminated non-allowlisted body should be dropped")
	}
}

func TestFilterProjectStateSections_TrailingWhitespaceInHeading(t *testing.T) {
	// Trailing spaces/tabs after an H2 heading should not defeat the
	// allowlist match. Normalization happens via strings.TrimRight.
	input := "# Project State — Thrum\n\n## Current State Summary   \t\n\nBody\n## Recent Sessions  \n\nDrop me\n"
	got := string(filterProjectStateSections([]byte(input), "implementer"))
	if !strings.Contains(got, "## Current State Summary") {
		t.Error("whitespace-trailed allowlisted H2 should survive filter")
	}
	if !strings.Contains(got, "Body") {
		t.Error("allowlisted body should survive filter even when heading has trailing whitespace")
	}
	if strings.Contains(got, "Drop me") {
		t.Error("whitespace-trailed non-allowlisted body should be dropped")
	}
}
