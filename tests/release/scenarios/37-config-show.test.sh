#!/usr/bin/env bash
# Scenario: config-show (migrates full_test_plan.md § 4.11)
#
# Verifies `thrum config show` (human-readable) and
# `thrum config show --json` (machine-readable) emit the canonical
# Runtime/Daemon section structure with valid content. Read-only
# against the run-level fixture's daemon — no mutation.
#
# Three assertions:
#   1. human form: exit 0 + contains "Thrum Configuration", "Runtime",
#      "Daemon" section headers AND a "Primary:" runtime line
#   2. JSON form: exit 0 + valid JSON parsing
#   3. JSON form: contains a runtime.primary key with a non-empty value

SID="37-config-show"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

# A1: human form.
human_out="$(mktemp -t kafm1-37-human.XXXXXX).txt"
"$TE" exec --cwd "$COORD_REPO" --clean -- thrum config show \
  > "$human_out" 2>&1
human_rc=$?
if [ "$human_rc" -eq 0 ] \
   && grep -q "Thrum Configuration" "$human_out" \
   && grep -qE "^Runtime$" "$human_out" \
   && grep -qE "^Daemon$" "$human_out" \
   && grep -qE "^  Primary:\s+" "$human_out"; then
  emit_pass "$SID" "human-form-sections"
else
  got="$(tr '\n' ' ' < "$human_out" | head -c 240)"
  emit_fail "$SID" "human-form-sections" \
    "exit 0 + 'Thrum Configuration', 'Runtime', 'Daemon' headers, 'Primary:' line" \
    "rc=${human_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$human_out"

# A2: JSON form parses. Use the file-write-inside-pane trick so daemon
# log noise from tmux pane doesn't pollute the captured stream.
json_out="$(mktemp -t kafm1-37-json.XXXXXX).json"
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  bash -c "thrum config show --json > '${json_out}' 2>/dev/null" \
  >/dev/null 2>&1 || true

if [ -s "$json_out" ] && jq -e . "$json_out" >/dev/null 2>&1; then
  emit_pass "$SID" "json-form-parses"
else
  got="$(tr '\n' ' ' < "$json_out" | head -c 240)"
  emit_fail "$SID" "json-form-parses" \
    "thrum config show --json emits valid JSON" \
    "${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# A3: JSON has runtime.primary key with non-empty value.
if [ -s "$json_out" ]; then
  primary="$(jq -r '.runtime.primary // .Runtime.Primary // ""' "$json_out" 2>/dev/null)"
else
  primary=""
fi
if [ -n "$primary" ] && [ "$primary" != "null" ]; then
  emit_pass "$SID" "json-runtime-primary"
else
  emit_fail "$SID" "json-runtime-primary" \
    "JSON.runtime.primary is non-empty, non-null" \
    "got: '${primary}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$json_out"
