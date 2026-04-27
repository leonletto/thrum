#!/usr/bin/env bash
# Scenario: multi-runtime-preferred-runtime-quickstart (migrates full_test_plan.md § 10C.2)
#
# Verifies that `thrum quickstart --runtime <name>` writes the
# supplied runtime to the agent identity's `preferred_runtime` field.
# This is the on-disk source for the second tier of the runtime
# resolution chain (see scenario 64) and the durable artifact behind
# scenarios 67's "quickstart-then-launch" all-in-one pattern.
#
# Sub-fixture (au7k discipline, dispatch rule #3): run-level coord's
# identity must NOT be mutated, since it would silently change the
# runtime resolution behavior in subsequent shared-fixture scenarios.
# Build a fresh repo + thrum init + quickstart in $BASE/kafm8-63/,
# then read its identity file directly. Sub-daemon stopped at
# scenario end.
#
# Single assertion:
#   1. preferred-runtime-opencode — the sub-fixture identity's
#      `preferred_runtime` field equals "opencode" (the value passed
#      to `quickstart --runtime`).
#
# Why "opencode" specifically: the markdown spec § 10C.2 uses
# opencode as the test value because it's a known non-default
# runtime preset. We don't actually launch opencode (that's the
# OPTIONAL § 10C.6 which is deferred as P3) — only assert that
# quickstart wrote the supplied flag value to the identity.

SID="63-multi-runtime-preferred-runtime-quickstart"
SUB_REPO="$BASE/kafm8-63"
SUB_AGENT="kafm8_63_coordinator"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_63() {

# Build the sub-fixture: git repo + thrum init + quickstart.
mkdir -p "$SUB_REPO"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-63@thrum.local" \
    && git config user.name "Release Tests 63" \
    && echo "# 63 sub-fixture" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum init --runtime claude >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-thrum-init" "thrum init in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# THRUM_NAME pin matches the markdown spec's invocation shape and
# keeps audit trails consistent with other out-of-pane thrum calls.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum quickstart \
    --name "$SUB_AGENT" \
    --role coordinator \
    --module testing \
    --runtime opencode \
    --intent "Testing preferred_runtime write" \
    --force >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-quickstart" "thrum quickstart --runtime opencode in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Read the identity file directly. Path shape mirrors the markdown
# spec's `$COORD_DIR/.thrum/identities/test_coordinator.json` lookup.
local identity_file="$SUB_REPO/.thrum/identities/${SUB_AGENT}.json"
if [ ! -f "$identity_file" ]; then
  emit_fail "$SID" "preferred-runtime-opencode" \
    "identity file at $identity_file" \
    "(file missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

local pref
pref=$(jq -r '.preferred_runtime // ""' "$identity_file" 2>/dev/null)
if [ "$pref" = "opencode" ]; then
  emit_pass "$SID" "preferred-runtime-opencode"
else
  local got
  got=$(tr '\n' ' ' < "$identity_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "preferred-runtime-opencode" \
    ".preferred_runtime == 'opencode' in $identity_file" \
    "got: '${pref}'; identity body: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_63

_run_scenario_63

# au7k cleanup: stop sub-daemon. thrum init in $SUB_REPO auto-started
# its own daemon; without explicit stop the sub-daemon would orphan
# past run-level teardown.
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum daemon stop >/dev/null 2>&1 || true
