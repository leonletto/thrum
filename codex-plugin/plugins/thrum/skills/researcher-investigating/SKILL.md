---
name: researcher-investigating
description:
  "Use when investigating, exploring code, working on a research task, when
  asked to find me X, or to investigate Y. Loads researcher-specific discipline
  for running an investigation cleanly."
# source: claude-plugin/skills/researcher-investigating/SKILL.md
# generated-by: scripts/sync-skills.sh
---

## Researcher: Investigating

### Use Explore sub-agents for breadth-first searches

**Why:** Reading 10 files into your main context to "understand the
architecture" is the Context Hog trap. Sub-agents partition the search across
multiple conversations that each report a focused finding back — the
investigation gets done without polluting your context with raw file contents.
(Source: findings_researcher.md F1 / project Sub-Agent Strategy.)

**How to apply:** When the question is "how does X work" or "what calls Y",
spawn an `Explore` sub-agent (`model: "sonnet"` for judgment, `model: "haiku"`
for mechanical) with a clear partition: a specific directory, a specific symbol,
or a specific question. Get a focused report back. For research across N > 6
items, invoke `efficient-multi-agent-research` instead of bespoke dispatch — it
handles partition + parallelization + consolidation.

### Verify the actual state — don't answer from recall

**Why:** Reporting from memory of past panes, files, or commits produces
over-claimed findings. The preamble carries the invariant; this rule carries the
operational depth — the specific commands to run before trusting any state
claim.

**How to apply:**

- **Pane state.** Run `tmux capture-pane -p -t <pane>` (or the runtime
  equivalent) before reporting "the pane shows X" / "the prompt fired".
- **Code state.** Use the Read tool or `git show HEAD:<path>` before reporting
  "function Y is at line Z" or "file X handles case W".
- **Beads state.** Run `bd show <id>` before reporting "task is in_progress" /
  "issue is closed" — your remembered idea of the state may be stale.

If you can't verify, say so explicitly: "I believe X based on <evidence>; I have
not verified Y."

### Scope queries before deep-diving — return early when unclear

**Why:** A vague request ("investigate the auth flow") burns hours investigating
dimensions the requester didn't actually care about. Returning early with a
clarifying question is faster overall — even if it adds five minutes of
round-trip latency, it prevents three hours of wrong-direction work. (Source:
findings_researcher.md F6 — codex "3 hours of work, one real deliverable commit"
stemmed from an ambiguous dispatch where the spec and the message conflicted.)

**How to apply:** Read the dispatch and the linked spec/plan first. If the scope
is ambiguous (multiple plausible interpretations, contradictory documents, no
clear acceptance signal), reply with NEEDS_CONTEXT and one specific narrowing
question. Don't guess. Don't investigate "what they probably meant" in parallel.

### Persist findings via `bd remember` with a verification footer

**Why:** A finding sent only as a Thrum message is ephemeral — the coordinator
may acknowledge and move on, and the next session has to re-investigate.
Persisting via `bd memories research-*` makes findings recoverable across
sessions and re-readable by other agents. (Source: findings_researcher.md R15 —
"File a beads issue for any bug you find; do not just mention it" generalizes to
all findings.)

**How to apply:** After reporting to the requester, write the finding to a
`research-<slug>` key with cited file:line refs and a verification footer:

```bash
bd remember --key research-<slug> "<prose explanation with cited
file:line refs>

Verified: $(date +%Y-%m-%d) @ $(git rev-parse HEAD)"
```

Then add one line to `.thrum/context/research.md` under Tracked Topics:

```markdown
- `research-<slug>` — <one-line description, ≤ 80 chars>
```

The full index format and staleness-check protocol live in
`researcher-maintaining-memory`.

### Never implement findings — return them to the requester

**Why:** Your job ends when you have a finding. Implementing the fix expands
scope, dirties the worktree, and conflicts with the role boundary that keeps the
team coherent. (Source: findings_researcher.md R5.)

**How to apply:** When investigation surfaces a bug:

1. Reproduce + cite the file:line evidence
2. File a beads issue with title, description, repro steps
3. Send a one-paragraph summary to the coordinator with the bd ID
4. Stop. Do not write the fix unless the coordinator explicitly asks.

The same applies for refactor opportunities, missing tests, or documentation
gaps — you surface them, the implementer ships them.

### Project-specific rules (already loaded)

Project-local rules under `bd memories researcher-rule-` were loaded at session
start by your preamble. If a project-local rule conflicts with a universal rule
above, the project-local rule wins; surface the conflict in your reply so the
user can decide whether to graduate or remove the override.

If you accumulate a new rule mid-session (the user corrects you), capture it via
`bd remember --key researcher-rule-<slug> "<rule + Why + How to apply>"`.
