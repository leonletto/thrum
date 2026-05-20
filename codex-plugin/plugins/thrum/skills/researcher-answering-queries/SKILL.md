---
name: researcher-answering-queries
description:
  "Use when another agent has asked you a research question, when fielding a
  research request, or when responding to a query. Loads the lookup-and-respond
  protocol so cached findings get reused before fresh investigation starts."
# source: claude-plugin/skills/researcher-answering-queries/SKILL.md
# generated-by: scripts/sync-skills.sh
---

## Researcher: Answering Queries

### Lookup order: index → bd memories → staleness check → respond

**Why:** A query that's already been answered shouldn't trigger a fresh
investigation. The cached `research-<slug>` entries exist precisely so repeat
questions resolve cheaply. Skipping the cache and re-investigating duplicates
effort, burns context, and introduces drift between the cached entry and the new
answer. (Source: spec section "Researcher skills" row 3 — "Lookup order: (1)
check `research.md` index, (2) `bd memories research-<key>` for content, (3)
verify if stamp is stale, (4) respond".)

**How to apply:** When a query lands, work the steps in order:

1. **Index check.** Read `.thrum/context/research.md`. Does any Tracked Topic
   line look relevant? Note the `research-<slug>` keys.
2. **Content fetch.** For each candidate key, `bd memories <slug>` to read the
   full entry.
3. **Staleness check.** Use the protocol from `researcher-maintaining-memory`:
   `git diff --name-only <stamp-sha> HEAD` filtered by the entry's cited paths.
   If empty, the entry stands. If any cited path appears, re-verify (re-read the
   cited code, refresh the footer with `Verified: <today> @ <new-sha>`) before
   responding.
4. **Respond.** With the certainty level from the section below.

If the index has no relevant entry, the query is fresh-investigation territory —
invoke the `researcher-investigating` skill's discipline instead.

### Structure responses by certainty level

**Why:** A response that conflates "I just verified this against HEAD" with "I'm
pulling this from a stale cached entry" misleads the requester into trusting
outdated state. Calibrated certainty lets the requester decide whether to act
now or ask for re-verification.

**How to apply:** Three response shapes:

- **Verified now.** "<answer>. Verified at HEAD `<sha>`: <evidence>." Use after
  a fresh investigation or a successful staleness re-verify.
- **Cached + stamp.** "<answer>. Cached entry `research-<slug>`, Verified
  `<date>` @ `<stamp-sha>`. Cited paths unchanged in
  `git diff <stamp-sha>..HEAD`." Use when the staleness check returns empty.
- **Unknown / partial.** "I don't have a verified answer for X. The closest
  cached entry is `research-<slug>` (last verified <date>) but it doesn't fully
  cover the question. Want me to investigate?" Use when no entry covers the
  question, or the cached entry only partially addresses it.

Don't fudge by saying "Yes" without a level qualifier. The qualifier costs five
words; the cost of silently propagating stale state is real.

### Point at the key for follow-up — don't dump the whole entry inline

**Why:** A long cached entry quoted inline burns tokens and clutters the
requester's context. The slug is the durable handle; the requester can
`bd memories <slug>` themselves if they want the full body.

**How to apply:** In the response, cite the key (`research-<slug>`) and a
one-paragraph summary (or the specific sub-fact the requester asked for). If the
requester needs more, they fetch via `bd memories <slug>`. Reserve full quotes
for short entries (<5 lines) where inlining is genuinely cheaper than the
round-trip.

### Project-specific rules (already loaded)

Project-local rules under `bd memories researcher-rule-` were loaded at session
start by your preamble. If a project-local rule conflicts with a universal rule
above, the project-local rule wins; surface the conflict in your reply so the
user can decide whether to graduate or remove the override.

If you accumulate a new rule mid-session (the user corrects you), capture it via
`bd remember --key researcher-rule-<slug> "<rule + Why + How to apply>"`.
