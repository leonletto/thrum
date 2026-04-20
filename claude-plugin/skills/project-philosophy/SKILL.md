---
name: project-philosophy
description: Use when a project needs its implementation philosophy established or updated — the canonical doc at .thrum/philosophy.md defining anti-patterns, red flags, and project-specific rules that implementation agents read at task-start time. First invocation generates from project inspection; subsequent invocations reconcile against current project state and propose diffs.
---

# Project Philosophy

## Canonical path

`.thrum/philosophy.md` — the single authoritative location. `project-setup` and every implementation-agent prompt read from this path only. Non-standard paths are handled by the fallback migration (documented below), not by moving the canonical target.

## Three run modes

On every invocation, dispatch by inspecting `.thrum/philosophy.md`:

1. **Absent** → **First-run**. Generate a new doc from project inspection + interactive prompts (documented below).
2. **Present and matches current project state** → **No-op exit**. Report "philosophy.md is current; no changes needed" and exit without writing.
3. **Present but project has drifted** → **Diff proposal**. Compute a sectional diff, present via `AskUserQuestion`, write only on confirmation.

The modes are idempotent — running the skill repeatedly on a stable project should produce no file changes after the first run.

A fourth path, **fallback migration**, runs before the First-run branch: if `.thrum/philosophy.md` is absent but a non-standard philosophy doc exists elsewhere in the repo, offer move/link/fresh before generating fresh.

## First-run mode

Triggered when `.thrum/philosophy.md` does not exist AND no non-standard philosophy doc is found (or the fallback migration is skipped via the "fresh" option).

### Step 1: Detect language

Inspect the repo root for manifest files and pick the primary language:

| File present | Language |
|---|---|
| `go.mod` | Go |
| `package.json` | JavaScript / TypeScript |
| `Cargo.toml` | Rust |
| `pyproject.toml` or `setup.py` or `requirements.txt` | Python |
| `pom.xml` or `build.gradle*` | Java / Kotlin |
| `Gemfile` | Ruby |
| `mix.exs` | Elixir |

If multiple are present, record the repo as polyglot and pick the most prominent by file count.

### Step 2: Detect framework

Grep the detected language's manifest for well-known framework markers:

- Go: `cmd/` layout + `net/http` or `gin-gonic` or `chi` → web service; `cobra` → CLI; pure library if neither
- JS/TS: `next` in `package.json` → Next.js; `react` without Next → React SPA; `express`/`fastify` → Node backend
- Python: `fastapi`/`flask`/`django` in dependencies → web backend; `pytorch`/`tensorflow` → ML
- Rust: `tokio`/`actix-web`/`axum` → async service; bin-only → CLI

### Step 3: Detect test harness

Check for test files and harness config:

- Go: `*_test.go` files; `testing` package import → stdlib; `testify` → assertions; `ginkgo` → BDD
- JS/TS: `__tests__/` or `*.test.ts`; `jest.config.*`, `vitest.config.*`, `playwright.config.*`
- Python: `tests/` dir; `pytest.ini`, `conftest.py`
- Rust: `#[cfg(test)]` blocks; `cargo test` always available

Record the harness in the rendered template so implementation-agent prompts know how to run tests.

### Step 4: Detect pattern conventions visible in existing code

Sample the codebase for:

- Error handling shape (Go: `if err != nil { return ..., fmt.Errorf(...) }`; Rust: `?` propagation; TS: try/catch vs. Result types)
- Logging convention (structured vs. `fmt.Println`; named logger vs. package-level)
- Public API shape (exported types, interface-heavy vs. struct-heavy)

Grep narrow — 20–50 lines is enough to surface the dominant convention. Don't read the whole repo; surface what's consistent, let the user correct via prompts.

### Step 5: Interactive prompts for project-specific rules

Use `AskUserQuestion` for items the codebase can't reveal:

- Known anti-patterns this team has hit (with links to post-mortems if any)
- Non-negotiable red flags (e.g. "never rm -rf a session dir")
- Deploy/release constraints (e.g. "no pushes to main from agents")
- External dependencies that require care (e.g. "never call prod Salesforce APIs from tests")

Each prompt should offer a clear default (usually "none"). Philosophy doc quality comes from real answers, not filler.

### Step 6: Render the template

Read `resources/philosophy-template.md` and interpolate the detected + provided values. Template interpolation uses simple placeholder substitution:

```
{{LANGUAGE}}        → "Go 1.22"
{{FRAMEWORK}}       → "cobra CLI + net/http server"
{{TEST_HARNESS}}    → "stdlib testing + testify assertions"
{{CONVENTIONS}}     → bullet list of detected patterns
{{ANTI_PATTERNS}}   → bullet list from user prompts
{{RED_FLAGS}}       → bullet list from user prompts
```

For any placeholder with no detected or provided value, leave a commented `<!-- TODO: … -->` line rather than silent emptiness — the user should see what's missing and can fill in later.

### Step 7: Write `.thrum/philosophy.md`

`mkdir -p .thrum/` then write the rendered content. Announce the path in the final message so the user knows where to edit it manually later.

<!-- Body continued in tasks 3-5 -->
