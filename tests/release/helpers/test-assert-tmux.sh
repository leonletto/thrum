#!/usr/bin/env bash
# tests/release/helpers/test-assert-tmux.sh
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/assert-tmux.sh"

SESSION="behavioral-test-$$"
tmux new-session -d -s "$SESSION" -c /tmp
trap 'tmux kill-session -t "$SESSION" 2>/dev/null || true' EXIT

# session_exists
assert_tmux_session_exists "$SESSION" || { echo "FAIL: session_exists positive"; exit 1; }
! assert_tmux_session_exists "no-such-session-$$" || { echo "FAIL: session_exists negative"; exit 1; }

# pane_running_runtime — start `sleep` in the pane as a stand-in runtime
tmux send-keys -t "$SESSION" 'sleep 30' Enter
sleep 1
assert_tmux_pane_running_runtime "$SESSION" "sleep" || { echo "FAIL: pane_running_runtime positive"; exit 1; }
! assert_tmux_pane_running_runtime "$SESSION" "no-such-binary" || { echo "FAIL: pane_running_runtime negative"; exit 1; }

# pane_contains — write something to the pane
tmux send-keys -t "$SESSION" C-c
tmux send-keys -t "$SESSION" 'echo hello-marker-12345' Enter
sleep 1
assert_tmux_pane_contains "$SESSION" "hello-marker-12345" || { echo "FAIL: pane_contains positive"; exit 1; }
! assert_tmux_pane_contains "$SESSION" "no-such-marker" || { echo "FAIL: pane_contains negative"; exit 1; }

# runtime_version — call against `bash` since it's always present
v=$(runtime_version bash)
[[ -n "$v" && "$v" != "unknown" ]] || { echo "FAIL: runtime_version of bash returned '$v'"; exit 1; }

# runtime_version — non-existent binary returns "unknown"
v=$(runtime_version /no/such/binary 2>/dev/null || true)
[[ "$v" == "unknown" ]] || { echo "FAIL: runtime_version of missing binary returned '$v', expected 'unknown'"; exit 1; }

echo "PASS"
