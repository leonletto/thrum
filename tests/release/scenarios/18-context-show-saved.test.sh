#!/usr/bin/env bash
# Scenario: context-show-saved (migrates full_test_plan.md § 9.2)
#
# Verifies that `thrum context show` round-trips a previously saved
# context blob — i.e. that step 9.1's save persisted to a place where
# show can read it back. The spec frames this as a smoke test that
# context storage works at the CLI layer (independent of any claude
# session): driven from outside the agent's tmux pane via tmux-exec
# with THRUM_NAME pinned, the same env shape the markdown uses.
#
# Coupling to scenario 17: 17 saves a marker via the NL "update my
# thrum context with" prompt; 18 verifies that same marker is readable
# via `thrum context show`. Together they exercise the save→show round
# trip end-to-end. Listing 17 before 18 in run.sh's natural sort order
# (17- < 18-) matches the spec's § 9.1 → § 9.2 ordering.
#
# Deviation from markdown § 9.2: the spec runs the show command from
# a fresh shell (`cd ~/.workspaces/.../test-coordinator; THRUM_NAME=...
# thrum context show`). We use tmux-exec to run the equivalent in an
# ephemeral pane that breaks the PID ancestry chain — same reason
# setup-repo.sh wraps driver-side thrum calls through tmux-exec
# (avoids the framework parent-process leaking identity). Otherwise
# faithful to the markdown.
#
# Read-only: no fixture mutation.

SID="18-context-show-saved"
MARKER="kafm5-17-marker-${RUNID}"

# Use tmux-exec for an out-of-pane invocation, mirroring the spec's
# fresh-shell + THRUM_NAME pattern. THRUM_NAME=test_coordinator_main
# ensures the lookup keys on the same agent identity scenario 17 wrote.
output_file="$(mktemp -t kafm5-18.XXXXXX)"
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
