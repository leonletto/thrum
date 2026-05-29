---
name: thrum-_snapshot-protocol
description: Shared snapshot-composition protocol consumed by $thrum-restart, $thrum-restart-extended, $thrum-sleep, $thrum-sleep-extended. Not user-invocable directly.
# source: claude-plugin/commands/_snapshot-protocol.md
# generated-by: scripts/sync-skills.sh
---

# Thrum _snapshot Protocol

This is a shared partial, not a user-invocable skill. Sibling Thrum skills
consume it as a protocol reference; do not invoke it directly.


## Snapshot Composition Protocol (shared partial)

Used by: `$thrum-restart`, `$thrum-restart-extended`, `$thrum-sleep`, `$thrum-sleep-extended`.

This is the canonical source-of-truth for snapshot composition discipline. The four sibling commands compose: read this partial → variant-specific terminal action. Do NOT invoke this file directly; it has no terminal action.

### Step 1 — Resolve identity and repo root

```bash
REPO=$(git rev-parse --show-toplevel) || { echo "ERROR: not in a git worktree"; exit 1; }
AGENT=$(thrum whoami --field agent_id) || { echo "ERROR: agent not registered"; exit 1; }
[ -n "$AGENT" ] || { echo "ERROR: empty agent_id"; exit 1; }
mkdir -p "${REPO}/.thrum/restart"
```

### Step 2 — Compose your continuation

#### Composition discipline (always applies)

Your context is high (or you are preparing to park) and the goal is to preserve all load-bearing decisions across the session boundary. Write a rich continuation that future-you will read as the first action after wake.

**§1 of your snapshot is REQUIRED FIRST PROSE.** Write the "Big picture — what shipped this session" (or, for sleep snapshots, "where work stands at park time") section BEFORE anything else. Be specific: name the artifacts, the decisions, the cycles closed. Examples:

> Locked the session-archive spec v2 with §1 Big picture requirement, five Q-Spec approvals, and Q-Spec-5 deferred to impl-time. Hand-off pending coordinator final review.

> Investigated rc.9 inbox-race against impl_inbox_race's hypothesis: confirmed the lock-substrate fence is the right fix. Filed thrum-XXX with 4 BLOCKING evidence points.

> Closed B-B1 E6.0 brainstormer-third pass. 2 BLOCKING + 5 IMPORTANT + 10 MINOR. All three load-bearing traps PASSed. Standing by for E6.1 next batch.

This section becomes YOUR OWN log entry, visible in `thrum agent sessions list` alongside the archives of every other session you've ever restarted from. Future-you (and other agents inspecting your history) skim §1 to decide which sessions are worth re-reading. Write it first — before the Resume Plan, before file paths, before patterns — because composing the §1 summary forces you to identify what was actually load-bearing about this session, and that priority shapes everything else you write below it.

After the §1 block:

CRITICAL DISCIPLINE — compose from your own working context only. To preserve the remaining runway:

- Do NOT dispatch sub-agents (Agent, Explore, etc.)
- Do NOT re-read files you've already read this session
- Do NOT spawn web fetches or external lookups
- Do NOT run lengthy investigations (git log spelunking, codebase searches, multi-file grep walks)

Each of those costs context you don't have to spend, and the cost compounds — a sub-agent that returns 6K tokens of summary doesn't just cost the dispatch, it pollutes the dying session further. If a fact isn't already in your working context, label it "unknown" or "verify post-restart" rather than fetching it now. Trust your in-context state.

Write for a competent stranger in your role — someone who has the runtime briefing (`thrum prime`, role preamble, project state) but none of this session's conversation context. Refer to the previous session in third person.

#### Choose your structure

The variant skill that invoked this partial tells you which structure to use:

- **`$thrum-restart` and `$thrum-sleep`** → use the STANDARD 11-section structure below.
- **`$thrum-restart-extended` and `$thrum-sleep-extended`** → use the EXTENDED 16-section structure below.

