---
name: coordinator-running-brainstorm-cycles
description:
  "Use when starting a brainstorm for a bug fix, feature, or architectural
  decision the coordinator can't trivially decide alone — spawns a researcher in
  an isolated worktree, runs the brainstorm interactively with the user,
  iterates dual-review cycles to ready-to-merge, optionally drives an
  overarching coherence pass when multiple sibling brainstorms close, then hands
  off to project-setup. Saves coordinator context by isolating brainstorm work
  in a sub-agent worktree rather than burning main-context tokens on Q-by-Q
  dialog."
# source: claude-plugin/skills/coordinator-running-brainstorm-cycles/SKILL.md
# generated-by: scripts/sync-skills.sh
---

## Coordinator: Running Brainstorm Cycles

### When to use

A coordinator-orchestrated brainstorm is the right shape when:

- The user wants to settle the **architecture / design** of a non-trivial bug
  fix or feature before any code is written.
- The decision space has **multiple defensible options** and the user wants to
  explore them dialectically rather than just be told "I picked X."
- The coordinator would otherwise be running the Q-by-Q dialog directly, burning
  main-context tokens that should stay reserved for coordination.
- Multiple parallel brainstorms are valuable (one researcher per topic) and the
  coordinator needs to keep them moving without serializing on its own
  attention.

**Don't use when:**

- The decision is small and the coordinator can just decide and act
  (single-paragraph note, no protocol changes).
- The user explicitly wants the coordinator to do the brainstorm in main context
  (rare; respect it).
- The work is mid-implementation and a researcher would just slow it down — use
  the `adversarial-critique` skill for in-flight design forks instead.

### What this skill does (and what it hands off)

This skill owns the **entire researcher-side design pipeline** with review gates
at every stage:

1. Spawn researcher → 2. Brainstorm interactively with user → 3. **Dual-review
   the brainstorm** → 4. Iterate to LOCK → 5. (Optional) Overarching coherence
   pass when sibling brainstorms close → 6. Researcher writes plan +
   **dual-review the plan** → 7. Researcher runs `project-setup` + **dual-review
   the impl prompt** → 8. Hand off to coord for implementer dispatch.

**Three explicit review gates** at stages 3, 6, and 7 — same dual-axis pattern
each time (`verify-against-source` + a prose-quality reviewer, sonnet sub-agents
in parallel — see "Review-loop mechanics" below). Skipping any review gate is a
documented anti-pattern. The brainstorm review catches design issues; the plan
review catches contract-drift and quality issues `writing-plans`' internal
reviewer misses; the prompt review catches plan→prompt translation errors before
the implementer executes against them.

### Review-loop mechanics (applies to Phases 3, 6, 7)

Every prose gate uses the SAME machinery. Define it once here; each phase below
names only its stage-specific source.

#### The two reviewers (parallel, per gate)

| Axis            | Reviewer                                                                                                       | Notes                                                                                                                                                                                                                                                                                                                                             |
| --------------- | -------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Conformance** | `verify-against-source` (thrum-owned skill)                                                                    | Does the artifact honor its INPUT(s)? Replaces `verify-against-plan` at prose gates — `verify-against-plan` BAILS without a code diff + a File-Structure table, so it cannot run on a brainstorm/plan/prompt. `verify-against-source` accepts a prose artifact + source doc with neither.                                                         |
| **Quality**     | general-purpose sonnet + prose-quality rubric (PRIMARY) ∥ `superpowers:requesting-code-review` (SUPPLEMENTARY) | The general-purpose prose-quality pass (internal consistency, gaps, contradiction, ambiguity, scope creep) is AUTHORITATIVE. `requesting-code-review` is diff/code-native, so run it as a supplementary fresh-eyes pass pointed at the artifact's commit range (`HEAD~1..HEAD`) — gated on the skill being resolvable; skip gracefully if absent. |

