#!/usr/bin/env bash
# Scenario: context-update-project (migrates full_test_plan.md § 9.1)
#
# Verifies the /thrum:update-project slash command end-to-end: a
# pre-seeded `.thrum/context/project_state.md` gets mutated when the
# COORD pane invokes the slash command. The skill body
# (claude-plugin/commands/update-project.md) instructs claude to
# delegate to a sub-agent that runs a mechanical-state gather + reads
# the file + makes targeted Edit calls. We assert only that the file
# was actually mutated — not that the edits are semantically correct
# — because the skill's structured-edit logic depends on a richer
# project_state.md than a fresh fixture has, and asserting on edit
# correctness would test the sub-agent's prose judgment, not the
# slash-routing contract.
#
# Why mtime + size-change instead of content-substring matching: the
# skill's edits are non-deterministic (sub-agent prose decisions); the
# only deterministic invariant is "file was touched." mtime advance +
# byte-size delta together rule out a no-op invocation (claude
# acknowledged the slash command but didn't actually edit anything).
#
# Why this is the spec contract for § 9.1: § 9.1's heading is "Test
# /thrum:update-project" — the slash command is the named subject.
# The spec body uses NL "update my thrum context with: X" instead of
# the slash command, which is a documentation drift in the spec
# (NL → `thrum context save` is a distinct code path that scenario
# 18's CLI assertion already covers). Asserting on the slash command
# closes the coverage gap dual review flagged.
#
# Driven against COORD pane (matches markdown subject pane). Fixture
# mutation: writes (and leaves) `.thrum/context/project_state.md`
# under $COORD_REPO; subsequent scenarios that depend on a virgin
# repo state should run BEFORE 17 (current sort puts it after the
# worktree/restart scenarios that care).

SID="17-context-update-project"
PANE="$COORD_PANE"
REPO="$COORD_REPO"
PS_FILE="$REPO/.thrum/context/project_state.md"
MARKER="kafm5-17-preseed-${RUNID}"

# Step 1: pre-seed a minimal but plausibly-structured project_state.md
# so the skill's sub-agent has something to read + edit. Mirrors the
# section shapes the skill's targeted-edit instructions reference
# (Last Updated / Phase header, Architecture Health table, Recent
# Sessions block) so the sub-agent doesn't bail on a missing-anchor
# error mid-edit.
mkdir -p "$REPO/.thrum/context"
cat > "$PS_FILE" <<EOF
# Project State — KAFM5 Test Fixture

**Last Updated:** 2026-04-01 **Phase:** Pre-seed for kafm.5 scenario 17 (${MARKER}).

---

## Current State Summary

Fresh fixture seeded for slash-command coverage of /thrum:update-project.

### Architecture Health

| Component | Status | Session / Date | Details |
|-----------|--------|----------------|---------|
| seed | OK | S0 · 2026-04-01 | Pre-seed row; expect skill to add new rows above this. |

### Recent Sessions

#### Session 0 (2026-04-01) — Fixture seed

Fresh fixture; no real sessions yet.

### Session Blocks (consolidated)

(none)

---

## Open Epics / Active Work

(none)
EOF

# Capture pre-state for the mutation assertion.
size_before=$(wc -c < "$PS_FILE")
mtime_before=$(stat -f %m "$PS_FILE" 2>/dev/null || stat -c %Y "$PS_FILE")

# Settle the coord pane in case prior scenarios left rendering active.
wait_for_pane_idle "$PANE" 60

# Send the slash command. send_slash_command handles the discrete
# `/` keystroke split needed for reliable slash-mode engagement.
send_slash_command "$PANE" "/thrum:update-project"

# Poll for mutation. The skill's sub-agent does Read + multiple Edits;
# under haiku in the fixture that round-trip can be 60-180s. Poll
# every 3s up to 240s for either an mtime advance or a size delta.
elapsed=0
mutated=false
while [ "$elapsed" -lt 240 ]; do
  size_now=$(wc -c < "$PS_FILE" 2>/dev/null || echo "$size_before")
  mtime_now=$(stat -f %m "$PS_FILE" 2>/dev/null || stat -c %Y "$PS_FILE" 2>/dev/null || echo "$mtime_before")
  if [ "$mtime_now" -gt "$mtime_before" ] || [ "$size_now" != "$size_before" ]; then
    mutated=true
    break
  fi
  sleep 3
  elapsed=$((elapsed + 3))
done

if $mutated; then
  emit_pass "$SID" "project-state-mutated"
else
  emit_fail "$SID" "project-state-mutated" \
    "${PS_FILE} mutated (mtime advance OR size change) within 240s of /thrum:update-project" \
    "(no mutation observed; size_before=${size_before} mtime_before=${mtime_before})" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
