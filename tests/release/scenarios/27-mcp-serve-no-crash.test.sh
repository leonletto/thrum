#!/usr/bin/env bash
# Scenario: mcp-serve-no-crash (migrates full_test_plan.md § 5.6)
#
# Verifies that `thrum mcp serve` starts cleanly without panic when
# bounded by a short timeout. The MCP server is a long-running stdio
# process by design — there's no natural exit code to assert against
# in a release-test context, so we run it under a 5-second tmux-exec
# timeout and assert the absence of crash markers in its captured
# output (no Go panic backtrace, no fatal-error line).
#
# Why timeout-then-no-panic vs. some "ready" probe: thrum mcp serve
# speaks the MCP stdio protocol on file descriptors 0/1; without a
# matching MCP client on the other end, there's no useful "ready"
# line to grep for. The test the spec § 5.6 actually defines is the
# narrow no-crash invariant — a regression that panics during init
# would surface here, while behavioral correctness of the stdio
# protocol is out of scope (covered by Step 5.1-5.5 via the CLI
# parity assertions, since MCP tools translate to the same paths).
#
# Driven via tmux-exec (one-shot ephemeral pane) with --timeout 5
# and --clean so no THRUM_* env leaks. THRUM_NAME pinned per
# spec § 5.6 ("Needs THRUM_NAME for identity resolution").
#
# Two assertions:
#   1. no-panic — captured combined stdout+stderr does NOT contain
#      "panic:" (Go's runtime panic header).
#   2. no-fatal — captured output does NOT contain "fatal error:"
#      (Go's runtime fatal-error header). Distinct from "panic:"
#      because some crashes go through the runtime fatal path
#      (e.g. concurrent map writes) without producing a "panic:"
#      header.
#
# Read-only at the fixture level — the serve invocation is a
# fresh process that exits at timeout.

SID="27-mcp-serve-no-crash"

out_file="$(mktemp -t kafm2-27.XXXXXX)"

# tmux-exec exec runs in an ephemeral pane bounded by --timeout. The
# inner shell may exit non-zero from the timeout — that's expected
# and not an error condition. We capture combined stdout+stderr to
# assert against the no-crash invariant.
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$COORD_REPO" --clean --timeout 5 -- \
  env THRUM_NAME=test_coordinator_main thrum mcp serve \
  > "$out_file" 2>&1 || true

# Assertion 1: no Go runtime panic header.
if grep -q "panic:" "$out_file"; then
  got_excerpt="$(grep -m1 -A2 "panic:" "$out_file" | tr '\n' ' ' | head -c 240)"
  emit_fail "$SID" "no-panic" \
    "captured output without 'panic:' header" \
    "${got_excerpt}" \
    "scenarios/${SID}.test.sh:$LINENO"
else
  emit_pass "$SID" "no-panic"
fi

# Assertion 2: no Go runtime fatal-error header.
if grep -q "fatal error:" "$out_file"; then
  got_excerpt="$(grep -m1 -A2 "fatal error:" "$out_file" | tr '\n' ' ' | head -c 240)"
  emit_fail "$SID" "no-fatal-error" \
    "captured output without 'fatal error:' header" \
    "${got_excerpt}" \
    "scenarios/${SID}.test.sh:$LINENO"
else
  emit_pass "$SID" "no-fatal-error"
fi

rm -f "$out_file"
