## CLI Hints

### Overview

When you run a Thrum command, you don't always know what's about to happen — or
what you should do next. This is true for humans and for agents running Thrum
commands inside scripts.

CLI hints fill that gap. There are two kinds:

- **Pre-action hints** fire before the command runs. A `warn` hint flags a risky
  condition — maybe the session name already exists, or the directory you're
  pointing at isn't a git worktree. If the condition is bad enough, the command
  aborts before touching anything.
- **Post-action hints** fire after success. An `info` hint suggests the natural
  next step — usually the exact command you'd want to run.

By default, hints appear as a text trailer on stderr (Shape B). If you're
running with `--json`, they move into the JSON body (Shape C). Either way, the
success output on stdout stays clean so pipes work.

Phase B (v0.9.0) wires hints to three commands: `thrum send`,
`thrum tmux create`, and `thrum init`. Those commands have 8 stable hint codes.
All other commands still use the legacy `LegacyHint()` flat-map (random tips,
not codes), until they're retrofitted in a later phase.

---

### Suppression

Three ways to silence hints:

- `--quiet` — suppresses hints **and** the human-readable success line. Use when
  you want silent success/failure with no extra output.
- `--json` — doesn't suppress hints. It moves them into the `hints` array in the
  JSON output instead. Tooling reads them there.
- `THRUM_NO_HINTS=1` — suppresses hints entirely in both text and JSON mode.
  Truthy: any non-empty value except `"0"` or `"false"` (case-insensitive).

```bash
# Suppress hints for this one call
THRUM_NO_HINTS=1 thrum tmux create --session impl --runtime claude --cwd /repos/impl

# Or export it for the whole shell session
export THRUM_NO_HINTS=1
```

`--quiet` and `--json` are per-call flags. `THRUM_NO_HINTS` is an environment
variable — useful in CI, scripts, or agent sessions where you never want hint
noise.

---

### Shape B: text trailer (default)

When you run a command without `--json`, hints render as a stderr trailer after
the success line. The success line itself goes to stdout.

```text
$ thrum tmux create --session impl_demo --runtime claude --cwd /tmp/foo

✓ session created: impl_demo

  warn [tmux.create.not-a-worktree]: --cwd '/tmp/foo' is not a git worktree
    fix:  git worktree add /tmp/foo <branch>
    or:   thrum worktree create <name>    (creates + wires a fresh worktree)

  info [tmux.create.next-launch]: session created — agent is NOT running yet
    start:  thrum tmux launch impl_demo
    prime:  thrum tmux send impl_demo '/thrum:prime'    (after launch, for non-claude runtimes)
```

A few things to notice:

- The success line (`✓ session created: impl_demo`) goes to stdout. The hints go
  to stderr. Piping the output to `jq` or another tool won't get contaminated.
- `warn` hints appear before `info` hints, regardless of the order they fired.
- Each hint can have option rows below it — suggested commands with short
  labels. Label widths align across rows within a hint.
- A leading blank line separates hints from the command's stdout.

When a `warn` hint blocks execution, the command exits non-zero and only the
blocking hints appear. The success line never prints because nothing succeeded.

---

### Shape C: JSON output

Pass `--json` to get machine-readable output. Hints move into a `hints` array
inside the JSON body.

```bash
thrum tmux create --session impl_demo --runtime claude --cwd /tmp/foo --json
```

```json
{
  "status": "ok",
  "session": "impl_demo",
  "runtime": "claude",
  "hints": [
    {
      "code": "tmux.create.not-a-worktree",
      "severity": "warn",
      "message": "--cwd '/tmp/foo' is not a git worktree",
      "options": [
        { "label": "fix", "cmd": "git worktree add /tmp/foo <branch>" },
        {
          "label": "or",
          "cmd": "thrum worktree create <name>",
          "note": "creates + wires a fresh worktree"
        }
      ]
    },
    {
      "code": "tmux.create.next-launch",
      "severity": "info",
      "message": "session created — agent is NOT running yet",
      "options": [
        { "label": "start", "cmd": "thrum tmux launch impl_demo" },
        {
          "label": "prime",
          "cmd": "thrum tmux send impl_demo '/thrum:prime'",
          "note": "after launch, for non-claude runtimes"
        }
      ]
    }
  ]
}
```

The `hints` key is omitted entirely when there are no hints, so you don't need
to handle `"hints": null`. If `THRUM_NO_HINTS` is set, the key is also omitted
even in JSON mode.

Each hint object has:

