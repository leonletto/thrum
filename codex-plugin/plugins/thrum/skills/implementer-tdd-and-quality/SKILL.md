---
name: implementer-tdd-and-quality
description:
  "Use when writing tests, running tests, hitting a quality gate, or before
  reporting a task done. Loads project-specific test and quality discipline that
  complements superpowers:test-driven-development."
# source: claude-plugin/skills/implementer-tdd-and-quality/SKILL.md
# generated-by: scripts/sync-skills.sh
---

## Implementer: TDD and Quality

This skill **complements** `superpowers:test-driven-development` — it does not
restate the red-green-refactor discipline, the iron law on watching tests fail,
or any other content from that skill. Layer this content on top of those
universals: the rules below capture project-specific Go conventions and
quality-gate practice.

### Race detector is mandatory

**Why:** Thrum's daemon has significant concurrent state (goroutines for
WebSocket, Unix socket, sync, telegram bridge, peer transport). Race conditions
that pass bare `go test` fail under `-race` — sometimes with silent data
corruption that takes hours to track down later. (Source:
findings_implementer.md — "Run tests with the race detector, not bare".) The
Makefile's `make test` includes `-race` by default for exactly this reason.

**How to apply:** Always run `go test -race ./...` (or scope to specific
packages with `-race`). Never report tests passing without having run with
`-race`. For a specific package:
`go test ./internal/daemon/... -race -count=1 -timeout=120s`. For everything:
`make test`.

### Scope test runs to packages you touched

**Why:** Running the entire test tree on every iteration of a fix-test loop is
wasteful — most packages weren't touched and their tests will pass identically
every iteration. Scoping speeds the inner loop without sacrificing correctness;
the full tree gets one final run before reporting done.

**How to apply:** During the inner fix-test loop, run only the affected
packages: `go test -race ./internal/<pkg>/... -run <TestName>`. Once the scoped
tests pass and you're ready to report done, run `make test` once to confirm
nothing else broke.

### Use `t.TempDir()` for filesystem fixtures — never hardcoded paths

**Why:** Hardcoded absolute paths (especially the implementer's home directory)
only work on one machine. (Source: findings_implementer.md — virtual-supervisor
2026-04-17 wrote `TestSupervisorIdentity_Shape` with a hardcoded
`/Users/leon/dev/opensource/thrum`. Review caught it as a SHOULD FIX; the fix
was two characters: `t.TempDir()`.) The same pattern shows up with
`os.UserHomeDir()` results being baked into expected values.

**How to apply:** Any test that creates a temporary directory, writes files, or
needs an isolated git repo must use `t.TempDir()`. It is cleaned up
automatically and is unique per test run. If you need a git repo, initialize it
in the temp dir: `cmd := exec.Command("git", "init", t.TempDir())`. For fixtures
comparing paths, capture the temp dir into a variable and reference it in
expected values — never hardcode.

### Avoid `go vet ./...` regressions before reporting

**Why:** `go vet` catches a class of bugs that compile but are wrong:
unreachable code, suspicious type conversions, malformed printf args. Lint-clean
code that fails `vet` will get caught in CI or by review; running `vet` locally
catches it pre-commit when the fix is one line.

**How to apply:** Before reporting done, run `go vet ./...` and `make lint`.
Both should pass clean. If a `vet` finding looks like a false positive, prefer a
small annotation or refactor to silencing the warning.

### Two-stage self-review before pinging the coordinator

**Why:** A self-review pass before the coordinator's dual review reduces total
review round-trips. (Source: findings_implementer.md — virtual-supervisor
implementer found and fixed a real `safecmd` injection issue via self-review
before the coordinator's formal review, which meant fewer BLOCKING findings and
fewer iterations.) Self-review is not ceremony; it pre-pays the cost of issues
that would otherwise come back as findings.

**How to apply:** At task close, before sending DONE:

1. **Spec compliance pass.** Read each acceptance criterion from the task
   description. For every test case the spec mentions, verify it exists in the
   test file. For every behavior change the spec specifies, verify it's
   implemented and tested.
2. **Code quality pass.** Read your full diff
   (`git --no-pager diff <base>...HEAD`). Watch for: hardcoded paths,
   `os.UserHomeDir` baked into tests, inline `exec.Command` calls that should
   use `safecmd`, swallowed errors, missing `slog` calls on recoverable
   failures.

Fix what you find before sending the ping.

### Project-specific rules (already loaded)

Project-local rules under `bd memories implementer-rule-` were loaded at session
start by your preamble. If a project-local rule conflicts with a universal rule
above, the project-local rule wins; surface the conflict in your reply so the
user can decide whether to graduate or remove the override.

If you accumulate a new rule mid-session (the user corrects you), capture it via
`bd remember --key implementer-rule-<slug> "<rule + Why + How to apply>"`.
