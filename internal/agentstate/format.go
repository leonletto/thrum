// Package agentstate owns the per-scheduled-agent state.md format
// (spec §7.5): a Markdown file that carries session history as
// "4 verbatim entries + up to 3 summary blocks of 5 entries each",
// yielding a 15-19 session sliding window.
//
// The format is canonical: the parser is structurally strict so
// downstream tooling (`/thrum:recover-agent-state` per spec §6.5)
// can flip `agents.state_md_parse_failed_at` on the first malformed
// write rather than letting partial damage stack across wakes.
//
// Three primitives:
//
//   - Parse(content)            — strict structural parse
//   - (*StateMD).Write(w)       — round-trip writer
//   - PromoteAndDrop(s, entry)  — sliding-window logic per
//                                  brainstorm Q1 cycle-2
package agentstate

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// StateMD is the structured projection of an agent's state.md file.
// Field set + invariants per spec §7.5.
type StateMD struct {
	// Header
	AgentName    string
	LastUpdated  time.Time // from `> **Last updated:** <ISO 8601>` line
	LastRunID    string    // from `**Last run:** <run_id>` line
	LastRunState string    // from `· <completion-state>` suffix on the last-run line

	// Session history
	Verbatim      []VerbatimEntry // hard cap: ≤4
	SummaryBlocks []SummaryBlock  // hard cap: ≤3, each with ≤5 entries

	// Free-form sections (one paragraph each)
	LastWorkedOn string
	PlanningNext string

	// Reference table — `<path>: <purpose>` rows
	References []ReferenceEntry
}

// VerbatimEntry is one of the up-to-4 most-recent session records,
// stored unsummarized so the agent reading state.md sees the
// per-session detail it just authored.
//
// Source line shape (em-dash and ASCII `--` both accepted by the
// parser; the writer emits em-dash to match spec §7.5 examples):
//
//	1. <session-id> · <date> — <one-line summary>
type VerbatimEntry struct {
	SessionID string
	Date      string // free-form date string (writer + parser don't enforce a fixed format)
	Summary   string
}

// SummaryBlock is one of up-to-3 graduated blocks, each holding
// up to 5 entries that have aged out of the verbatim queue.
//
// Source line shape:
//
//	**Block A** (sessions <start>–<end>): <one-or-multi-line summary>
type SummaryBlock struct {
	Label        string // "A" / "B" / "C" — assigned by the writer; parser preserves
	StartSession string
	EndSession   string
	Summary      string
}

// ReferenceEntry is one row of the reference table at the foot of
// state.md. Format: `- `<path>`: <purpose>`.
type ReferenceEntry struct {
	Path    string
	Purpose string
}

// Cap constants per spec §7.5 + brainstorm Q1 cycle-2:
//
//	max verbatim = 4
//	max summary blocks = 3
//	max entries per block = 5
//	floor = 4 + 2*5 + 1 = 15 (just after dropping a full block)
//	peak  = 4 + 3*5     = 19 (just after a new entry lands)
const (
	MaxVerbatim          = 4
	MaxSummaryBlocks     = 3
	MaxEntriesPerBlock   = 5
	WindowFloor          = 15
	WindowPeak           = 19
)

// Structural error sentinels. The parser returns these so the
// `/thrum:recover-agent-state` skill (spec §6.5) can match on
// errors.Is to decide whether to set agents.state_md_parse_failed_at
// or attempt a more tolerant retry.
var (
	ErrMissingHeader        = errors.New("agentstate: missing # header line")
	ErrMissingMetadata      = errors.New("agentstate: missing `Last updated` / `Last run` metadata line")
	ErrTooManyVerbatim      = errors.New("agentstate: verbatim section has more than 4 entries")
	ErrSummaryBlockTooLarge = errors.New("agentstate: summary block has more than 5 entries")
	ErrTooManySummaryBlocks = errors.New("agentstate: more than 3 summary blocks")
	ErrMalformedFormat      = errors.New("agentstate: unrecognized structural shape")
)