Both are `general-purpose`, `model: "sonnet"`, `run_in_background: true`. Wait
for BOTH before consolidating. Verify each BLOCKING against source before
forwarding.

#### Footer → commit → stamp (per stage)

1. After the artifact is written, append the gate footer:
   `<!-- THRUM-GATE: stage=<brainstorm|plan|prompt> next=<dual-review|writing-plans|project-setup|dispatch> -->`
2. **Commit the artifact (WIP) including the footer** — this gives the
   supplementary `requesting-code-review` pass a real `HEAD~1..HEAD` range.
3. Run the dual review against that range; each fix cycle commits its changes
   (giving the supplementary reviewer a fresh diff).
4. On terminate, append the verdict stamp. Use the **canonical, fixed-key-order
   form** (keys always in the order `stage=`, `verdict=`, `cycle=`, `date=`;
   **case-sensitive** — exactly `Ready:Yes` / `OVERRIDE`; never `grep -i`) so a
   literal `grep -F` matches:
   `<!-- THRUM-REVIEW: stage=<S> verdict=<Ready:Yes|OVERRIDE> cycle=<N> date=<YYYY-MM-DD> [by=<agent> reason="..."] -->`

#### Soft pre-flight greps (the SOFT enforcement tier)

Before invoking `writing-plans` (Phase 6) and before `project-setup` (Phase 7),
`grep -F` the prior artifact for `THRUM-REVIEW: stage=<S> verdict=Ready:Yes` (or
`verdict=OVERRIDE`); absent → STOP and run the review first. This is a STRONG
BEHAVIORAL guardrail, NOT a mechanical gate — an agent can ignore it. The only
structurally-enforceable gate is `project-setup` Phase 0 (it hard-bails).

#### Loop semantics

- `Ready:Yes` = ZERO BLOCKING findings remain. IMPORTANT/MINOR may be
  acknowledged-and-deferred without another cycle via a footer line:
  `<!-- THRUM-DEFER: stage=<S> cycle=<N> item="IMPORTANT #2" reason="<why + tracking ref>" -->`
  (any THRUM-DEFER still present at `project-setup` is surfaced in its Phase 0 /
  override audit).
- **Per-stage cap = 3 cycles**, independent per stage (max 9 across the
  pipeline) — a hard brainstorm does not starve the plan's budget.
- Cap hit with BLOCKINGs still open → STOP and escalate to the coordinator, who
  logs an override (stamp `verdict=OVERRIDE … reason="…"`) or redirects.

#### Superpowers dependency (D6)

The wrapper drives `superpowers:brainstorming` / `writing-plans`. Two distinct
pre-flight behaviors:

- `requesting-code-review` not resolvable → SKIP the supplementary quality pass
  gracefully (the loop still runs on the general-purpose primary).
- `brainstorming` / `writing-plans` not present → **BAIL** with an install
  instruction (`/plugin install superpowers@<marketplace>`).

`verify-against-source` carries no superpowers dependency and always works.

#### Countermand the superpowers chain (outcome-based)

`brainstorming` auto-chains to `writing-plans`, and `writing-plans` defaults
pull toward subagent-driven execution — both bypass thrum's `project-setup`. At
each invocation, inject (keyed on the concept, so it survives upstream
rewording):

> Regardless of any execution-handoff, "recommended" sub-skill, or save-location
> default these skills emit, the ONLY downstream path in thrum is the next thrum
> stage. After the artifact is written, STOP — do not auto-chain. Save to the
> thrum path, not the superpowers default. For `writing-plans`: remove/replace
> the plan-header line matching the stable substring
> `REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development` and ignore
> the Execution-Handoff offer — the plan feeds `project-setup`.

### Phase 1 — Set up the worktree, branch, and agent

#### Pick the base branch

