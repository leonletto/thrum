#!/usr/bin/env bash
# Scenario: message-read-all (migrates full_test_plan.md § 4.1)
#
# Verifies `thrum message read --all`: after sending a fresh marker
# message to impl, calling `message read --all` either marks it
# (✓ Marked N messages) or finds none (No unread messages — claude
# autonomous-handling raced us). Either output is a valid success
# of the contract; the post-condition that DOES hold deterministically
# is that `thrum inbox --unread --json` returns zero messages.
#
# All daemon mutations run via tmux-exec (out-of-pane) with THRUM_NAME
# pinned. Sender is COORD identity, reader is IMPL identity. The
# COORD/IMPL pane processes are not driven here — the assertions
# need deterministic command output unaffected by claude's autonomous
# inbox handling.

SID="28-message-read-all"
MARKER="kafm1-28-mark-${RUNID}"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

# Step 1: drain any pending unread on impl so the marker we send is
# the only thing in flight (or at least, the only thing matching our
# assertion target). Pre-existing inbound from prior scenarios would
# pollute the unread count.
"$TE" exec --cwd "$IMPL_REPO" --clean -- \
  env THRUM_NAME=test_implementer thrum message read --all >/dev/null 2>&1 || true

# Step 2: send a marker-bearing message coord → impl.
send_out="$(mktemp -t kafm1-28-send.XXXXXX).txt"
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum send "Read test (${MARKER})" --to @test_implementer \
  > "$send_out" 2>&1 || true

# Step 3: brief settle for delivery + any autonomous handling.
sleep 3

# Assertion 1: `message read --all` exits 0 AND output matches one of
# the two valid post-states: "Marked N message(s) as read" OR
# "No unread messages."
read_out="$(mktemp -t kafm1-28-read.XXXXXX).txt"
"$TE" exec --cwd "$IMPL_REPO" --clean -- \
  env THRUM_NAME=test_implementer thrum message read --all \
  > "$read_out" 2>&1
read_rc=$?

if [ "$read_rc" -eq 0 ] && grep -qE "(✓ Marked [0-9]+ messages? as read|No unread messages\.)" "$read_out"; then
  emit_pass "$SID" "read-all-output"
else
  got="$(tr '\n' ' ' < "$read_out" | head -c 240)"
  emit_fail "$SID" "read-all-output" \
    "exit 0 + stdout matching '✓ Marked N messages as read' OR 'No unread messages.'" \
    "rc=${read_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: post-condition — impl unread count is 0. Use the
# tmux-exec→file→host-jq pattern: tmux-exec captures pane output at
# 80 cols and prefixes daemon log lines, so the outer shell's `> file`
# redirect would catch garbage. Writing the file from INSIDE the
# inner pane via bash -c gives clean JSON.
unread_file="$(mktemp -t kafm1-28-unread.XXXXXX).json"
"$TE" exec --cwd "$IMPL_REPO" --clean -- \
  bash -c "env THRUM_NAME=test_implementer thrum inbox --unread --json > '${unread_file}' 2>/dev/null" \
  >/dev/null 2>&1 || true

if [ -s "$unread_file" ]; then
  unread_count="$(jq -r '.messages | length' < "$unread_file" 2>/dev/null || echo "?")"
else
  unread_count="?"
fi

if [ "$unread_count" = "0" ]; then
  emit_pass "$SID" "no-unread-after-read-all"
else
  got_excerpt="$(tr '\n' ' ' < "$unread_file" | head -c 240)"
  emit_fail "$SID" "no-unread-after-read-all" \
    "thrum inbox --unread --json shows .messages | length == 0" \
    "got: length=${unread_count}; payload: ${got_excerpt:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

rm -f "$send_out" "$read_out" "$unread_file"
