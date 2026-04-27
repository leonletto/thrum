#!/usr/bin/env bash
# Scenario: context-update-project (migrates full_test_plan.md § 9.1)
#
# Verifies that asking a registered claude pane (in natural language) to
# "update my thrum context with: X" results in the context being saved
# such that `thrum context show --session` echoes X back. This is the
# end-to-end smoke for the /thrum:update-project skill flow: NL prompt →
# claude interpretation → underlying `thrum context save` call → context
# show round-trip.
#
# Deviation from markdown § 9.1: the spec's expected outcome is a
# free-form "Context updated confirmation" line in the pane chat. We
# instead assert the underlying observable contract — that the context
# store actually contains the marker the user asked to save — via a
# `! thrum context show --session` round-trip. This is deterministic and
# grep-able vs. claude's free-form prose, mirrors the deviation
# discipline used in scenario 05 (whoami --json over NL identity prose),
# and is what every downstream consumer of the saved context will read.
#
# Note on slash command: the heading names /thrum:update-project, but
# the markdown body uses an NL prompt — those are different code paths.
# The slash command's skill (claude-plugin/commands/update-project.md)
# is heavyweight (spawns a sub-agent and edits an EXISTING structured
# project_state.md via targeted edits — won't no-op cleanly against the
# fresh fixture which has no prior project_state.md). The NL prompt
# routes through claude's general tool selection and reaches the same
# `thrum context save` end-state without that prerequisite. We test the
# NL path because it matches what § 9.1's actual command does.
#
# Fixture mutation: writes context for test_coordinator_main. Subsequent
# scenarios see this saved context if they `thrum context show`. No
# teardown needed — context is overwritten on every save.

SID="17-context-update-project"
PANE="$COORD_PANE"
REPO="$COORD_REPO"
MARKER="kafm5-17-marker-${RUNID}"

# Settle the coord pane in case prior scenarios left rendering in flight.
# COORD's prime output is large; allow up to 60s (matches scenario 03).
wait_for_pane_idle "$PANE" 60

# Send the NL prompt verbatim from the markdown spec. send_command
# (no `!`-prefix branch) types the text into claude's chat input and
# submits it.
send_command "$PANE" "update my thrum context with: ${MARKER}"

# Give claude time to interpret + invoke `thrum context save`. The
# spec's expected wait is 30s; claude haiku in the fixture is fast
# but the tool-call round trip is bounded, so allow up to 90s before
# we round-trip via show.
sleep 30
wait_for_pane_idle "$PANE" 60

# Now verify the underlying contract: the saved context contains the
# marker. Drive via `!`-bash so the result lands in JSONL where
# assert_jsonl can match it.
if send_bash_and_wait "$PANE" "$REPO" \
    "thrum context show --session" \
    "$MARKER" 60; then
  emit_pass "$SID" "context-marker-saved"
else
  emit_fail "$SID" "context-marker-saved" \
    "thrum context show --session output containing '${MARKER}'" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