| Topic shape                                                                          | Base branch                                           |
| ------------------------------------------------------------------------------------ | ----------------------------------------------------- |
| Bug fix, hardening, infra cleanup belonging to current release line                  | `thrum-dev`                                           |
| Work belonging to a multi-epic version program (e.g. v0.11 personal-agent substrate) | The version's long-lived branch (e.g. `thrum-agents`) |
| Work tied to an existing feature epic with its own long-lived branch                 | That branch                                           |

When in doubt, ask the user. Don't branch from `upstream/*` (per the global
git-safety rule).

#### Pick a name and module slug

- **Worktree dir name:** `<topic>-brainstorm` — kebab-case, descriptive of the
  topic, ends with `-brainstorm` so it's instantly recognizable in
  `git worktree list`. Examples: `inbox-race-brainstorm`, `agents-brainstorm`,
  `email-brainstorm`.
- **Branch name:** `feature/<topic>-brainstorm` (or `fix/<topic>-brainstorm` if
  the topic is a bug fix).
- **Agent name:** `researcher_<topic_underscore>` — matches the
  `<role>_<module>` convention. Examples: `researcher_inbox_race`,
  `researcher_scheduler`.
- **Module slug:** `<topic_underscore>` — short, lowercase, underscored.

#### Create branch + worktree + agent + launch runtime

```bash
# 1. Branch + worktree
git worktree add -b feature/<topic>-brainstorm \
  /Users/<you>/.thrum/worktrees/thrum/<topic>-brainstorm \
  <base-branch>

# 2. Register the agent and create the tmux session in one call
thrum tmux create <topic>-brainstorm \
  --cwd /Users/<you>/.thrum/worktrees/thrum/<topic>-brainstorm \
  --role researcher \
  --name researcher_<topic_underscore> \
  --module <topic_underscore> \
  --intent "Brainstorm: <one-line topic description>" \
  --runtime claude

# 3. Launch the runtime in the session
thrum tmux launch <topic>-brainstorm
```

The runtime auto-primes via the SessionStart hook. Do NOT manually inject
`$thrum-prime` via send-keys (per `coordinator-dispatching-work` discipline).

### Phase 2 — Send the briefing message

The briefing message is a structured handoff. Always send it via `thrum send`,
not `tmux send-keys` (per `coordinator-dispatching-work`).

#### The interactive-with-user scribe protocol

**Always include this verbatim block at the top of the briefing.** It encodes a
hard-won lesson: researchers who pre-emptively draft brainstorm docs without the
user produce confident-but-wrong commitments that cost a full review cycle to
undo. The "interactive scribe" framing keeps the user in the loop and the
researcher in capture-mode rather than design-mode.

```text
═══ INTERACTIVE-WITH-LEON SCRIBE PROTOCOL ═══

Stand by at-pane. Do NOT autonomously draft a brainstorm doc. <user> will
join your pane when they're ready and drive the brainstorm Q-by-Q. Your job
is to surface the questions, present options + reasoning per question,
capture decisions verbatim, and keep the brainstorm doc under user control.
The 'researcher-rule-interactive-brainstorm-scribe' rule applies — no
premature autonomous drafting.
```

(Replace `<user>` with the actual user's name. The bd memory key
`researcher-rule-interactive-brainstorm-scribe` propagates the rule across
researcher restarts.)

#### Briefing structure

Every briefing should include these sections (use `═══` headers for visual
distinction in the inbox display):

