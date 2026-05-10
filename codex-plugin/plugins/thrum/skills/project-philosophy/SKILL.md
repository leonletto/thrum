---
name: project-philosophy
description: "Use when a project needs its implementation philosophy established or updated — the canonical doc at .thrum/philosophy.md defining anti-patterns, red flags, and project-specific rules that implementation agents read at task-start time. First invocation generates from project inspection; subsequent invocations reconcile against current project state and propose diffs."
# source: claude-plugin/skills/project-philosophy/SKILL.md
# generated-by: scripts/sync-skills.sh
---


## Project Philosophy

### Canonical path

`.thrum/philosophy.md` — the single authoritative location. `project-setup` and
every implementation-agent prompt read from this path only. Non-standard paths
are handled by the fallback migration (documented below), not by moving the
canonical target.

### Run modes and dispatch order

On every invocation, dispatch in this order. Earlier branches short-circuit
later ones.

0. **Fallback migration** — runs first when `.thrum/philosophy.md` is absent. If
   a legacy philosophy doc exists elsewhere in the repo, offer move/link/fresh
   before generating anything new. If no legacy doc is found, fall through to
   branch 1.
1. **First-run** (doc absent, no legacy doc or "fresh" chosen above) — generate
   a new `.thrum/philosophy.md` from project inspection + interactive prompts
   (documented below).
2. **Re-run-unchanged** (doc present, matches current project state) — report
   "philosophy.md is current; no changes needed" and exit without writing.
3. **Re-run-evolved** (doc present, project has drifted) — compute a sectional
   diff, present via `AskUserQuestion`, write only on confirmation.

The modes are idempotent — running the skill repeatedly on a stable project
should produce no file changes after the first run.

> **Note: human-in-the-loop required.** This skill uses `AskUserQuestion` for
> first-run project-specific prompts, re-run-evolved diff approval, and
> fallback-migration three-way choice. It cannot run unattended in CI or any
> other non-interactive automation context. Invoke it only from a session that
> can answer prompts.

### Fallback migration

Runs before the First-run branch when `.thrum/philosophy.md` is absent. If the
user has a philosophy doc at a legacy path, offer a three-way choice rather than
ignoring it and generating a parallel doc.

#### Step 1: Search for legacy philosophy docs

```bash
find . -type f \( -name "*philosophy*" -o -name "*anti-pattern*" -o -name "*implementation-standards*" -o -name "CONTRIBUTING.md" \) \
  -not -path '*/node_modules/*' \
  -not -path '*/.git/*' \
  -not -path '*/dist/*' \
  -not -path '*/build/*' \
  2>/dev/null | head
```

Common legacy locations: `dev-docs/implementation-philosophy.md`,
`docs/anti-patterns.md`, `CONTRIBUTING.md` with an anti-patterns section.

If zero candidates: fall through to First-run. If one or more candidates:
present the choice.

#### Step 2: Present three-way choice via `AskUserQuestion`

Question: "Found existing philosophy doc at `<path>`. What should I do?"

Options:

- **Move** — `git mv <path> .thrum/philosophy.md`. Preserves history via rename;
  the file moves to the canonical location and the legacy path stops existing.