The standard structure is the working compact format. The extended structure is fundamentally different: a comprehensive technical reference (wire contracts, capability matrix, anticipated Q&A, design rationale) appropriate for designer/architect-grade handoffs where the next session may be cold and must recover the full design grammar from this artifact alone.

#### Standard structure (11 sections)

Use these numbered sections (write each as prose or table — your call — but the numbered structure itself should be present):

1. **Big picture** — what shipped this session (REQUIRED FIRST, written above per the §1 mandate).
2. **Where every artifact stands** — branches, specs, plans, in-flight PRs, partially-landed work. Concrete tips / paths / commit SHAs.
3. **Players + contributions** — who's working on what, who's standing down, what each agent's latest state is.
4. **Decisions made this session** — with the context that drove each. Just listing the decision loses the reasoning future-you needs to judge edge cases.
5. **Questions awaiting repo owner input** — anything queued for the project owner's call before work can proceed. Name the question concretely.
6. **Outstanding work you owe** — commitments still open on your side (pushes, merges, dispatches, doc-patches).
7. **Patterns that worked / burned us** — short reflective section: what to keep doing, what to stop. Two sub-sections is enough.
8. **File paths future-you will reopen** — concrete paths the next session will need. Group by purpose (in-flight / queued / reference).
9. **Numbered resume plan** — concrete first-N-steps the next session should take, in order. Step 1 must be actionable from a cold start.
10. **Honest unknowns — verify post-restart, do NOT fabricate** — list facts you suspect changed during the session OR were never confirmed in the first place. Future-you must NOT carry these forward as truth until they're verified.
11. **End-of-continuation note** — one short paragraph reflecting on the session itself.

Skip a section only when it genuinely doesn't apply — an honest "N/A: no decisions this session" beats fabrication. The numbered structure itself should always be present so future-you can scan for what's covered and what isn't.

#### Extended structure (16 sections)

Numbered `§1.` through `§16.`. The §-prefix is intentional — extended snapshots use a visually distinct convention from standard to signal grade difference. Each section names a concern; write as prose, a table, or omit with an explicit "N/A: <reason>" rather than skipping silently.

```
§1.  Header block — author / date / restart reason / restart-you mandate / context-percent at write time
§2.  Identity — agent_id / worktree path / branch / role / coordinator name / cut-point commit SHA
§3.  Big picture — what shipped this session  [REQUIRED FIRST PROSE — mirrors standard §1]
§4.  Mission state — where this work currently sits in its pipeline
§5.  Locked design decisions — Leon-LOCKs, design-fork resolutions, with rationale
§6.  Open L-questions parked for Leon — or "NONE: <reason>" with explicit note
§7.  Wire contracts — durable technical reference (types, signatures, file:line cites)
§8.  Capability matrix — per-surface contracts (row-by-row table for fanout work; OMIT if N/A)
§9.  Design inventory — locked taxonomy (entanglement classes, patterns, anti-patterns; OMIT if N/A)
§10. Cycle history — review cycles run, findings, fold record (OMIT if pre-review-phase)
§11. Pipeline state — DAG of artifacts (brainstorm → plan → impl → review) with current cursor
§12. Pre-restart artifacts on disk — durable handoff manifest (table: artifact / path / status)
§13. Anticipated Q&A — questions restart-you or downstream implementer will ask, pre-answered
§14. References — bd IDs, commit anchors, memories, file paths, message IDs (load-bearing for transcript recon)
§15. Honest unknowns — verify post-restart, do NOT fabricate
§16. Restart-you's immediate next actions — numbered; step 1 actionable from cold start
```

**Per-section guidance:**