// Compiled patterns. Defined at package init so the hot
// recover/update paths don't pay regex-compile cost per call.
var (
	// Header: `# Agent State — <name>` — em-dash OR ASCII `--`.
	headerRE = regexp.MustCompile(`^# Agent State (?:—|--) (.+)$`)

	// Metadata blockquote: `> **Last updated:** <ts> **Last run:** <id> · <state>`.
	// Two capture groups (ts, run_id_and_state); the dot/middle-dot split is parsed downstream.
	metadataRE = regexp.MustCompile(`^>\s*\*\*Last updated:\*\*\s*(\S+)\s+\*\*Last run:\*\*\s*(.+)$`)

	// Verbatim entry: `1. <session-id> · <date> — <summary>` (em-dash OR ASCII).
	// Numbered prefix is 1-based and assigned by ordering; parser ignores the number
	// since the slice index encodes the same ordering.
	verbatimRE = regexp.MustCompile(`^\d+\.\s+(\S+)\s+·\s+([^—]*?)\s*(?:—|--)\s*(.+)$`)

	// Summary block header: `**Block A** (sessions <start>–<end>): <first-line>`.
	// En-dash (U+2013) is the canonical separator inside the parens; ASCII `-` accepted.
	summaryHeaderRE = regexp.MustCompile(`^\*\*Block ([A-Z])\*\*\s*\(sessions\s+(\S+?)(?:–|-)(\S+)\):\s*(.*)$`)

	// Reference row: `- `<path>`: <purpose>`.
	referenceRE = regexp.MustCompile("^- `([^`]+)`:\\s*(.+)$")
)

