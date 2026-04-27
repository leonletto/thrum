# Release Test Framework

Bash runner that drives a real coordinator+implementer multi-agent thrum
fixture in tmux panes, asserts against agent JSONL transcripts via
`!`-prefix commands, and emits tailable per-assertion output. Replaces
(over time) the operator-driven markdown plans under
`dev-docs/release-testing/`.

**Design spec:** `dev-docs/specs/2026-04-26-release-test-framework-design.md`
**Implementation plan:** `dev-docs/plans/2026-04-26-release-test-framework-implementation.md`

## Run all scenarios

```bash
./tests/release/run.sh
```

## Run one scenario

```bash
./tests/release/run.sh "01-*.test.sh"
```

The arg is a glob filter against `scenarios/`.

## Output

Each assertion emits one line with status tag + scenario-id /
assertion-name. Failures stream inline with an indented `→` detail block
AND get repeated in the end-of-run `SUMMARY` block.

```text
[PASS] 01-session-start-injection / briefing-header
[FAIL] 01-session-start-injection / old-nudge-absent
       → expected: bash-stdout starting with 'FAILED old_nudge_absent'
       → got:      VERIFIED old_nudge_absent (1 hits in SessionStart:startup)
       → file:     scenarios/01-session-start-injection.test.sh:18
```

`tail -f run.log` shows failures inline. `grep '^\[FAIL\]' run.log` gives
a clean post-run failure list with file:line references.

Exit codes: `0` on all-pass, `1` on assertion failures, `2` on setup
failure (no scenarios attempted).

## Author a new scenario

1. Create `scenarios/NN-name.test.sh` (numeric prefix; explicit ordering).
2. Top-of-file preamble explaining what's verified, why it matters,
   fixture mutations (if any), and whether it's safe to re-run after
   another scenario.
3. Source nothing — `run.sh` already sourced `helpers/all.sh` before
   sourcing your scenario. You have access to: `send_command`,
   `assert_jsonl`, `wait_for_jsonl_match`, `wait_for_session_start`,
   `wait_for_pane_idle`, `wait_for_bash_stdout_contains`,
   `send_bash_and_wait`, `emit_pass` / `emit_fail` / `emit_skip`, plus
   env vars `COORD_PANE`, `IMPL_PANE`, `COORD_REPO`, `IMPL_REPO`,
   `BASE`, `WORKTREE_BASE`, `REPO`, `RUNID`, `THRUM_RELEASE_REPO_ROOT`.
4. Default to driving the IMPL pane for state-mutating tests; coord
   stays stable as the driver surface.
5. **Use distinct `check-context-value.sh <tag>` arguments per
   invocation, not just per scenario.** `assert_jsonl` reads the JSONL
   from the beginning and matches the first hit, so a repeated tag from
   earlier in the same session would match a stale entry. If a scenario
   needs to assert a needle twice, name them `foo_pre` / `foo_post`,
   not the same `foo` both times.
6. **Driver-side `thrum` calls must wrap through `tmux-exec`.** The
   plain `thrum` CLI walks the calling process's PID tree to derive
   identity; from inside the runner's bash tool the walk hits the
   parent runtime and adopts the wrong agent / fires cross-worktree
   guards. Wrap with `"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec
   --cwd "$REPO" --clean -- thrum <args>` so the call runs in an
   ephemeral pane whose ancestry chain ends at the tmux server.

## Debugging a failed scenario

The fixture lives at `$HOME/.thrum_release_tests/$RUNID/`. By default
`run.sh` removes it via `run_teardown` on EXIT. To inspect after a
failure:

```bash
THRUM_RELEASE_NO_TEARDOWN=1 ./tests/release/run.sh
# After failure, inspect:
tmux attach -t coord
tmux attach -t impl
ls "$HOME/.thrum_release_tests/" | tail -1   # latest RUNID
ls "$HOME/.thrum_release_test_worktrees/" | tail -1
# Manual cleanup when done:
tmux kill-session -t coord; tmux kill-session -t impl
ps -eo pid,command | grep "thrum.*daemon.*thrum_release" \
  | grep -v grep | awk '{print $1}' | xargs -r kill
rm -rf "$HOME/.thrum_release_tests"/* "$HOME/.thrum_release_test_worktrees"/*
```

The runner's preflight cleanup at the start of `run_setup` will also
nuke leftover `coord`/`impl` tmux sessions and `thrum_release_tests`
daemons before the next run, so a forgotten `NO_TEARDOWN` won't block
subsequent runs.

## Tools required

`thrum`, `tmux`, `jq`, `git`, `claude` — all in `PATH`. Plus the
executable `scripts/check-context-value.sh` and `scripts/tmux-exec` in
this repo. The pre-release thrum claude-plugin must be installed (see
`dev-docs/prompts/release-test-framework-continuation.md` § 13 for the
plugin install flow).

## NOT part of `make ci`

Release tests run on demand via `make release-tests` (or directly via
`tests/release/run.sh`). They spawn real claude processes; wall time
and API cost both make CI integration impractical. See spec § 8.3.
