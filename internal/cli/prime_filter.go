package cli

import (
	"bufio"
	"bytes"
	"strings"
)

// projectStateCoordinatorRole is the only role that receives the full
// project_state.md. Every other role gets the architectural subset —
// the H1 header block plus allowlisted H2 sections.
const projectStateCoordinatorRole = "coordinator"

// projectStateAllowedH2 enumerates H2 sections that non-coordinator
// roles receive. Heavy narrative sections (Recent Sessions, What's
// Queued) flood implementer/tester/researcher context without adding
// information those roles act on.
var projectStateAllowedH2 = map[string]bool{
	"## Current State Summary":    true,
	"## Open Epics / Active Work": true,
	"## Key Architecture Files":   true,
	"## Worktree Layout":          true,
}

// filterProjectStateSections returns a role-scoped view of
// project_state.md.
//
// Rules:
//   - role == "coordinator": passthrough, unchanged.
//   - All other roles (including "", unknown): keep everything before
//     the first `## ` heading (H1 block + preamble), then include only
//     allowlisted H2 sections. `### ` and deeper subheadings inherit
//     their parent H2's inclusion state.
//   - Missing sections are skipped silently (no error).
func filterProjectStateSections(data []byte, role string) []byte {
	if role == projectStateCoordinatorRole {
		return data
	}

	var out bytes.Buffer
	out.Grow(len(data))

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	seenH2 := false
	include := true

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "## ") {
			seenH2 = true
			include = projectStateAllowedH2[strings.TrimRight(line, " \t")]
		}
		if !seenH2 || include {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}

	// Preserve trailing-newline behavior of the input: bufio.Scanner
	// strips \n from each line, and we append one back. If the original
	// lacked a trailing newline, strip the one we added on the last line.
	result := out.Bytes()
	if len(data) > 0 && data[len(data)-1] != '\n' &&
		len(result) > 0 && result[len(result)-1] == '\n' {
		result = result[:len(result)-1]
	}
	return result
}