- **Link** — Create `.thrum/philosophy.md` with a single line: `See: <path>`.
  The legacy doc stays in place; the canonical path becomes a pointer. Useful
  when the legacy path is load-bearing (e.g., referenced from a
  `CONTRIBUTING.md` that can't be retargeted cheaply).
- **Fresh** — Ignore the legacy doc; generate a new `.thrum/philosophy.md` via
  the First-run flow. The legacy doc stays unmodified. Useful when the legacy
  doc is stale or incomplete.

If multiple candidates were found, ask which one first (separate
`AskUserQuestion`) before offering move/link/fresh.

#### Step 3: Execute the chosen option

**Move path:**

```bash
mkdir -p .thrum/
git mv <legacy-path> .thrum/philosophy.md
```

`git mv` requires the repo to be initialized with at least one commit on the
current branch. If the repo has no commits (fresh `git init`), fall back to a
plain `mv` + `git add`, and note in the final message that rename history is not
preserved.

**Link path:**

```bash
mkdir -p .thrum/
printf 'See: %s\n' "<legacy-path>" > .thrum/philosophy.md
```

**Fresh path:** Drop into First-run mode without touching the legacy file.

#### Step 4: Announce and continue

Tell the user what was done and where the canonical doc now lives. If `move` or
`link` was chosen, exit (the doc is materialized — no further generation
needed). If `fresh` was chosen, continue into First-run Step 1.

### First-run mode

Triggered when `.thrum/philosophy.md` does not exist AND no non-standard
philosophy doc is found (or the fallback migration is skipped via the "fresh"
option).

#### Step 1: Detect language

Inspect the repo root for manifest files and pick the primary language:

| File present                                         | Language                |
| ---------------------------------------------------- | ----------------------- |
| `go.mod`                                             | Go                      |
| `package.json`                                       | JavaScript / TypeScript |
| `Cargo.toml`                                         | Rust                    |
| `pyproject.toml` or `setup.py` or `requirements.txt` | Python                  |
| `pom.xml` or `build.gradle*`                         | Java / Kotlin           |
| `Gemfile`                                            | Ruby                    |
| `mix.exs`                                            | Elixir                  |

If multiple are present, record the repo as polyglot and pick the most prominent
by file count.

#### Step 2: Detect framework

Grep the detected language's manifest for well-known framework markers:

- Go: `cmd/` layout + `net/http` or `gin-gonic` or `chi` → web service; `cobra`
  → CLI; pure library if neither
- JS/TS: `next` in `package.json` → Next.js; `react` without Next → React SPA;
  `express`/`fastify` → Node backend
- Python: `fastapi`/`flask`/`django` in dependencies → web backend;
  `pytorch`/`tensorflow` → ML
- Rust: `tokio`/`actix-web`/`axum` → async service; bin-only → CLI

#### Step 3: Detect test harness

Check for test files and harness config:

- Go: `*_test.go` files; `testing` package import → stdlib; `testify` →
  assertions; `ginkgo` → BDD
- JS/TS: `__tests__/` or `*.test.ts`; `jest.config.*`, `vitest.config.*`,
  `playwright.config.*`
- Python: `tests/` dir; `pytest.ini`, `conftest.py`
- Rust: `#[cfg(test)]` blocks; `cargo test` always available

Record the harness in the rendered template so implementation-agent prompts know
how to run tests.

#### Step 4: Detect pattern conventions visible in existing code

Sample the codebase for:

- Error handling shape (Go: `if err != nil { return ..., fmt.Errorf(...) }`;
  Rust: `?` propagation; TS: try/catch vs. Result types)
- Logging convention (structured vs. `fmt.Println`; named logger vs.
  package-level)
- Public API shape (exported types, interface-heavy vs. struct-heavy)

Grep narrow — 20–50 lines is enough to surface the dominant convention. Don't
read the whole repo; surface what's consistent, let the user correct via
prompts.

#### Step 5: Interactive prompts for project-specific rules

Use `AskUserQuestion` for items the codebase can't reveal:

- Known anti-patterns this team has hit (with links to post-mortems if any)
- Non-negotiable red flags (e.g. "never rm -rf a session dir")
- Deploy/release constraints (e.g. "no pushes to main from agents")
- External dependencies that require care (e.g. "never call prod Salesforce APIs
  from tests")

Each prompt should offer a clear default (usually "none"). Philosophy doc
quality comes from real answers, not filler.

#### Step 6: Render the template

Read `resources/philosophy-template.md` and interpolate the detected + provided
values. Template interpolation uses simple placeholder substitution:

```text
{{LANGUAGE}}        → "Go 1.22"
{{FRAMEWORK}}       → "cobra CLI + net/http server"
{{TEST_HARNESS}}    → "stdlib testing + testify assertions"
{{CONVENTIONS}}     → bullet list of detected patterns
{{ANTI_PATTERNS}}   → bullet list from user prompts
{{RED_FLAGS}}       → bullet list from user prompts
```

For any placeholder with no detected or provided value, leave a commented
`<!-- TODO: … -->` line rather than silent emptiness — the user should see
what's missing and can fill in later.

#### Step 7: Write `.thrum/philosophy.md`

`mkdir -p .thrum/` then write the rendered content. Announce the path in the
final message so the user knows where to edit it manually later.

### Re-run-unchanged mode

Triggered when `.thrum/philosophy.md` exists. The goal is a fast,
side-effect-free sanity check that confirms the doc is still accurate for the
current project state.

#### Step 1: Read the existing doc

Read `.thrum/philosophy.md` in full. Parse its sections — at minimum the
language/framework line, the test harness line, and the anti-patterns list.

#### Step 2: Sanity-check against current project state

Walk the same detection steps as first-run, but compare rather than write:

- Language / framework / test harness — do the current manifest files agree with
  what the doc claims?
- Anti-patterns in `CLAUDE.md` (or equivalent) — is every anti-pattern in
  CLAUDE.md reflected in the doc?
- Linters or test configs recently added — are they acknowledged?

Each check is a boolean "matches" vs. "differs". Keep the comparison narrow;
this mode should not do deep inspection.

#### Step 3: If nothing differs — no-op exit

Print:

> `.thrum/philosophy.md` is current; no changes needed.

Do NOT write to the file. Do NOT update mtime. Do NOT create backups. Exit
cleanly.

#### Step 4: If anything differs — hand off to evolved mode

If at least one check reports "differs", transition to the re-run-evolved flow
(below). The drift detection from Step 2 becomes the input to the diff proposal.

Idempotency invariant: re-running `/project-philosophy` on an unchanged project
must never modify any file. Tests for this mode should confirm mtime is
untouched — `stat -c %Y .thrum/philosophy.md` on Linux,
`stat -f %m .thrum/philosophy.md` on macOS.

### Re-run-evolved mode

Triggered when `.thrum/philosophy.md` exists AND the unchanged-mode sanity check
flagged drift. This is the load-bearing idempotency path — philosophy docs
accumulate learned anti-patterns over time, so silent overwrites destroy
context. The merge-proposal shape is the only correct shape.

> **Never silently overwrite.** Philosophy docs are not regenerated — they are
> evolved. If the user has added hand-written anti-patterns, post-mortem
> references, or team-specific notes, those must survive every re-run. A write
> without explicit user confirmation is a bug.

#### Step 1: Read the existing doc

Same as re-run-unchanged Step 1. Keep the parsed sections in memory so the diff
proposal can cite them.

#### Step 2: Detect drift

Four drift categories, conservative on purpose:

- **Language/framework version change.** `go.mod`'s `go 1.X` differs from the
  doc's stated version; `package.json` lists `next@15.x` but doc says
  `next@14.x`.
- **New test patterns in codebase.** `ginkgo` imports appear but the doc lists
  only `stdlib testing`. New `playwright.config.ts` exists but doc doesn't
  mention E2E.
- **New anti-patterns recorded in CLAUDE.md since last run.** Compare
  anti-pattern bullets in CLAUDE.md against the doc's `{{ANTI_PATTERNS}}`
  section; anything new in CLAUDE.md is a drift candidate.
- **New linters or code-quality tooling added.** `.golangci.yml`,
  `eslint.config.js`, `ruff.toml` that weren't mentioned in the doc.

Start conservative. Expand the detector list only based on real-use feedback —
over-eager drift detection pesters users and erodes trust in the skill.

#### Step 3: Compute a proposed diff (sectional)

Present drift as a readable sectional diff, one item per drift category. Each
item should have:

- The detected change in plain language
- A proposed edit (insertion / replacement / addition at the bottom of a
  section)
- A one-line rationale

Example shape:

```markdown
### Proposed updates to `.thrum/philosophy.md`

**1. Framework version bump**

- Current: "Next.js 14"
- Proposed: "Next.js 15"
- Rationale: `package.json` lists `"next": "^15.0.0"`

**2. New anti-pattern from CLAUDE.md**

- Proposed addition under "## Anti-patterns":
  > - Never run migrations from an agent session — always via the release
  >   pipeline.
- Rationale: Added to CLAUDE.md in commit abc1234 after the 2026-03 incident.
```

#### Step 4: Present via `AskUserQuestion`

Use `AskUserQuestion` with:

- A concise summary question: "3 drifts detected against `.thrum/philosophy.md`.
  Apply all, select, or decline?"
- Options: `Apply all`, `Select items to apply`, `Decline (keep doc as-is)`
- Include the full sectional diff from Step 3 in the question context so the
  user can see what they're approving

If the user picks `Select items`, fire a follow-up `AskUserQuestion` with
per-item yes/no choices.

#### Step 5: Write only on confirmation

Apply approved edits in-place. For each applied edit:

- Preserve surrounding content (don't re-render the whole template)
- Append a provenance comment near the change:
  `<!-- updated YYYY-MM-DD via project-philosophy re-run-evolved -->`

Write the file once, at the end. Announce the applied items in the final
message.

#### Step 6: On decline — no write + log

If the user declines (or selects zero items), do NOT write to the file. Record
the skipped proposal in `.thrum/philosophy-skipped.jsonl` (append-only, one JSON
line per decline) with the timestamp and the drift items proposed. Future runs
can re-surface the same items without re-computing drift from scratch.

Skipped-proposal log is advisory — its absence is not an error.