1. **Identity reminder** — agent name, worktree, branch, base branch (and that
   it's outside any larger version program if applicable). **Include an explicit
   "you run thrum commands from THIS worktree, not from the main repo or any
   other worktree" instruction** (see "Where the researcher runs thrum" below).
2. **Interactive scribe protocol** (verbatim block above)
3. **The problem** — one paragraph of the user-visible symptom or the feature
   gap
4. **What we already diagnosed / decided** — to prevent relitigation. Be
   explicit: "don't relitigate these; start from here."
5. **Design space to explore** — bulleted list of the open decisions the user
   will drive Q-by-Q
6. **Context to read while standing by** — concrete file paths + brief relevance
   notes. Researchers who read the right files before the dialog start with
   grounded options instead of generic ones.
7. **Deliverable** — exact path the brainstorm doc lands at; convention:
   `dev-docs/brainstorms/<YYYY-MM-DD>-<topic>-brainstorm.md`. Include the
   "Decision Summary table at the bottom is canonical" framing. **Explicitly say
   NOT to pre-write the design spec** until review feedback has landed.
8. **Stand-by-at-pane instruction** — one sentence telling them to confirm
   readiness and wait, not to start producing output.
9. **Countermand callout** — `superpowers:brainstorming`'s terminal state is
   "invoke writing-plans". Tell the researcher explicitly: when brainstorming
   reaches that terminal state, STOP at the design doc — do NOT auto-chain to
   `writing-plans`. The brainstorm goes through the Phase 3 review gate first,
   then Phase 6 runs `writing-plans` deliberately (with its own countermand).
   See "Review-loop mechanics → Countermand the superpowers chain."

Send the whole thing as one `thrum send` call. The recipient's runtime will
display it as a single inbox message; structure beats brevity here.

#### Where the researcher runs thrum (do not let them inherit the wrong rule)

Some users have a global `~/.claude/CLAUDE.md` rule that reads "always run thrum
commands from the main repo directory." That rule is correct for the
main-repo-resident coordinator agent — but **it inverts for worktree-resident
researchers.** A researcher running thrum from the main repo path resolves
identity to the coordinator's name (e.g. `@coordinator_main`) and sends every
message under the coordinator's identity, polluting audit trails.

The correct rule is: **agents run thrum commands from their OWN home
directory.** Coordinator from main repo. Researcher from their worktree. **Never
run thrum from a worktree that isn't yours** (that's the underlying intent the
buggy global rule was reaching for).

Always include an explicit instruction in the Identity-Reminder section of the
briefing, e.g.:

```text
⚠ Run thrum commands from THIS worktree
(/Users/<you>/.thrum/worktrees/thrum/<topic>-brainstorm), not from the
main repo or any other worktree. The global CLAUDE.md "main repo only"
rule is correct for coordinators but inverts for worktree-resident
agents — your identity file lives in this worktree, and running thrum
from anywhere else will impersonate whoever owns that path.
```

Project-local capture: run `bd memories coordinator-rule-thrum-from-own-home` to
see the canonical version of this rule. If your project doesn't have it, the
researcher's first restart may re-inherit the buggy global rule.

### Phase 3 — Dual-review when the brainstorm closes

When the researcher reports the brainstorm is ready for review, run the
**two-reviewer dual review** per "Review-loop mechanics" above.

- **Conformance:** `verify-against-source` — artifact = the brainstorm doc;
  **source(s)** = parent decisions, the feature request, the ticket, and any
  sibling brainstorms.
- **Quality:** general-purpose prose-quality PRIMARY ∥ `requesting-code-review`
  SUPPLEMENTARY (internal consistency, technical soundness, anti-patterns,
  gaps).

Both `general-purpose`, `model: "sonnet"`, `run_in_background: true`. **Have the
researcher append the gate footer and commit (in their own worktree) BEFORE you
spawn the dual-review sub-agents** — the brainstorm lives in the researcher's
branch, and the supplementary `requesting-code-review` pass needs a real
`HEAD~1..HEAD` diff. Wait for BOTH before consolidating; verify each BLOCKING
against source before forwarding. Apply the footer → commit → stamp protocol and
the loop semantics (Ready:Yes = 0 BLOCKING, per-stage 3-cap, override) from
"Review-loop mechanics". On terminate, the researcher stamps the brainstorm:
`<!-- THRUM-REVIEW: stage=brainstorm verdict=Ready:Yes cycle=<N> date=<YYYY-MM-DD> -->`.