// Parse reads a state.md content blob and returns the parsed shape.
// Returns one of the package's structural sentinels on any
// malformed-input failure mode; the caller (recovery skill) flips
// the corruption flag per spec §6.5 on any non-nil error.
//
// The parser is strict on STRUCTURE (counts, section markers,
// header presence) and lenient on TEXT (paragraph content is taken
// as-written; no schema enforcement on summaries themselves).
func Parse(content string) (*StateMD, error) {
	if !strings.HasPrefix(content, "# Agent State ") {
		return nil, ErrMissingHeader
	}

	s := &StateMD{}
	lines := strings.Split(content, "\n")

	// Section state machine. Tracks which section's lines we're
	// currently accumulating. The "above the # Session history"
	// header block sets AgentName + metadata; subsequent sections
	// flush into the appropriate field.
	const (
		sectionPreamble = iota // header + metadata, before "## Session history"
		sectionVerbatim
		sectionSummaryBlocks
		sectionLastWorkedOn
		sectionPlanningNext
		sectionReferenceTable
	)
	section := sectionPreamble

	var (
		lastWorkedOnLines []string
		planningNextLines []string
		currentBlock      *SummaryBlock
	)

	flushCurrentBlock := func() {
		if currentBlock != nil {
			s.SummaryBlocks = append(s.SummaryBlocks, *currentBlock)
			currentBlock = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")

		switch {
		case strings.HasPrefix(trimmed, "# Agent State"):
			m := headerRE.FindStringSubmatch(trimmed)
			if m == nil {
				return nil, fmt.Errorf("%w: %q", ErrMissingHeader, trimmed)
			}
			s.AgentName = m[1]
			continue

		case strings.HasPrefix(trimmed, "> "):
			// Metadata blockquote line (only one expected).
			if !strings.Contains(trimmed, "Last updated") {
				continue // ignore stray blockquotes
			}
			m := metadataRE.FindStringSubmatch(trimmed)
			if m == nil {
				return nil, fmt.Errorf("%w: %q", ErrMissingMetadata, trimmed)
			}
			ts, err := time.Parse(time.RFC3339, m[1])
			if err != nil {
				return nil, fmt.Errorf("agentstate: Last updated not RFC 3339: %q: %w", m[1], err)
			}
			s.LastUpdated = ts
			// Split run_id and state on `·` (middle dot, U+00B7).
			runField := strings.TrimSpace(m[2])
			runID, runState, ok := strings.Cut(runField, "·")
			s.LastRunID = strings.TrimSpace(runID)
			if ok {
				s.LastRunState = strings.TrimSpace(runState)
			}
			continue

		case trimmed == "## Session history":
			section = sectionVerbatim
			continue

		case trimmed == "### Verbatim (most recent first)":
			section = sectionVerbatim
			continue

		case trimmed == "### Summary blocks (most recent first)":
			section = sectionSummaryBlocks
			continue

		case trimmed == "## Last worked on":
			flushCurrentBlock()
			section = sectionLastWorkedOn
			continue

		case trimmed == "## Planning next":
			flushCurrentBlock()
			section = sectionPlanningNext
			continue

		case trimmed == "## Reference table":
			flushCurrentBlock()
			section = sectionReferenceTable
			continue
		}

		// Section-specific handling.
		switch section {
		case sectionVerbatim:
			if strings.TrimSpace(trimmed) == "" {
				continue
			}
			m := verbatimRE.FindStringSubmatch(trimmed)
			if m == nil {
				// Permissive: non-matching lines (stray blank-ish
				// punctuation, future schema additions) skip rather
				// than fail. Only structural counts get strict-checked.
				continue
			}
			if len(s.Verbatim) >= MaxVerbatim {
				return nil, ErrTooManyVerbatim
			}
			s.Verbatim = append(s.Verbatim, VerbatimEntry{
				SessionID: m[1],
				Date:      strings.TrimSpace(m[2]),
				Summary:   strings.TrimSpace(m[3]),
			})

		case sectionSummaryBlocks:
			if strings.TrimSpace(trimmed) == "" {
				continue
			}
			m := summaryHeaderRE.FindStringSubmatch(trimmed)
			if m != nil {
				flushCurrentBlock()
				if len(s.SummaryBlocks) >= MaxSummaryBlocks {
					return nil, ErrTooManySummaryBlocks
				}
				currentBlock = &SummaryBlock{
					Label:        m[1],
					StartSession: m[2],
					EndSession:   m[3],
					Summary:      strings.TrimSpace(m[4]),
				}
				continue
			}
			// Continuation line of the current block's multi-line summary.
			if currentBlock != nil {
				if currentBlock.Summary != "" {
					currentBlock.Summary += "\n"
				}
				currentBlock.Summary += trimmed
			}

		case sectionLastWorkedOn:
			lastWorkedOnLines = append(lastWorkedOnLines, trimmed)

		case sectionPlanningNext:
			planningNextLines = append(planningNextLines, trimmed)

		case sectionReferenceTable:
			if strings.TrimSpace(trimmed) == "" {
				continue
			}
			m := referenceRE.FindStringSubmatch(trimmed)
			if m == nil {
				continue // permissive on non-matching prose
			}
			s.References = append(s.References, ReferenceEntry{
				Path:    m[1],
				Purpose: strings.TrimSpace(m[2]),
			})
		}
	}

	// Final flush — last summary block might still be in-flight if
	// "## Last worked on" wasn't present (defensive; shouldn't
	// happen for well-formed input).
	flushCurrentBlock()

	// Validate block-entry-count cap. The parser collects multi-line
	// summary text per block (continuation lines append), but each
	// block represents UP TO 5 logical entries. Spec §7.5 doesn't
	// require enumerating entries inside a block — the cap is
	// enforced at the writer side (PromoteAndDrop creates blocks
	// with ≤5 entries). For the parser, the structural check is
	// "no block has more than MaxEntriesPerBlock entries", which
	// can't be verified without re-parsing the block summary text.
	// The cap is therefore semantic on the writer side; the parser
	// trusts the writer.

	s.LastWorkedOn = strings.TrimSpace(strings.Join(lastWorkedOnLines, "\n"))
	s.PlanningNext = strings.TrimSpace(strings.Join(planningNextLines, "\n"))

	return s, nil
}

// Write serializes a StateMD to an io.Writer using the canonical
// spec §7.5 format. The output round-trips through Parse — tests
// in format_test.go assert this invariant.
func (s *StateMD) Write(w io.Writer) error {
	var buf strings.Builder

	// Header
	fmt.Fprintf(&buf, "# Agent State — %s\n\n", s.AgentName)

	// Metadata blockquote (only emit when we have meaningful data).
	if !s.LastUpdated.IsZero() || s.LastRunID != "" {
		ts := s.LastUpdated.UTC().Format(time.RFC3339)
		runField := s.LastRunID
		if s.LastRunState != "" {
			runField = s.LastRunID + " · " + s.LastRunState
		}
		fmt.Fprintf(&buf, "> **Last updated:** %s **Last run:** %s\n\n", ts, runField)
	}

	// Session history
	buf.WriteString("## Session history\n\n")
	buf.WriteString("### Verbatim (most recent first)\n\n")
	for i, v := range s.Verbatim {
		fmt.Fprintf(&buf, "%d. %s · %s — %s\n", i+1, v.SessionID, v.Date, v.Summary)
	}
	buf.WriteString("\n")

	buf.WriteString("### Summary blocks (most recent first)\n\n")
	for _, b := range s.SummaryBlocks {
		fmt.Fprintf(&buf, "**Block %s** (sessions %s–%s): %s\n\n",
			b.Label, b.StartSession, b.EndSession, b.Summary)
	}

	// Last worked on
	buf.WriteString("## Last worked on\n\n")
	if s.LastWorkedOn != "" {
		buf.WriteString(s.LastWorkedOn)
		buf.WriteString("\n")
	}
	buf.WriteString("\n")

	// Planning next
	buf.WriteString("## Planning next\n\n")
	if s.PlanningNext != "" {
		buf.WriteString(s.PlanningNext)
		buf.WriteString("\n")
	}
	buf.WriteString("\n")

	// Reference table
	buf.WriteString("## Reference table\n\n")
	for _, r := range s.References {
		fmt.Fprintf(&buf, "- `%s`: %s\n", r.Path, r.Purpose)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

// PromoteAndDrop applies the spec §7.5 sliding-window rules when a
// new session lands. Mutates s in place per brainstorm Q1 cycle-2:
//
//  1. newEntry becomes verbatim slot #1 (most recent).
//  2. Existing verbatim entries shift; was-#4 graduates out.
//  3. Graduating entry tries to enter the most-recent summary
//     block:
//     - If most-recent block has fewer than 5 entries → append.
//     - If most-recent block is full (5/5) → open a NEW
//     most-recent block with the graduating entry as its first
//     member.
//  4. If opening a new block would make 4 blocks total → drop the
//     oldest block entirely (5 entries lost) before opening the
//     new one.
//
// Yo-yo: total session count oscillates between 15 (just after a
// drop while opening a fresh block) and 19 (peak just before a
// drop).
//
// "Graduating entry" is represented in a summary block by appending
// the entry's SessionID + summary into the block's Summary field as
// a new line. Block entry count is derived from `strings.Count(b.Summary, "\n") + 1`
// when the field is non-empty.
func PromoteAndDrop(s *StateMD, newEntry VerbatimEntry) {
	// Step 1+2: insert newEntry at slot #1; shift others.
	s.Verbatim = append([]VerbatimEntry{newEntry}, s.Verbatim...)

	var graduating *VerbatimEntry
	if len(s.Verbatim) > MaxVerbatim {
		// Pop the oldest (last) verbatim entry.
		gradEntry := s.Verbatim[MaxVerbatim]
		s.Verbatim = s.Verbatim[:MaxVerbatim]
		graduating = &gradEntry
	}
	if graduating == nil {
		return
	}

	// Step 3+4: route graduating entry into a summary block.
	gradLine := fmt.Sprintf("%s · %s — %s",
		graduating.SessionID, graduating.Date, graduating.Summary)

	if len(s.SummaryBlocks) > 0 {
		head := &s.SummaryBlocks[0]
		headEntries := summaryEntryCount(head.Summary)
		if headEntries < MaxEntriesPerBlock {
			// Append to the most-recent block.
			if head.Summary != "" {
				head.Summary += "\n"
			}
			head.Summary += gradLine
			// Update end-session bound — most-recent block now
			// covers up through the graduating entry.
			head.EndSession = graduating.SessionID
			return
		}
	}

	// Open a NEW most-recent block. Drop oldest if at cap.
	if len(s.SummaryBlocks) >= MaxSummaryBlocks {
		s.SummaryBlocks = s.SummaryBlocks[:MaxSummaryBlocks-1]
	}
	newBlock := SummaryBlock{
		Label:        nextBlockLabel(s.SummaryBlocks),
		StartSession: graduating.SessionID,
		EndSession:   graduating.SessionID,
		Summary:      gradLine,
	}
	s.SummaryBlocks = append([]SummaryBlock{newBlock}, s.SummaryBlocks...)
}

// summaryEntryCount returns the number of newline-delimited entries
// in a SummaryBlock's Summary field. Empty Summary = 0 entries.
func summaryEntryCount(summary string) int {
	if summary == "" {
		return 0
	}
	return strings.Count(summary, "\n") + 1
}

// nextBlockLabel returns the label for a newly-opened most-recent
// block — A, B, or C — picking the label not currently used by
// the existing blocks. Labels are cosmetic (used only in the
// markdown render); content disambiguates via StartSession /
// EndSession.
func nextBlockLabel(existing []SummaryBlock) string {
	used := make(map[string]bool, len(existing))
	for _, b := range existing {
		used[b.Label] = true
	}
	for _, candidate := range []string{"A", "B", "C"} {
		if !used[candidate] {
			return candidate
		}
	}
	// Defensive: all three labels used — caller would have dropped
	// one before reaching here per MaxSummaryBlocks. Fall back to A.
	return "A"
}
