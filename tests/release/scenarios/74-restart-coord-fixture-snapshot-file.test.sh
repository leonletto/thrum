#!/usr/bin/env bash
# Scenario: restart-coord-fixture-snapshot-file (migrates full_test_plan.md § 10.5)
#
# Asserts the daemon's restart RPC wrote a snapshot whose content
# carries the canonical "# Restart Snapshot — <agent>" header. The
# snapshot file at .thrum/restart/<agent>.md is consumed within
# seconds by the new claude session's inject-prime-context.sh hook
# (consume-on-load default), so by the time this scenario runs
# directly may or may not be on disk.
#
# Verification strategy: the briefing's SessionStart attachment
# embeds the snapshot verbatim under "# Previous Session Context" —
# even after the source file is consumed, the rendered content
# survives in JSONL. Asserting on the attachment content is a
# stronger contract than asserting on the (transient) file: it
# proves the daemon WROTE the snapshot AND the hook RENDERED it
# (i.e. the full handoff worked, not just the write half).
#
# One assertion: post-restart SessionStart attachment contains the
# canonical "# Restart Snapshot — <agent>" header. Same shape as
# scenario 09's `save-header` but against the daemon-side restart-
# RPC path embedded in the post-restart briefing.
#
# Depends on scenario 73 (which triggered the restart).

SID="74-restart-coord-fixture-snapshot-file"

if [ -z "${KAFM6_S1_AGENT:-}" ] || [ -z "${KAFM6_S1_WT:-}" ]; then
  emit_fail "$SID" "fixture-precondition" \
    "scenario 70 fixture identifiers exported" \
    "(missing — scenarios 70 + 73 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Filter: post-restart SessionStart attachment whose stdout/content
# contains the canonical agent-name header. Daemon-side restart-RPC
# embeds the snapshot file's body inside the briefing's # Previous
# Session Context block; the snapshot's first line is itself the
# canonical "# Restart Snapshot — <agent>" header, so the briefing
# attachment carries it.
SNAPSHOT_HEADER="# Restart Snapshot — ${KAFM6_S1_AGENT}"
header_filter='.type == "attachment"
        and (.attachment.hookEvent == "SessionStart")
        and (((.attachment.stdout // "" | tostring) | contains("'"$SNAPSHOT_HEADER"'"))
             or ((.attachment.content // "" | tostring) | contains("'"$SNAPSHOT_HEADER"'")))'

if wait_for_jsonl_match "$KAFM6_S1_WT" "$header_filter" 90 >/dev/null; then
  emit_pass "$SID" "snapshot-header-in-briefing"
else
  emit_fail "$SID" "snapshot-header-in-briefing" \
    "post-restart SessionStart attachment containing \"${SNAPSHOT_HEADER}\"" \
    "(none observed within 90s — daemon may have skipped the snapshot save, or inject-prime-context.sh failed to render it)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
