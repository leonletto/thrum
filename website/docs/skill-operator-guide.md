---
title: "Skill Operator Guide"
description:
  "End-to-end operator guide for the v0.11 per-project skill lifecycle:
  drafting, reviewing, promoting, and troubleshooting skills under
  .thrum/skills/."
category: "skills"
order: 1
tags:
  ["skills", "operator", "v0.11", "promote", "secret-scan", "staleness"]
last_updated: "2026-05-17"
---

## Skill Operator Guide

This guide walks you — the human running this project — through the
v0.11 skill lifecycle: how proposed skills become promoted ones, what
the coordinator review flow looks like at the CLI, and how to handle
the rough edges (secret-scan blocks, the C-B2 check-the-skill stub,
multi-machine git, troubleshooting).

> The conceptual framing for why per-project skills exist alongside
> plugin-bundled skills lives in
> [the C-B1 design spec §1](../specs/2026-05-15-thrum-agents-c-b1-design.md#1-context--scope).
> This page is the operational side: the verbs you type, the files you
> touch, the failure modes you'll see.

## 1. What skills are

A **skill** is a markdown document at `.thrum/skills/<name>/SKILL.md`
that any runtime in the repo can discover at agent-load time. The
canonical SKILL.md is YAML frontmatter (schema in spec §9.2) plus a
prose body. Agents read skills the same way they read plugin-bundled
skills today; the per-project surface is for institutional knowledge
that lives with this repo specifically.

| Surface | Lives in | Visible to | Authored by |
| --- | --- | --- | --- |
| Plugin-bundled skills | Plugin marketplace | Every project using that plugin | Plugin author |
| Per-project skills (v0.11) | `.thrum/skills/<name>/` | Every runtime in this repo | Coordinator (after a propose-review-promote cycle) |

Plugin-bundled skills are the right surface for content reusable across
projects ("How to write a good commit message"). Per-project skills are
the right surface for content tied to *this* codebase ("How we handle
auth migrations on the foo-service").

## 2. Drafting a proposed skill

Non-coordinator agents draft a SKILL.md by writing it directly to:

```
.thrum/agents/<your-agent-id>/proposed-skills/<skill-name>/SKILL.md
```

There is **no `thrum skill propose` verb** — the filesystem write IS
the proposal. The daemon's watcher detects the new file and:

1. Parses the frontmatter (invalid YAML doesn't block the propose;
   it surfaces a `frontmatter_invalid` flag in the listing).
2. Notifies every coordinator-role agent in the repo via the
   supervisor inbox.
3. Mints a staleness reminder via the A-B4 reminders substrate so the
   proposal doesn't sit unreviewed for >48 hours (default).

The minimum frontmatter for a propose is:

```yaml
---
name: my-skill                       # required; must match the dir name
description: "What this skill covers" # required for ValidateProposed
thrum:
  proposed_by: "@your-agent-id"      # required
  trigger_reason: "why this skill"   # required
---
```

The promote-time stamper fills in the rest (`thrum.promoted_by`,
`thrum.created_at`, `thrum.review.*`). See spec §13.1 and §9.2 for the
full schema.

## 3. Coordinator review flow

You — wearing your coordinator hat — review and promote with three
verbs:

```bash
thrum skill list --pending                          # see in-flight proposals
thrum skill show .thrum/agents/@alice/proposed-skills/widget/SKILL.md
thrum skill revise <path> "<findings text>"        # send revision feedback
thrum skill promote <path>                          # land it in .thrum/skills/
```

`thrum skill list --pending` returns the open proposals with their
age and author so you can triage by what's been waiting longest.
`--proposed-by @agent` scopes the listing to a single submitter.

`thrum skill show <path>` renders the frontmatter + body in human-readable
form. Pass `--raw` to also include the raw file contents (useful for an
edit-promote diff).

`thrum skill revise <path> "<findings>"` packages your findings into a
structured inbox message and sends it to the submitter — the daemon
never writes into the submitter's proposed-skills directory. The
submitter revises in place; the watcher picks up the edit and re-emits
the notification.

`thrum skill promote <path>` runs the gate sequence:

1. Coordinator-role auth (you must be registered as `role=coordinator`).
2. Frontmatter validation (returns `frontmatter_invalid` if anything's
   missing).
3. Secret-scan (returns `secret_scan_blocked` on hit; see §4 below).
4. Atomic move into `.thrum/skills/<name>/`, with rollback on rename
   failure.
5. Provenance stamping (your agent ID into `thrum.promoted_by`, now
   into `thrum.created_at`, etc.).
6. Inbox fanout to every non-supervisor agent in the repo.
7. Staleness reminder cancelled (no more pending nudges for this name).

Full request/response shapes are in spec §7.5.

## 4. `--force` semantics + the C-B2 stub window

v0.11 ships with `thrum skill check` as a **stub** — the
check-the-skill meta-skill that performs the admission review hasn't
landed yet (it's on the C-B2 roadmap). The stub returns the canonical
error:

```
$ thrum skill check .thrum/agents/@alice/proposed-skills/widget/SKILL.md
check-the-skill meta-skill not implemented in this build. Use
'thrum skill promote --force <path>' to bypass the admission gate, or
wait for C-B2 to ship.
```

Exit code is 2 to distinguish from a normal failure (exit 1) per
spec §7.3. This is the **documented escape hatch** during the gap —
see [canonical §8.3](../specs/canonical-thrum-reference.md#83-stub-and-ship-broken)
for the stub-and-ship-broken decision rationale.

`thrum skill promote --force <path>` skips the (non-existent live)
check-the-skill gate. **Secret-scan still runs even with `--force`** —
you cannot bypass secret-scan from the CLI under any circumstance.
That's a deliberate one-way valve: a coordinator who needs to ship a
proposal with a real-looking-but-fake secret must use
`--allow-secret '<regex>'` instead, which records the override in
`review.secret_scan_overrides[]` for audit.

When you use `--force`, pair it with `--force-reason "<why>"` so the
audit string in `review.force_override` isn't empty:

```bash
thrum skill promote .thrum/agents/@alice/proposed-skills/widget/SKILL.md \
    --force \
    --force-reason "C-B2 not shipped yet; coordinator-verified content"
```

Every fanout recipient sees a `[FORCE OVERRIDE: ...]` marker on their
inbox notification so the bypass is loud rather than silent.

## 5. Per-runtime two-tier matrix

Skills mirror from `.thrum/skills/<name>/` to per-worktree runtime
locations via the mirror substrate (spec §11–§12). Which runtimes get
which surface:

| Runtime | Mirror destination | Live-reload | Tier |
| --- | --- | --- | --- |
| Claude Code | `<worktree>/.claude/skills/<name>/` | New chat session | Tier 1 (live mirror) |
| OpenAI Codex | `<worktree>/.codex/skills/<name>/` | New chat session | Tier 1 (live mirror) |
| Other runtimes | (no v0.11 mirror) | n/a | Tier 2 (read .thrum/skills/ directly) |

Tier 1 runtimes get fsnotify-debounced live updates: a promote at one
worktree propagates to every worktree's mirror destination within
~500ms. Tier 2 runtimes have no mirror — they (or their plugins) must
read from `.thrum/skills/` directly.

If a runtime's mirror destination gets out of sync (manual edit,
clock skew between machines), run:

```bash
thrum skill sync          # full reconcile, every worktree, every runtime
thrum skill sync foo bar  # scoped: only "foo" and "bar" get refreshed
```

A scoped sync leaves unrelated mirror destinations untouched — useful
when you've hand-edited one runtime's copy for debugging and don't
want a full reconcile to wipe your scratch work.

## 6. Multi-machine git workflow

`.thrum/skills/` is **committed** to the repo. Promoted skills travel
with the codebase: a new clone on another machine sees the same
skill library after `git pull`. Pending proposals under
`.thrum/agents/<author>/proposed-skills/` are **gitignored** (LOCAL-only
per spec §9.1) — they survive a `thrum daemon restart` because the
watcher rescans the directory at boot and re-mints any staleness
reminders for existing proposals, not because they ship with git.

What stays out of git:

- `.thrum/state/skill-proposal-reminders.jsonl` — per-daemon sidecar
  for the staleness reminder map. Gitignored; each daemon rebuilds
  from the `reminders` table at boot.
- Plugin-bundled skills — these live in the plugin marketplace,
  installed via `thrum plugin install`. Never check them into
  `.thrum/skills/`.

On a fresh clone or after pulling someone else's promote:

```bash
thrum skill sync           # re-mirror every promoted skill to every runtime
thrum skill validate       # sanity-check frontmatter (catches merge conflicts)
```

If two machines promoted skills with the same name independently, the
git merge produces a SKILL.md with two top-level `thrum:` blocks.
`thrum skill validate` flags this as `duplicate_provenance` — see §7
below for the fix.

## 7. Troubleshooting

### `secret_scan_blocked` on promote

The scanner caught a known pattern in the proposal body or
frontmatter. The response includes `{path, line, pattern_category}`
tuples — the matched string itself is **never** logged or returned
(privacy guarantee per spec §14.3).

If the match is a real secret, remove it from the proposal and have
the submitter revise. If it's a documented test fixture or fake key,
override the specific pattern with an audit reason:

```bash
thrum skill promote <path> \
    --allow-secret 'sk_live_[0-9a-zA-Z]+' \
    --allow-secret-reason "documented fake Stripe key in test fixture"
```

The override is recorded in `review.secret_scan_overrides[]` —
future promotes (e.g. an edit-promote of the same skill) must re-grant
the override. Don't blanket-allow patterns; the audit trail loses
value when every override says "noisy".

### `frontmatter_invalid` on promote

The validator surfaces a list of findings — typically a missing
required field (`thrum.proposed_by`, `thrum.trigger_reason`) or a
regex violation on the `name` field. Fix the SKILL.md in place; the
watcher picks up the edit and the promote can retry.

### Stuck staleness reminder

If you promoted a skill but the staleness reminder is still firing,
either (a) the cancel best-effort path failed at promote time (check
the daemon log for `skill staleness reminder cancelled`), or (b) the
proposal path on disk doesn't match the path stored in the sidecar.

Manual recovery: locate the reminder ID via `thrum agent reminder list`
(coordinator-role can pass `--agent <author>` to scope to the proposing
agent) or inspect `.thrum/state/skill-proposal-reminders.jsonl`
directly; then `thrum agent reminder <id> --clear` to dismiss (or
`--cancel` if the reminder was wrong to start with — see `thrum agent
reminder --help` for the semantic distinction).

### `duplicate_provenance` from validate

A git merge between two clones that both promoted skills with the
same name produced a SKILL.md with two top-level `thrum:` blocks.
yaml.v3 silently collapses to the second on Decode, but
`thrum skill validate` walks the raw MappingNode and flags it.

Fix manually: edit the SKILL.md to keep one `thrum:` block, choosing
the desired provenance (usually the later promote's). After the edit,
`thrum skill validate <name>` should return `ok`.

### Mirror gaps after a manual edit

If you edited a runtime mirror file directly (e.g.
`<worktree>/.claude/skills/foo/SKILL.md`) and want to undo, run:

```bash
thrum skill sync foo
```

This re-applies the canonical content from `.thrum/skills/foo/` and
removes any drift.

### CLI exit codes

| Verb | Exit 0 | Exit 1 | Exit 2 |
| --- | --- | --- | --- |
| `thrum skill list` | success | any error | n/a |
| `thrum skill show` | success | any error | n/a |
| `thrum skill check` | n/a (always errors in v0.11 stub) | any other error | C-B2 stub error |
| `thrum skill promote` | success | any error | n/a |
| `thrum skill delete` | success | any error / aborted | n/a |
| `thrum skill revise` | success | any error | n/a |
| `thrum skill sync` | success | any error | n/a |
| `thrum skill validate` | all `ok` | any non-`ok` result | n/a |

`thrum skill validate` is suitable as a pre-commit hook entry or CI
gate — the exit-1 path lets the hook block a commit that would
corrupt the skill library.

---

## See also

- [C-B1 design spec](../specs/2026-05-15-thrum-agents-c-b1-design.md) —
  authoritative wire contract for every `skill.*` RPC
- [Canonical §8.3](../specs/canonical-thrum-reference.md#83-stub-and-ship-broken) —
  rationale for the C-B2 check-the-skill stub
- [Canonical §8.5](../specs/canonical-thrum-reference.md#85-lean-prime) —
  how the skill-library check fits the lean-prime path
- [Canonical §8.6](../specs/canonical-thrum-reference.md#86-ephemeral-worktrees) —
  how skill mirror interacts with ephemeral worktrees