| Field      | Type   | Notes                                  |
| ---------- | ------ | -------------------------------------- |
| `code`     | string | Stable dotted slug (see catalog below) |
| `severity` | string | `"warn"` or `"info"`                   |
| `message`  | string | Human-readable description             |
| `options`  | array  | Zero or more suggested next steps      |

Each option object has `label` (short identifier), `cmd` (exact command string),
and optionally `note` (parenthetical caveat).

---

### Hint code catalog (Phase B — pilot commands)

All 8 codes are stable. They won't be renamed once published; new codes get
added to the catalog in later phases.

| Code                                | Severity | Trigger                                              | AllowForce | Commands      |
| ----------------------------------- | -------- | ---------------------------------------------------- | ---------- | ------------- |
| `tmux.create.not-a-worktree`        | warn     | `--cwd` path is not a git worktree                   | false      | `tmux create` |
| `tmux.create.session-exists`        | warn     | tmux session name already running                    | true       | `tmux create` |
| `tmux.create.identity-exists-alive` | warn     | worktree has a live registered agent                 | false      | `tmux create` |
| `tmux.create.identity-exists-stale` | warn     | worktree has a stale identity file (no live session) | true       | `tmux create` |
| `tmux.create.next-launch`           | info     | post-success: session created, agent not running yet | n/a        | `tmux create` |
| `tmux.create.identity-replaced`     | info     | post-success: `--force` replaced a stale identity    | n/a        | `tmux create` |
| `send.recipient-stale`              | info     | recipient's last activity exceeds 30 minutes         | n/a        | `send`        |
| `init.next-quickstart`              | info     | post-success: no agent identity registered yet       | n/a        | `init`        |

Severity column uses `warn` and `info` as they appear in the actual output.
`n/a` in AllowForce means the hint is informational and never blocks execution.

---

### AllowForce semantics

`AllowForce` only matters for `warn` hints. It controls whether `--force` can
override the block.

**`AllowForce=true` (recoverable):** The condition is risky but the operator can
knowingly proceed. Without `--force`, the command aborts and prints the hint.
With `--force`, the hint is logged but execution continues.

Examples: a session name that already exists (`tmux.create.session-exists`), or
a stale identity file in the worktree (`tmux.create.identity-exists-stale`).
Both cases have a clear recovery path where overwriting the existing state is
the right call.

**`AllowForce=false` (hard refusal):** Even `--force` won't help. The operation
can't be safely completed because proceeding would cause worse problems than
aborting.

Examples: `--cwd` isn't a git worktree (`tmux.create.not-a-worktree`) — there's
nothing useful to do with a non-worktree path. A live agent is registered in the
target worktree (`tmux.create.identity-exists-alive`) — overwriting a live
session's identity file would orphan a running agent. In both cases, the right
answer is to fix the input, not to force through.

**`info` hints:** Never block, regardless of `AllowForce`. They're post-action
observations. The field isn't applicable to them.

---

### Pre-action blocking

Here's the execution sequence for a command with wired hint sources:

1. Collect pre-action hints (`Post=false`).
2. Call `HandlePreAction`. If any `warn` hint blocks (either `AllowForce=false`,
   or `AllowForce=true` and `--force` wasn't passed), execution stops. The
   blocking hints render to stderr and the CLI exits non-zero. The underlying
   RPC is never called.
3. If no hints block, the command runs.
4. On success, collect post-action hints (`Post=true`) and emit them.

When a hard-refusal hint fires (`AllowForce=false`), the abort message is:

```text
aborted — see hint above for next steps
```

When a recoverable hint fires without `--force`:

```text
aborted (pass --force to override recoverable conditions)
```

The distinction matters if you're parsing exit messages in a script. Both exit
non-zero.

---

### Phase roadmap

**Phase B (shipped in v0.9.0):** `thrum send`, `thrum tmux create`,
`thrum init`. Eight hint codes. Stable dotted slugs you can match in scripts and
tooling.

**Phases C–E (planned, tracked under `thrum-rqkf`):**

- Phase C: retrofit the hint pipeline to remaining commands (currently on the
  legacy `LegacyHint()` flat-map).
- Phase D: daemon-side anti-pattern detection — hints that fire based on runtime
  state the daemon observes, not just command inputs.
- Phase E: docs and `llms.txt` updates to cover the expanded catalog.

The hint code namespace is append-only. Codes published in Phase B won't change.

---

### See Also

- [Configuration](configuration.md) — `THRUM_NO_HINTS` env var and full
  environment variable reference
- [CLI Reference](cli.md) — full command reference for `thrum send`,
  `thrum tmux create`, and `thrum init`
- [Tmux Sessions](tmux-sessions.md) — worktree-anchored session workflow that
  the `tmux.create.*` hints assume
