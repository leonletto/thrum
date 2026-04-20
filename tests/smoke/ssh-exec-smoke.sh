#!/usr/bin/env bash
# tests/smoke/ssh-exec-smoke.sh — Manual smoke for scripts/ssh-exec.
#
# Not automated — run manually, requires a reachable SSH host. Use as a sanity
# check after editing scripts/ssh-exec. Exits 0 on all-pass, 1 on any failure.
#
# Usage: HOST=leonsmacmini.local bash tests/smoke/ssh-exec-smoke.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
SSH_EXEC="$SCRIPT_DIR/scripts/ssh-exec"
HOST="${HOST:-leonsmacmini.local}"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

# 1. Basic exec success
out=$("$SSH_EXEC" exec --host "$HOST" --timeout 5 -- echo hello)
[ "$out" = "hello" ] || fail "basic exec: got '$out'"
pass "basic exec"

# 2. Remote exit code pass-through (1)
rc=0
"$SSH_EXEC" exec --host "$HOST" --timeout 5 -- false || rc=$?
[ "$rc" = "1" ] || fail "exit-code pass-through: got $rc"
pass "remote exit pass-through (1)"

# 3. Remote exit code pass-through (42)
rc=0
"$SSH_EXEC" exec --host "$HOST" --timeout 5 -- sh -c "exit 42" || rc=$?
[ "$rc" = "42" ] || fail "exit-code pass-through: got $rc"
pass "remote exit pass-through (42)"

# 4. SSH layer failure → 255
rc=0
"$SSH_EXEC" exec --host "nonexistent-xyz.invalid" --timeout 3 -- uname 2>/dev/null || rc=$?
[ "$rc" = "255" ] || fail "ssh layer failure: got $rc"
pass "ssh layer failure → 255"

# 5. Timeout → 255
rc=0
"$SSH_EXEC" exec --host "$HOST" --timeout 2 -- sleep 10 2>/dev/null || rc=$?
[ "$rc" = "255" ] || fail "timeout: got $rc"
pass "timeout → 255"

# 6. Stdin passthrough
out=$(echo "piped-input" | "$SSH_EXEC" exec --host "$HOST" --timeout 5 --stdin -- cat)
[ "$out" = "piped-input" ] || fail "stdin: got '$out'"
pass "stdin passthrough"

# 7. copy + cleanup
tmpfile=$(mktemp)
echo "copy-test-contents" > "$tmpfile"
"$SSH_EXEC" copy --host "$HOST" "$tmpfile" /tmp/ssh-exec-smoke-copy
remote_contents=$("$SSH_EXEC" exec --host "$HOST" --timeout 5 -- cat /tmp/ssh-exec-smoke-copy)
[ "$remote_contents" = "copy-test-contents" ] || fail "copy: got '$remote_contents'"
"$SSH_EXEC" exec --host "$HOST" --timeout 5 -- rm -f /tmp/ssh-exec-smoke-copy
rm -f "$tmpfile"
pass "copy subcommand"

# 8. logs
"$SSH_EXEC" exec --host "$HOST" --timeout 5 -- sh -c "echo log-line > /tmp/ssh-exec-smoke.log"
log_out=$("$SSH_EXEC" logs --host "$HOST" /tmp/ssh-exec-smoke.log)
[ "$log_out" = "log-line" ] || fail "logs: got '$log_out'"
"$SSH_EXEC" exec --host "$HOST" --timeout 5 -- rm -f /tmp/ssh-exec-smoke.log
pass "logs subcommand"

# 9. env passthrough
out=$("$SSH_EXEC" exec --host "$HOST" --timeout 5 --env "FOO=bar-baz" -- \
  sh -c 'echo "${FOO:-unset}"')
[ "$out" = "bar-baz" ] || fail "env passthrough: got '$out'"
pass "env passthrough"

echo "--- all 9 checks passed ---"