#### Consolidated findings format

Send ONE numbered list to the researcher per `thrum send`, ordered:

1. BLOCKINGs in detail (severity, location, finding, suggestion)
2. IMPORTANTs as one-paragraph each
3. MINORs as condensed one-liners

Include "no-action positive tick-offs" at the bottom — this gives the researcher
confidence about what's already correct and prevents over-editing.

### Phase 4 — Iteration cycles

When the researcher reports their fixes are done:

- Run a **targeted verification sub-agent** (one sub-agent, sonnet) that reads
  the updated doc and confirms each prescribed fix landed correctly. Do NOT
  re-derive the original findings — that's done.
- For tiny cosmetic fixes (one-line format updates, missing citations), ask
  in-place and spot-verify with a Bash grep — don't run a full verification
  cycle for single-line changes.
- For a researcher's design refinements that diverged from the literal
  recommendation: evaluate on substance, not literal compliance. Smart
  alternatives that close the original concern AND respect prior decisions
  better are coordinator-accepted territory; flag them for the user only if they
  materially change the user-visible shape.

When the verification passes, send the researcher an explicit "APPROVED — ready
to merge" with stand-down instructions.

### Phase 5 — Overarching coherence pass (when sibling brainstorms close)

If the topic is part of a larger program with multiple parallel brainstorms,
once **all sibling brainstorms reach ready-to-merge**, fire an opus-tier
coherence + implementability pass over the corpus before specs are written.

Two parallel passes (background, opus model — escalate from sonnet because this
is genuinely cross-cutting reasoning over a large corpus):

| Pass                             | Focus                                                                                                                                                                                        |
| -------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Coherence / Contradiction**    | Direct contradictions between brainstorms, hidden cross-doc assumptions, vocabulary drift, reserved-field collisions, lifecycle composition, end-to-end event flows, cumulative parent-honor |
| **Implementability / Execution** | Realistic ship-cost, critical-path bottlenecks, capability assumptions that may not hold, test surface, operator-experience journeys, sub-epics needing split, sequencing risk               |

Both passes read the full brainstorm corpus + parent decisions + roadmap + any
existing companion specs. Their job is NOT to re-derive single-brainstorm
findings — it's to surface integration-layer issues that no single review could
see.

Findings from this pass go back to the relevant researchers (or to a follow-up
brainstorm if the issue spans multiple). Do not skip this pass when ≥ 3
brainstorms close in the same program — the integration layer is where the most
expensive footguns hide.

### Phase 6 — Researcher writes the plan (with dual-review gate)

Once the brainstorm is LOCKED (and any companion design spec is LOCKED), the
researcher runs `superpowers:writing-plans` to convert brainstorm + spec into
the implementation plan doc. (First, the soft pre-flight grep: `grep -F` the
brainstorm for its `THRUM-REVIEW: stage=brainstorm verdict=Ready:Yes` (or
`OVERRIDE`) stamp — absent → STOP and finish the brainstorm review.) **Inject
the outcome-based countermand** (see Review-loop mechanics) at the
`writing-plans` invocation: stop when the plan is written, save to the thrum
path, and strip the `subagent-driven-development` plan-header line — the plan
feeds `project-setup`, not superpowers execution.

**The plan doc gets the SAME dual-review treatment as the brainstorm.** This
review step is mandatory — `writing-plans` has an internal reviewer, but real-
world experience shows that's not sufficient. Independent review catches
contract-drift and quality issues the internal reviewer misses.

**The researcher (not coord) dispatches the dual-review** in their own worktree:

1. Researcher writes plan v1 via `writing-plans` skill (countermand applied).
2. Researcher runs the two-reviewer dual review (Review-loop mechanics):
   - **Conformance:** `verify-against-source` — artifact = the plan;
     **source(s)** = the brainstorm + the design spec. Verifies the plan honors
     every LOCKED decision; flags missing scope, silent deviation, over-scoping.
   - **Quality:** general-purpose prose-quality PRIMARY ∥
     `requesting-code-review` SUPPLEMENTARY — per-task acceptance-criteria
     precision, anti-pattern enumeration, risk-register completeness, sequencing
     logic.
