#!/usr/bin/env bash
# Scenario: cross-session-identity (migrates full_test_plan.md § 8.1)
#
# Verifies that each tmux pane resolves to its OWN registered thrum identity
# — NOT the other pane's. The fixture registers two distinct agents
# (@test_coordinator_main / coordinator and @test_implementer / implementer)
# in two different cwds. A regression here means peercred attribution drifted
# and one pane's identity leaked into the other.
#
# This re-asserts what setup-repo.sh's whoami probes already verified during
# fixture construction; the duplication is intentional. The setup probes
# guard "fixture is correctly initialized" — these assertions guard the
# scenario-level invariant "by the time scenario 05 runs (after preceding
# scenarios), each pane STILL resolves to its own identity, role, and
# repo." Restart-bearing scenarios 02/03 above mutate session state and
# could in principle disturb identity routing.
#
# Deviation from markdown § 8.1: the original spec prompted claude in
# natural language ("what is my thrum identity") and inspected the
# pane's chat output. We assert against `thrum whoami --json` directly
# because (i) deterministic and grep-able vs claude's free-form prose,
# (ii) `thrum whoami` IS the actual identity contract — what the daemon
# resolves for each pane is what every other CLI command will use,
# (iii) claude's NL identity answer comes from the SessionStart
# briefing-injection path, which is already covered by scenario 01.
# Asserting on the same path here would only verify the briefing twice.
#
# Read-only: no fixture mutation.

SID="05-cross-session-identity"

# Assertion 1: coord pane resolves to @test_coordinator_main
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum whoami --json" \
    '"agent_id": "test_coordinator_main"' 60; then
  emit_pass "$SID" "coord-name"
else
  emit_fail "$SID" "coord-name" \
    'whoami --json output containing "agent_id": "test_coordinator_main"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: coord pane resolves to role coordinator
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum whoami --json" \
    '"role": "coordinator"' 60; then
  emit_pass "$SID" "coord-role"
else
  emit_fail "$SID" "coord-role" \
    'whoami --json output containing "role": "coordinator"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 3: impl pane resolves to @test_implementer (NOT coord)
if send_bash_and_wait "$IMPL_PANE" "$IMPL_REPO" \
    "thrum whoami --json" \
    '"agent_id": "test_implementer"' 60; then
  emit_pass "$SID" "impl-name"
else
  emit_fail "$SID" "impl-name" \
    'whoami --json output containing "agent_id": "test_implementer"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 4: impl pane resolves to role implementer (NOT coordinator)
if send_bash_and_wait "$IMPL_PANE" "$IMPL_REPO" \
    "thrum whoami --json" \
    '"role": "implementer"' 60; then
  emit_pass "$SID" "impl-role"
else
  emit_fail "$SID" "impl-role" \
    'whoami --json output containing "role": "implementer"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
