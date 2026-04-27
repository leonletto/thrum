#!/usr/bin/env bash
# Scenario: context-show-saved (migrates full_test_plan.md § 9.2)
#
# Verifies the `thrum context save` → `thrum context show` round trip
# at the CLI layer, independent of any claude session: a content blob
# saved via `thrum context save --file` is readable back via
# `thrum context show --session`. The save/show pair is the
# storage-layer contract every higher-level path (the
# /thrum:update-project skill, the PreCompact hook, /thrum:load-context
# via thrum prime) eventually reads through.
#
# Driven entirely out-of-pane via tmux-exec — same pattern the
# markdown spec uses (`cd ~/.workspaces/.../test-coordinator;
# THRUM_NAME=test_coordinator thrum context show`). tmux-exec breaks
# the PID ancestry chain so the call resolves to the agent we pass via
# THRUM_NAME, not the runner's parent.
#
# Independent of scenario 17 by design: 17 covers the slash-command
# /thrum:update-project path; this scenario covers the CLI save+show
# round-trip without any claude involvement. Coverage of those two
# paths is intentionally separate so a regression in either is
# attributable.
#
# Read-only at the fixture level (writes to test_coordinator_main's
# context store, which is not consumed by any subsequent scenario).

SID="18-context-show-saved"
MARKER="kafm5-18-marker-${RUNID}"

# Save the marker via tmux-exec + --file. tmux-exec runs commands
# inside an ephemeral tmux pane (not a child process), so shell-pipe
# stdin doesn't reach the inner command — use the --file flag, which
# `thrum context save` accepts as an explicit content source.
marker_file="$(mktemp -t kafm5-18.XXXXXX)"
printf '%s\n' "$MARKER" > "$marker_file"
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum context save --file "$marker_file" \
  >/dev/null 2>&1 || true
rm -f "$marker_file"

# Read the marker back via the same out-of-pane pattern.
output_file="$(mktemp -t kafm5-18-show.XXXXXX)"
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum context show --session \
  > "$output_file" 2>&1 || true

if grep -q "$MARKER" "$output_file"; then
  emit_pass "$SID" "show-includes-marker"
else
  got="$(tr '\n' ' ' < "$output_file" | head -c 240)"
  emit_fail "$SID" "show-includes-marker" \
    "thrum context show --session output containing '${MARKER}'" \
    "${got:-<empty output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$output_file"