3. Researcher consolidates findings into ONE numbered list (all findings, all
   severities, per the same format Phase 3 uses).
4. Researcher folds findings inline → plan v2; applies footer → commit → stamp.
5. Researcher repeats only if cycle-1 introduces new design surface (rare for
   bounded mechanical plans); otherwise v2 LOCKED, stamped
   `<!-- THRUM-REVIEW: stage=plan verdict=Ready:Yes cycle=<N> date=<YYYY-MM-DD> -->`.
6. Researcher signals plan LOCKED back to coord, citing both review passes.

If the researcher skips this step, send them back. Before Phase 7, the soft
pre-flight grep (Review-loop mechanics) confirms the plan carries the
`stage=plan verdict=Ready:Yes` (or `OVERRIDE`) stamp. Don't let them proceed to
`project-setup` until the plan has passed dual-review.

### Phase 7 — Researcher runs project-setup (with dual-review gate)

Plan LOCKED → researcher runs `thrum:project-setup` to produce the bundled
output: bd tickets + implementer prompt + cross-references.

**The implementer-prompt doc gets the SAME dual-review treatment as the plan.**
The prompt is the artifact the implementer executes against turn-by-turn; errors
propagate. The plan dual-review caught contract-drift in the plan; the prompt
review catches translation errors (plan → prompt) plus prompt-specific quality
(instructions clarity, anti-pattern enumeration, dispatch-readiness).

**The researcher (not coord) dispatches the post-setup dual-review**:

1. Researcher runs `project-setup` skill (bd tickets + prompt land together).
   Note: `project-setup` Phase 0 hard-bails unless the plan carries its
   `stage=plan verdict=Ready:Yes`/`OVERRIDE` stamp — Phase 6 must have stamped
   it.
2. Researcher runs the two-reviewer dual review on the impl prompt:
   - **Conformance:** `verify-against-source` — artifact = the impl prompt;
     **source(s)** = the LOCKED plan + the bead task descriptions (dump
     `bd show` to a temp file so the sub-agent can read the per-task ACs).
     Verifies the prompt faithfully translates per-task content + ACs + risk
     callouts; flags missing scope, divergent wording, dropped anti-patterns.
   - **Quality:** general-purpose prose-quality PRIMARY ∥
     `requesting-code-review` SUPPLEMENTARY — clarity, scope language,
     dispatch-readiness, sub-agent model guidance, DONE-shape spec.
