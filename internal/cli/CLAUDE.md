# internal/cli — CLI Helpers and JSON Output Contract

This package contains the helper functions backing `cmd/thrum`'s commands. Two
pieces are load-bearing for the CLI's `--json` output contract.

## --json Output Contract

When a thrum command runs with `--json`, **stdout MUST be a single valid JSON
document.** Any text on stderr is allowed (humans grep it; agent harnesses that
merge stdout+stderr via `tmux capture-pane` would otherwise choke on
`JSON.parse`).

The contract is enforced by two pieces installed before any command code runs:

1. **`installSlogBridge` in `cmd/thrum/main.go`** is invoked from
   `rootCmd.PersistentPreRunE` as the first action. In `--json` mode it replaces
   `slog.Default()` with `cli.SlogHintHandler`; in human mode it installs a
   plain `slog.TextHandler` writing to stderr at `LevelWarn`.
2. **`cli.EmitJSON(body)` and `cli.EmitJSONWithHints(body, collected)`** in
   `emit_json.go` are the only places in the codebase allowed to write
   pretty-printed JSON to stdout. Both drain the `pushedHints` buffer on the way
   out and graft accumulated slog records under a top-level `"hints"` key.

Library code that calls `slog.Warn(...)` mid-command therefore Just Works: the
record gets converted to a `Hint` and lands in the JSON body's `hints` array
instead of corrupting stdout.

### Adding a new --json-capable command

- **Always** emit JSON via `cli.EmitJSON(result)` or
  `cli.EmitJSONWithHints(result, collectedHints)`. Never use
  `json.MarshalIndent` + `fmt.Println` directly inside an RPC command path —
  doing so bypasses the bridge and silently discards any slog records that fired
  during the call.
- For user-facing diagnostics in `--json`-capable commands, prefer
  `slog.Warn(...)` over `fmt.Fprintln(os.Stderr, ...)`. The bridge will surface
  the slog message in the response body's `hints`; a bare `Fprintln` to stderr
  will not.
- Operator-only prompts (e.g. interactive confirmations like
  `"Continue? [y/N]"`) are exempt — they're not user-facing diagnostics and they
  only fire when stdin is a TTY.

### Hint code derivation

`SlogHintHandler.Handle` derives the `Hint.Code` from the first
whitespace-delimited token of the message:

- `"worktree.PaneTargetForIdentity refused: ..."` →
  `worktree.panetargetforidentity`
- `"[telegram.msgmap] persistence write failed"` → `telegram.msgmap`
- `"identity_guard_fire"` → `runtime.warn` (no dot in the token, fallback)

Structured slog attrs (`slog.With(...)`) are intentionally **not** propagated to
hints. The hint message is the record's text only.

### Reference files

- `internal/cli/sloghint.go` — `SlogHintHandler`, `deriveHintCode`,
  `DrainPushedHints`, `ResetPushedHintsForTest`.
- `internal/cli/emit_json.go` — `EmitJSON`, `EmitJSONWithHints`.
- `cmd/thrum/main.go` (`installSlogBridge` near the top) — the wiring point in
  `PersistentPreRunE`.