- **§1 Header.** Top of file. Author (agent_id), date, restart reason (1-line trigger), one-sentence mandate to restart-you ("read this in full, then [N specific actions]"), context-% at write time. Read-first prominence.
- **§2 Identity.** `agent_id` / worktree absolute path / branch / role / coordinator name / cut-point commit SHA for any line cites that follow. Mechanical; lets a totally cold restart-you orient before reading any prose.
- **§3 Big picture.** REQUIRED FIRST PROSE. 1–3 sentences summarizing what the session accomplished. Specific: artifacts, decisions, cycles closed. Becomes the session-archive log entry. Write this BEFORE anything else (composing §3 forces you to identify what was load-bearing, which shapes what you write below). **Sleep variant:** for `$thrum-sleep` and `$thrum-sleep-extended`, §3 frames as "where work stands at park time" rather than "what shipped" — the agent is parking, not completing. Composition discipline (1–3 sentences, specific, load-bearing-first) is identical.
- **§4 Mission state.** Where this work sits in its pipeline. Not the pipeline DAG itself (that's §11) — the narrative status.
- **§5 Locked design decisions.** Each LOCK gets a line: decision + Leon's wording (when applicable) + rationale.
- **§6 Open L-questions.** Numbered Q-list with researcher recommendations. OR "NONE: all locked per <evidence>" with the evidence cited.
- **§7 Wire contracts.** Durable technical reference — types, function signatures, struct field lists, file:line cites. Cut from the live tree at §2's cut-point SHA. **Format:** use fenced code blocks for type signatures and struct fields; use inline `file:line` citations anchored to §2's cut-point SHA so future-you can `git show <SHA>:<file>` to reproduce.
- **§8 Capability matrix.** Row-by-row table when work fans out across N call sites / migration surfaces / per-symbol concerns. **OMIT criterion:** OMIT when work is single-call-site or single-surface. **REQUIRED criterion:** required when the session covered changes across ≥2 independent call sites / migration surfaces / per-symbol concerns. When in doubt, include — a one-row table is cheaper than a missing one.
- **§9 Design inventory.** Locked taxonomy: entanglement classes, pattern catalogue, anti-patterns. **OMIT criterion:** OMIT when the work hasn't yet developed a stable design grammar. **REQUIRED criterion:** required when ≥3 named classes or patterns recur in the session's decisions.
- **§10 Cycle history.** Review cycles run, findings (BLOCKING / IMPORTANT / MINOR counts), fold record, verify-before-fold spot-checks. **OMIT criterion:** OMIT when the snapshot is written before any review cycle has run. **REQUIRED criterion:** required after the first dual-review cycle closes.
- **§11 Pipeline state.** DAG of artifacts (brainstorm → plan → impl → review) with current cursor position. ASCII tree or table.
- **§12 Pre-restart artifacts.** Manifest table: each durable artifact's path + status. Lets restart-you spot-check all artifacts exist before acting.
- **§13 Anticipated Q&A.** Pre-answer questions restart-you or downstream implementer will ask. Format: Q (bold) + A (short). Answers cite live code so downstream agents don't re-derive.
- **§14 References.** Load-bearing index: bd IDs (with one-line state), commit anchors (with SHA + one-line claim), memory keys (with rationale for relevance), file paths grouped by purpose, critical message IDs (for transcript reconstruction).
- **§15 Honest unknowns.** List facts you suspect changed during the session OR were never confirmed. Future-you must NOT carry these forward as truth until verified.
- **§16 Restart-you's immediate next actions.** Numbered. Step 1 actionable from cold start.

**Visual-drift note:** Extended template adopts `§N.` prefix convention intentionally distinct from standard's `N. **Header**` numbered-list-with-bold-lead. The divergence signals grade difference (standard = compact, extended = comprehensive). Do NOT align them; the two surfaces stay visually distinct on purpose. **Fallback** (Tier 2): if any runtime strips `§` characters during rendering or commit-message embedding, the variant skill MAY substitute bold-numbered (`**1.**`, `**2.**`, …) matching standard.

### Step 3 — Write the continuation directly to your restart file

Use your Write tool to save the composed continuation to:

```text
${REPO}/.thrum/restart/${AGENT}.md
```

`thrum prime` will auto-inject this file at next session start (whether wake comes from `thrum tmux restart` or `thrum tmux create`). No bash heredoc or `cat <<EOF` redirection is needed — write the file directly.

After writing, return control to your variant skill (`$thrum-restart`, `$thrum-restart-extended`, `$thrum-sleep`, or `$thrum-sleep-extended`) for the terminal action.
