---
name: researcher-maintaining-memory
description:
  Use after completing research, when updating research memory, when verifying
  entries, or when working with the research index. Loads the format,
  seed-skeleton, and staleness-check protocol for the researcher's memory
  artifacts.
---

# Researcher: Maintaining Memory

## Index file structure

**Why:** A flat list of `bd memories research-*` keys is hard to scan when the
topic count grows past ~10. The thin index at `.thrum/context/research.md` gives
any agent a one-glance overview of what's tracked and where the open questions
live, while the full content lives in beads memories. (Source: spec section
"Researcher memory model".)

**How to apply:** Maintain three sections in `.thrum/context/research.md`:

- **Repo Map** — high-level architecture orientation, hand-maintained. One
  bullet per major package/area with a 1-line description of what it does.
- **Tracked Topics** — one line per `research-<slug>` key with a short
  description: ``- `research-<slug>` — <≤ 80-char summary>``
- **Open Questions** — investigations not yet started. Include the filed bd
  issue ID when one exists.

Keep the index file under ~200 lines. If it grows past that, the right move is
usually to retire stale topics (see `bd forget` below) rather than expand
sections.

## Seed the skeleton on first session

**Why:** A fresh thrum init in a new repo doesn't ship
`.thrum/context/research.md` — the file is researcher-curated, so researchers
seed it on first registration. Without seeding, each session re-derives "what
should this file look like" from memory and produces slightly different shapes
per researcher. (Source: spec section "`.thrum/context/research.md` — thin
index" + "First `@researcher` registration" in Bootstrap & invocation flow.)

**How to apply:** On session start, check whether `.thrum/context/research.md`
exists. If missing, write the skeleton below verbatim via the `Write` tool, then
proceed with the assigned task. If present, leave it alone — only the curated
content evolves.

The skeleton (paste this exactly):

```markdown
# Research Memory — <repo>

The actual findings live as beads memories with the `research-` prefix. Search:
`bd memories research-<keyword>` · Read: `bd memories <key>`

## Repo Map (high-level, hand-maintained)

- internal/daemon/ — RPC server, WebSocket + Unix socket, event log
- internal/sync/ — Tailscale-based sync (replaced git a-sync v0.6.1)
- ui/packages/... — React 19 SPA + shared-logic
- ...

## Tracked Topics

- `research-auth-flow` — How peercred authenticates a registered caller
- `research-message-routing` — CLI → daemon → recipient inbox path
- `research-sync-mechanism` — Tailscale handshake + bidirectional WS
- ...

## Open Questions (not yet investigated)

- How does HandleStatus interact with @thrum-managed=1 sessions on dev machines?
  (filed: thrum-zuz5)
- ...
```

The "..." sentinels in the skeleton are intentional — they mark each section as
expandable. Replace the example bullets with this repo's actual contents as you
discover them.

## `research-<slug>` and `research-mod-<module>-<slug>` namespaces

**Why:** Namespacing prevents silent overwrite when future installable modules
(`thrum module install <name>`) start writing their own research entries. User
captures stay under the bare prefix; module installs reserve the `mod-<module>-`
sub-segment. The convention costs nothing in v1 (no module tooling exists yet)
and prevents a class of bugs once it does. (Source: spec section "Forward
extension — thrum modules".)

**How to apply:** Choose the slug — lowercase, hyphen-separated, descriptive.
User captures: `research-<slug>` (e.g. `research-auth-flow`). Module installs
(forward, when tooling lands): `research-mod-<module-name>-<slug>`.

## Verification-stamp footer format

**Why:** The footer makes a `bd memories` entry self-describing — anyone reading
it sees when it was last verified and against what commit. Without the stamp, an
entry from six months ago looks the same as one from yesterday, and downstream
consumers can't tell which is trustworthy. (Source: spec section "`bd remember`
entries — per-topic content".)

**How to apply:** Every entry ends with `Verified: YYYY-MM-DD @ <commit-sha>`.
The SHA is `git rev-parse HEAD` at verification time. Cite file:line refs in the
prose for any multi-file claim. Concrete example:

```bash
bd remember --key research-auth-flow "Authentication of registered callers
happens in internal/daemon/identity/peercred/resolver_unix.go (5-step
pipeline). Steps 1-2 return raw errors so server.go falls through to
legacy client-asserted identity. Steps 3+5 wrap ErrAnonymous when truly
anonymous.

Verified: 2026-04-24 @ 43cf52f30"
```

## Staleness check via `git diff` filtered by cited paths

**Why:** Research entries can age out without anyone noticing — the underlying
code changes, the entry's claim becomes wrong, but the entry is still cached and
gets quoted. The staleness check is a single `git diff` filtered by the entry's
cited paths: if any cited file appears in the diff between the stamp and HEAD,
re-verify before answering; otherwise the cached entry stands. (Source: spec
section "Researcher memory model".)

**How to apply:** Before answering a query backed by a cached entry:

```bash
# Extract stamp SHA from the entry
STAMP_SHA="<from the Verified: footer>"
# Extract cited paths from the entry's prose (e.g. "internal/daemon/...")
CITED="<paths>"

git diff --name-only "$STAMP_SHA" HEAD -- $CITED
```

If the output is empty, the entry stands as-is — answer with confidence. If any
path appears, re-verify the entry (re-read the cited code, update the prose if
needed, refresh the footer with `Verified: <today> @ <new-sha>`), then answer.

## Removal: `bd forget` AND remove the index line

**Why:** Orphaned index entries — pointing at a `research-<slug>` that no longer
exists in `bd memories` — are a bug. Anyone reading the index sees the entry,
looks it up, and gets nothing. Both halves of the removal must happen together.
(Source: spec section "Researcher memory model" — "Removal:
`bd forget research-X` AND remove the index line — orphaned index entries are a
bug".)

**How to apply:** When retiring a topic, run `bd forget research-<slug>` AND
remove the matching line under Tracked Topics in `.thrum/context/research.md`.
If the topic is graduating to module-installed status (rare), document the
graduation in the commit message rather than just deleting.

## Project-specific rules (already loaded)

Project-local rules under `bd memories researcher-rule-` were loaded at session
start by your preamble. If a project-local rule conflicts with a universal rule
above, the project-local rule wins; surface the conflict in your reply so the
user can decide whether to graduate or remove the override.

If you accumulate a new rule mid-session (the user corrects you), capture it via
`bd remember --key researcher-rule-<slug> "<rule + Why + How to apply>"`.