3. Researcher consolidates → folds inline → re-issues prompt; `project-setup`
   appends the prompt-stage stamps (it OWNS them — see that skill's Step 5):
   first `<!-- THRUM-GATE: stage=prompt next=dispatch -->` immediately after the
   prompt is generated, then
   `<!-- THRUM-REVIEW: stage=prompt verdict=Ready:Yes cycle=<N> date=<YYYY-MM-DD> -->`
   after this review terminates.
4. Researcher signals "project-setup complete + post-setup dual-review applied"
   back to coord, citing both review passes + final artifact paths.

Reaffirmation (countermand at the prompt stage): `project-setup` is the only
downstream path — do not adopt `subagent-driven-development` execution even if a
superpowers skill suggests it.

If the researcher signals "project-setup complete" WITHOUT mentioning the
post-setup reviews, ASK explicitly: "did the post-setup dual-review run on the
impl prompt?" Send them back if not. The artifact the implementer executes must
be reviewed.

### Phase 8 — Hand off to coord

With plan LOCKED + reviewed AND project-setup complete + reviewed:

1. Researcher provides coord: plan path, prompt path, bd-epic ID, bd-task ID
   list, prereq verifications (philosophy.md, bd version, etc.).
2. Coord verifies the artifacts briefly + proceeds to Phase 0 implementer
   dispatch (worktree creation, hard-freeze if applicable, impl dispatch).
3. Stand the researcher down at-pane (or keep on standby for impl-time Q&A if
   Leon explicitly requests continuity). Don't leave brainstorm researchers
   spinning idle indefinitely; they consume tmux sessions.

### Anti-patterns

❌ **Pre-emptive autonomous spec.** Researcher writes the design spec without
review feedback on the brainstorm. Costs a full review cycle to undo and reduces
the brainstorm to a write-only artifact. The
`researcher-rule-interactive-brainstorm-scribe` bd memory rule blocks this;
include the verbatim protocol block in every briefing.

❌ **Half-batched findings.** Sending review findings before BOTH dual reviews
complete. Researcher fixes batch 1 and never sees batch 2.

❌ **Skipping the post-plan dual-review.** Treating `writing-plans` skill's
internal reviewer as sufficient and proceeding directly to `project-setup`
without an independent review of the plan doc. Documented gap as of S76
(2026-05-20); see `bd memories feedback-post-project-setup-review`. The plan and
the impl prompt MUST get the same dual-axis review treatment the brainstorm gets
— Phase 6 + Phase 7 explicit.

❌ **Skipping the post-project-setup dual-review on the impl prompt.** The
prompt is the artifact the implementer executes against turn-by-turn. Errors in
it propagate into every task. `project-setup` runs the bundle (bd + prompt)
mechanically; the prompt content is the implementer's spec. Treat it as such and
review it before dispatch.

❌ **Misread BLOCKINGs forwarded as gospel.** Reviewers (sub-agents) sometimes
misread cited code or stretch citations. Spot-verify any BLOCKING that names a
specific file/line/symbol before forwarding.

❌ **`thrum message read --all` mid-brainstorm.** Classic timing bomb: read
message A → message B arrives during the read → `--all` silently marks B read →
B is never seen. Use `thrum message read <id> [<id>...]` with specific IDs
instead, especially when juggling multiple researchers.

❌ **Sub-agents into the researcher's worktree.**
`feedback_no_subagents_to_worktrees` applies. If you need code research in the
brainstorm worktree, ask the researcher to do it; if you need broad codebase
exploration, spawn an `Explore` sub-agent in the main repo path, not the
worktree path.

❌ **Skipping the coherence pass.** When ≥ 3 sibling brainstorms close in the
same program, integration-layer issues that no single review can see are
virtually guaranteed. Fire the opus pass; it's worth the cost.

❌ **Renaming brainstorm researchers between topics.** Identity is bound to the
worktree. If a topic is done, kill the tmux session and tear down the worktree;
don't rebind the agent name to a different topic in place.

### Reference: existing pattern in flight

If you need a concrete reference for the briefing structure, dual-review cycles,
and overarching coherence pass, look at the v0.11 personal-agent substrate
program (started 2026-05-13):

- Brainstorm docs:
  `dev-docs/brainstorms/2026-05-13-thrum-agents-{a,b,c,d}-b1-brainstorm.md`
- Tracking: `dev-docs/thrum-agents/brainstorming-roadmap.md` + bd `thrum-6qmf`
- Four parallel researchers (`@researcher_scheduler`, `@researcher_agents`,
  `@researcher_skills`, `@researcher_email`) ran the pattern end-to-end.

### Project-specific rules (already loaded)

Project-local rules under `bd memories coordinator-rule-` were loaded at session
start by your preamble. If a project-local rule conflicts with this skill, the
project-local rule wins; surface the conflict in your reply so the user can
decide whether to graduate or remove the override.

If you accumulate a new rule mid-session about brainstorm orchestration, capture
it via:

```bash
bd remember --key coordinator-rule-brainstorm-<slug> "<rule + Why + How to apply>"
```
