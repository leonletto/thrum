#!/usr/bin/env bash
# Behavioral test: planning-skill review-loop gates (thrum-wjqw.5 / FE.5).
#
# Standalone (NOT sourced by run.sh — it does not need the multi-agent fixture).
# Run directly:  bash tests/release/skill-review-loop-gates.sh
#
# Scope: the review-loop gates are SKILL.md prose followed by an LLM, so a full
# runtime-observed scenario would require driving a Claude session through
# project-setup (heavy + non-deterministic). This test pins the DETERMINISTIC
# core instead:
#   (A) Functional — the exact grep -F gate the skills prescribe correctly
#       discriminates a stamped / unstamped / OVERRIDE plan doc.
#   (B) Contract  — the shipped skill files carry the correct, non-rotted gate
#       contract (case-sensitive grep -F, M2-canonical stamp form, the
#       verify-against-source prose-only pre-flight, the stamp-satisfies
#       boundary). A regression that loosens grep -F to grep -i, reverts to the
#       bare stamp form, or re-introduces a diff/File-Structure bail is caught.
#
# A full end-to-end fixture scenario (drive Claude through project-setup and
# assert the actual bail in the JSONL transcript) is a heavier follow-up.

set -uo pipefail

REPO="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)"
SKILLS="$REPO/claude-plugin/skills"
PASS=0
FAIL=0

ok()   { echo "  PASS: $1"; PASS=$((PASS+1)); }
bad()  { echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

# The canonical, case-sensitive gate string project-setup Phase 0 prescribes.
READY='THRUM-REVIEW: stage=plan verdict=Ready:Yes'
OVERRIDE='THRUM-REVIEW: stage=plan verdict=OVERRIDE'

echo "== (A) Functional: Phase 0 grep -F gate discriminates plan docs =="
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

printf '# plan\n\nbody\n' > "$TMP/no-stamp.md"
printf '# plan\n\nbody\n\n<!-- %s cycle=2 date=2026-05-21 -->\n' "$READY" > "$TMP/ready.md"
printf '# plan\n\nbody\n\n<!-- %s cycle=3 date=2026-05-21 by=coordinator_main reason="x" -->\n' "$OVERRIDE" > "$TMP/override.md"
printf '# plan\n\nbody\n\n<!-- thrum-review: stage=plan verdict=ready:yes -->\n' > "$TMP/miscased.md"

# unstamped → BOTH greps miss (gate bails)
if ! grep -qF "$READY" "$TMP/no-stamp.md" && ! grep -qF "$OVERRIDE" "$TMP/no-stamp.md"; then
  ok "unstamped plan fails the gate (bails)"; else bad "unstamped plan should fail the gate"; fi

# ready → Ready:Yes grep matches (gate passes)
if grep -qF "$READY" "$TMP/ready.md"; then
  ok "Ready:Yes plan passes the gate"; else bad "Ready:Yes plan should pass"; fi

# override → OVERRIDE grep matches (gate passes)
if grep -qF "$OVERRIDE" "$TMP/override.md"; then
  ok "OVERRIDE plan passes the gate"; else bad "OVERRIDE plan should pass"; fi

# mis-cased stamp → case-sensitive grep -F misses (gate bails — proves -F not -i)
if ! grep -qF "$READY" "$TMP/miscased.md"; then
  ok "mis-cased stamp (ready:yes) fails the case-sensitive gate"; else bad "mis-cased stamp should NOT pass a case-sensitive gate"; fi

echo "== (B) Contract: shipped skill files carry the correct gate contract =="
PS="$SKILLS/project-setup/SKILL.md"
VAS="$SKILLS/verify-against-source/SKILL.md"
CRBC="$SKILLS/coordinator-running-brainstorm-cycles/SKILL.md"
CDW="$SKILLS/coordinator-dispatching-work/SKILL.md"

# project-setup Phase 0 exists + uses grep -F + accepts Ready:Yes OR OVERRIDE
grep -qF '## Phase 0 — Review gate' "$PS" && ok "project-setup has Phase 0 gate" || bad "project-setup missing Phase 0 gate"
grep -qF 'grep -F' "$PS" && ok "project-setup Phase 0 uses grep -F" || bad "project-setup Phase 0 must use grep -F"
grep -qF "$READY" "$PS" && grep -qF "$OVERRIDE" "$PS" && ok "project-setup accepts Ready:Yes OR OVERRIDE" || bad "project-setup must accept Ready:Yes AND OVERRIDE"
# The skill must DIRECT case-sensitive matching (it mentions "grep -i" only to forbid it).
grep -qiE 'case-sensitive' "$PS" && grep -qiE 'never .*grep -i|not .*grep -i|never `?grep -i' "$PS" \
  && ok "project-setup directs case-sensitive matching (forbids grep -i)" \
  || bad "project-setup must direct case-sensitive matching and forbid grep -i"

# verify-against-source: prose-only pre-flight (must NOT inherit verify-against-plan bails)
grep -qiE 'no.+non-empty-diff|no.+diff check' "$VAS" && ok "verify-against-source disclaims a diff bail" || bad "verify-against-source must state NO diff requirement"
grep -qiE 'File-Structure' "$VAS" && grep -qiE 'no.+File-Structure|without.+File-Structure|neither' "$VAS" && ok "verify-against-source disclaims a File-Structure bail" || bad "verify-against-source must state NO File-Structure requirement"

# wrapper: uses verify-against-source at the prose gates (not verify-against-plan as the gate reviewer)
grep -qF 'verify-against-source' "$CRBC" && ok "wrapper uses verify-against-source" || bad "wrapper must use verify-against-source at prose gates"

# dispatch skill: stamp-satisfies-pre-dispatch boundary present
grep -qF 'THRUM-REVIEW: stage=prompt verdict=Ready:Yes' "$CDW" && ok "dispatch skill has the stamp-satisfies boundary" || bad "dispatch skill missing the prompt-stamp boundary"

echo
echo "== SUMMARY: $PASS passed, $FAIL failed =="
[ "$FAIL" -eq 0 ]
