---
name: adversarial-critique
description: Use when an implementer is blocked mid-work by a design flaw, contradiction, or unforeseen fork with 2-3 ways to resolve it - produces a defended pick that unblocks the work and leaves an audit trail so the decision can be revisited if it later proves wrong
---

# Adversarial Critique

## Overview

Stress-test a mid-implementation design fork by dispatching sub-agents that argue opposing options across three structured rounds. Produces a defended pick with invariants and acceptance criteria — not a "both approaches work" summary. The artifact left on disk is an audit trail: if the decision proves wrong later, future readers can see exactly why it was made and what was considered.

## When to use

- Implementation work has hit a blocker — a flaw, contradiction, or unforeseen fork — and can't continue until a decision is made
- The resolution has 2-3 plausible paths, each with real tradeoffs
- The decision is substantive enough that "just pick one" would likely be regretted
- You want an audit trail so the decision can be revisited if it proves wrong later

## When NOT to use

- Upfront design before implementation starts — use `brainstorming` instead
- Obvious choices where any option works — just pick and continue
- Emergencies where speed beats rigor
- Pure research questions — use `efficient-multi-agent-research`
- 4+ options — decompose the decision or narrow first

## Inputs

**Required:**
- Topic (one-sentence statement of the blocker)
- 2 or 3 options, each with a short name and one-sentence description

**Strongly recommended:**
- Context block: relevant file excerpts, prior art, schema snippets, constraints
- Engagement axes: cross-cutting concerns the debate MUST address (e.g. "cross-daemon semantics", "schema impact", "test strategy", "blast radius")

Without these, sub-agents re-grep the codebase and produce generic arguments. Pre-assembled context is the difference between a real debate and a shallow one.

## The three rounds

Dispatch one sub-agent per option, in parallel (single message, multiple Agent tool calls). **Model: sonnet** for all dispatches — quality matches opus on structured critique at a fraction of the cost.

### Round 1 — opening positions

Each agent gets: topic, context block, engagement axes, their assigned option. Word limit ~450. Required output:

- **Argument** (why this option)
- **Proposed shape** (concrete — API, struct, contract)
- **Pre-emptive rebuttal** (predict opponent's strongest attack, answer it now)

The pre-emptive rebuttal forces steelmanning and prevents R2 surprise-reveal.

### Round 2 — rebuttals

Fresh Agent dispatch per side (not SendMessage — returned agents don't re-activate). Pass in the other side(s)' R1 verbatim. Required output:

- **Concessions** (1-3 bullets, honest) — an agent that concedes nothing is performative; reject and re-prompt
- **Rebuttals** (point-by-point, cite file:line)
- **Refined position** (same side, tightened)

### Round 3 — synthesis

Fresh dispatch per side. Pass in opponent's R2. Required output — pick exactly one:

- **HOLD** (same position, sharpened invariants)
- **CONCEDE** (switch to opponent's refined position)
- **SYNTHESIZE** (hybrid — describe precisely, no hand-waving)

Plus, regardless of which:
- Cross-cutting invariants in **IF X AND Y THEN Z** form
- Acceptance test paths and mock strategy
- Out-of-scope list

### Resolution

- Both/all sides → same synthesis: lock it
- Sides diverge: coordinator picks with rationale, flags residual disagreement in the transcript

## 3-option variant

Each agent defends its own corner throughout. R1 as normal (3 parallel). In R2 and R3 each agent receives the *other two's* prior round verbatim. Same required outputs. Cost scales from ~100k (2 options) to ~150k (3 options).

## Early exit

If R1 agents converge on the same answer (e.g., both concede the opposite option is actually better), skip R2 and go straight to R3 synthesis to lock the invariants. Record the early exit in the transcript.

## Verifying sub-agent claims

Sub-agents will cite file:line evidence in R2 and R3. Before accepting a synthesis, verify any load-bearing citation against actual source. Misreads happen — synthesizing from a wrong-premise debate produces a wrong decision. If a core argument rests on a misread, re-dispatch that agent's round with the correction. See `verification-before-completion` for the general discipline.

## Output artifact

Write the full transcript to `docs/superpowers/brainstorms/YYYY-MM-DD-<topic>-debate.md`:

```markdown
# <Topic> — Adversarial Debate

**Date:** YYYY-MM-DD
**Options considered:** A / B [/ C]
**Resolution:** <picked option or hybrid name>

## Context
<context block>

## Engagement axes
<cross-cutting concerns>

## R1 — Opening positions
### Option A
<verbatim R1 output>
### Option B
<verbatim R1 output>

## R2 — Rebuttals
### Option A
<verbatim R2 output>
### Option B
<verbatim R2 output>

## R3 — Synthesis
### Option A
<verbatim R3 output>
### Option B
<verbatim R3 output>

## Resolution
- **Picked:** <option or hybrid>
- **Rationale:** <1-3 paragraphs>
- **Invariants:** <IF/THEN list>
- **Acceptance tests:** <paths + mock strategy>
- **Out of scope:** <list>
- **Residual disagreement:** <none, or what remains>

## Post-script
<anything the coordinator caught AFTER the debate — critical for audit if the decision is revisited later>
```

**Return to caller:** one-line summary (picked option + rationale) plus file path. Keeps the caller's context lean.

## Anti-patterns

- **R2 identical to R1** — agent wrote a performative concession. Reject and re-prompt with "what specifically did the opponent land?"
- **Hand-wavy synthesis** — R3 compromise that can't be stated as IF/THEN invariants is usually dodging the hard question. Require the format.
- **Sub-agents gave up because they couldn't find a file** — context block wasn't good enough. Embed quotes, paths, line numbers inline; don't make sub-agents re-grep.
- **Synthesis dispatched to a sub-agent** — don't. The coordinator holds the full debate; sub-agents only see what you pass them. Dispatch CRITIQUE, not SYNTHESIS.

## Model and cost

Sonnet for all dispatches (see the "Sub-Agent Model Selection" guidance in project / global instructions). Roughly ~100k tokens for 2 options, ~150k for 3 options. This is an investment that buys a reviewer-ready decision an implementer can execute in a fresh session without re-hashing the design.

## See also

- `brainstorming` — upfront design dialogue with the user (used before implementation starts; complements this skill, which handles mid-implementation blockers)
- `writing-plans` — turns the resolution into an implementation plan if the blocker requires re-planning
- `efficient-multi-agent-research` — sister pattern for research questions
- `verification-before-completion` — verification discipline for sub-agent claims
- `dispatching-parallel-agents` — general pattern for parallel sub-agent work
