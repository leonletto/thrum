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
//	**Block A** (sessions <start>–<end>): <first-entry-line>
//	<second-entry-line>
//	...
//	<fifth-entry-line>
//
// Each entry-line follows the same shape as a VerbatimEntry source
// line: `<session-id> · <date> — <summary>` (em-dash OR ASCII `--`).
// Entries are stored as a structured slice rather than concatenated
// text so the MaxEntriesPerBlock invariant is exactly enforceable
// at parse time (Phase 3 brainstormer-third B1 fix).
type SummaryBlock struct {
	Label        string // "A" / "B" / "C" — assigned by the writer; parser preserves
	StartSession string
	EndSession   string
	Entries      []SummaryEntry // hard cap: ≤5 per spec §7.5
}

// SummaryEntry is one graduated session inside a SummaryBlock.
// Shape matches VerbatimEntry intentionally — graduated entries
// preserve the same `<session-id> · <date> — <summary>` shape so
// the writer's render is consistent across verbatim + summary
// sections.
type SummaryEntry struct {
	SessionID string
	Date      string
	Summary   string
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
	// Trailing value accepts empty string (`(.*)$`) for the first-session
	// case where no --run-id has been recorded yet — the writer emits
	// "**Last run:** " (no trailing value) and the round-trip must succeed.
	metadataRE = regexp.MustCompile(`^>\s*\*\*Last updated:\*\*\s*(\S+)\s+\*\*Last run:\*\*\s*(.*)$`)

	// Verbatim entry: `1. <session-id> · <date> — <summary>` (em-dash OR ASCII).
	// Numbered prefix is 1-based and assigned by ordering; parser ignores the number
	// since the slice index encodes the same ordering.
	verbatimRE = regexp.MustCompile(`^\d+\.\s+(\S+)\s+·\s+([^—]*?)\s*(?:—|--)\s*(.+)$`)

	// Summary block header: `**Block A** (sessions <start>–<end>): <first-line>`.
	// En-dash (U+2013) is the canonical separator inside the parens; ASCII `-` accepted.
	summaryHeaderRE = regexp.MustCompile(`^\*\*Block ([A-Z])\*\*\s*\(sessions\s+(\S+?)(?:–|-)(\S+)\):\s*(.*)$`)

	// Summary entry line: `<session-id> · <date> — <summary>` (same
	// shape as verbatim, minus the leading "N. " number prefix).
	// Em-dash AND ASCII `--` both accepted.
	summaryEntryRE = regexp.MustCompile(`^(\S+)\s+·\s+([^—]*?)\s*(?:—|--)\s*(.+)$`)

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
// Section sentinels for the parser state machine. Lifted to
// package scope (from function scope) so sectionName can switch
// on the named constants rather than raw integer literals —
// otherwise inserting a new sentinel between existing ones would
// silently misalign sectionName's labels (Phase 3 fix-batch
// reviewer M1).
const (
	sectionPreamble       = iota // header + metadata, before "## Session history"
	sectionVerbatim              // inside ### Verbatim
	sectionSummaryBlocks         // inside ### Summary blocks
	sectionLastWorkedOn          // inside ## Last worked on
	sectionPlanningNext          // inside ## Planning next
	sectionReferenceTable        // inside ## Reference table
)

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
			if err := requireSectionOrder(section, sectionPreamble, "## Session history"); err != nil {
				return nil, err
			}
			section = sectionVerbatim
			continue

		case trimmed == "### Verbatim (most recent first)":
			// Sub-section of ## Session history; accept while
			// section is sectionVerbatim (which the ## heading
			// already set), or accept once if section was
			// sectionPreamble (defensive: some files might skip
			// the parent heading).
			if section != sectionVerbatim && section != sectionPreamble {
				return nil, fmt.Errorf("%w: '### Verbatim' appeared after %s (must follow '## Session history')",
					ErrMalformedFormat, sectionName(section))
			}
			section = sectionVerbatim
			continue

		case trimmed == "### Summary blocks (most recent first)":
			// Must follow Verbatim within Session history.
			if section != sectionVerbatim {
				return nil, fmt.Errorf("%w: '### Summary blocks' appeared after %s (must follow '### Verbatim')",
					ErrMalformedFormat, sectionName(section))
			}
			section = sectionSummaryBlocks
			continue

		case trimmed == "## Last worked on":
			// Must follow Verbatim or Summary blocks (the two
			// sub-sections of Session history). Out-of-order
			// (e.g., after "## Planning next") rejected.
			if section != sectionVerbatim && section != sectionSummaryBlocks {
				return nil, fmt.Errorf("%w: '## Last worked on' appeared after %s (must follow Session history)",
					ErrMalformedFormat, sectionName(section))
			}
			flushCurrentBlock()
			section = sectionLastWorkedOn
			continue

		case trimmed == "## Planning next":
			// Must follow Last worked on.
			if section != sectionLastWorkedOn {
				return nil, fmt.Errorf("%w: '## Planning next' appeared after %s (must follow '## Last worked on')",
					ErrMalformedFormat, sectionName(section))
			}
			flushCurrentBlock()
			section = sectionPlanningNext
			continue

		case trimmed == "## Reference table":
			// Must follow Planning next.
			if section != sectionPlanningNext {
				return nil, fmt.Errorf("%w: '## Reference table' appeared after %s (must follow '## Planning next')",
					ErrMalformedFormat, sectionName(section))
			}
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
				// Strict: non-blank verbatim-section lines that
				// don't match the regex indicate corruption (e.g.,
				// a mid-write crash that left a partial line).
				// Per spec §6.5 + Phase 3 Medium #1 fix: surface
				// as ErrMalformedFormat so the caller routes
				// through /thrum:recover-agent-state rather than
				// silently dropping the entry.
				return nil, fmt.Errorf("%w: malformed verbatim entry %q", ErrMalformedFormat, trimmed)
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
				}
				// The header's trailing text (capture group 4) is
				// the FIRST entry's inline summary. If it matches
				// the entry shape, parse it; otherwise treat the
				// whole thing as one entry's summary with empty
				// session-id/date (defensive — well-formed
				// blocks from PromoteAndDrop always have the
				// `<id> · <date> — <summary>` shape on the
				// header line).
				firstLine := strings.TrimSpace(m[4])
				if firstLine != "" {
					if entry, ok := parseSummaryEntryLine(firstLine); ok {
						currentBlock.Entries = append(currentBlock.Entries, entry)
					}
				}
				continue
			}
			// Body line: either a new entry (matches entry shape)
			// or a continuation of the previous entry's summary.
			if currentBlock != nil {
				if entry, ok := parseSummaryEntryLine(trimmed); ok {
					currentBlock.Entries = append(currentBlock.Entries, entry)
				} else if n := len(currentBlock.Entries); n > 0 {
					// Continuation of last entry's multi-line summary.
					currentBlock.Entries[n-1].Summary += "\n" + trimmed
				}
				// If no entries yet and the line doesn't match
				// entry shape, it's stray prose — silently
				// dropped (rare; would indicate hand-edit drift
				// that doesn't fit the format).
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

	// Validate block-entry-count cap (Phase 3 brainstormer-third B1
	// fix). Each block holds up to MaxEntriesPerBlock entries; the
	// structured SummaryBlock.Entries slice makes this exactly
	// enforceable at parse time. Previously the parser counted
	// newlines in a concatenated Summary string — that approach
	// was ambiguous on hand-edited multi-line entries (a single
	// graduated entry with embedded newlines would inflate the
	// count). The structured slice eliminates that ambiguity.
	for i, b := range s.SummaryBlocks {
		if len(b.Entries) > MaxEntriesPerBlock {
			return nil, fmt.Errorf("%w: block %d (label %q) has %d entries (cap %d)",
				ErrSummaryBlockTooLarge, i, b.Label,
				len(b.Entries), MaxEntriesPerBlock)
		}
	}

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
		// First entry rides on the block-header line; remaining
		// entries land on their own lines below. Empty Entries
		// renders just the header (defensive — shouldn't happen
		// for blocks generated by PromoteAndDrop).
		if len(b.Entries) == 0 {
			fmt.Fprintf(&buf, "**Block %s** (sessions %s–%s):\n\n",
				b.Label, b.StartSession, b.EndSession)
			continue
		}
		first := b.Entries[0]
		fmt.Fprintf(&buf, "**Block %s** (sessions %s–%s): %s · %s — %s\n",
			b.Label, b.StartSession, b.EndSession,
			first.SessionID, first.Date, first.Summary)
		for _, e := range b.Entries[1:] {
			fmt.Fprintf(&buf, "%s · %s — %s\n", e.SessionID, e.Date, e.Summary)
		}
		buf.WriteString("\n")
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
// Graduating entries are stored as structured SummaryEntry values
// in the block's Entries slice. Block entry count is exactly
// `len(block.Entries)`.
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
	gradEntry := SummaryEntry{
		SessionID: graduating.SessionID,
		Date:      graduating.Date,
		Summary:   graduating.Summary,
	}

	if len(s.SummaryBlocks) > 0 {
		head := &s.SummaryBlocks[0]
		if len(head.Entries) < MaxEntriesPerBlock {
			// Append to the most-recent block.
			head.Entries = append(head.Entries, gradEntry)
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
		Entries:      []SummaryEntry{gradEntry},
	}
	s.SummaryBlocks = append([]SummaryBlock{newBlock}, s.SummaryBlocks...)
}

// parseSummaryEntryLine attempts to match a line in the
// summary-blocks section as a `<session-id> · <date> — <summary>`
// entry shape. Returns (entry, true) on match; (zero, false)
// otherwise (the line is a continuation of the previous entry's
// multi-line summary, or stray prose).
func parseSummaryEntryLine(line string) (SummaryEntry, bool) {
	m := summaryEntryRE.FindStringSubmatch(line)
	if m == nil {
		return SummaryEntry{}, false
	}
	return SummaryEntry{
		SessionID: m[1],
		Date:      strings.TrimSpace(m[2]),
		Summary:   strings.TrimSpace(m[3]),
	}, true
}

// requireSectionOrder enforces the spec §7.5 heading sequence per
// brainstormer-third B2 fix. Returns ErrMalformedFormat (wrapped
// with a descriptive message) if the parser is about to enter a
// section but the prior section isn't a valid predecessor.
//
// The canonical sequence:
//
//	preamble  → sectionVerbatim       (## Session history)
//	verbatim  → sectionSummaryBlocks  (### Summary blocks)
//	verbatim
//	  OR summary blocks
//	          → sectionLastWorkedOn   (## Last worked on)
//	last worked on
//	          → sectionPlanningNext   (## Planning next)
//	planning next
//	          → sectionReferenceTable (## Reference table)
//
// A state.md with sections in any other order indicates either
// tampering or a writer bug; rejected via ErrMalformedFormat so
// the recovery skill routes through spec §6.5.
func requireSectionOrder(current, want int, heading string) error {
	if current != want {
		return fmt.Errorf("%w: %q appeared after %s (expected after %s)",
			ErrMalformedFormat, heading, sectionName(current), sectionName(want))
	}
	return nil
}

// sectionName renders a section sentinel as a human-friendly string
// for error messages. Switches on the named constants so a future
// iota reorder doesn't silently misalign labels (Phase 3 fix-batch
// reviewer M1).
func sectionName(s int) string {
	switch s {
	case sectionPreamble:
		return "preamble (before '## Session history')"
	case sectionVerbatim:
		return "'### Verbatim' section"
	case sectionSummaryBlocks:
		return "'### Summary blocks' section"
	case sectionLastWorkedOn:
		return "'## Last worked on' section"
	case sectionPlanningNext:
		return "'## Planning next' section"
	case sectionReferenceTable:
		return "'## Reference table' section"
	default:
		return fmt.Sprintf("unknown section %d", s)
	}
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
