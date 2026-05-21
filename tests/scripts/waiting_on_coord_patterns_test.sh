#!/usr/bin/env bash
# waiting_on_coord_patterns_test.sh — validate the regex pattern library in
# scripts/waiting-on-coord-agent-sweep.sh against the fixture set.
#
# Fixtures with prefix 0N_*.txt MUST flag (positive); fixtures with prefix
# neg_*.txt MUST NOT flag. Per thrum-e1n0 acceptance: at least 3 positive
# fixtures, all matching, no negative fixtures matching.
#
# Usage: bash tests/scripts/waiting_on_coord_patterns_test.sh
# Exit 0 on full pass, 1 on any miss.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
SWEEP="${REPO_ROOT}/scripts/waiting-on-coord-agent-sweep.sh"
FIXTURE_DIR="${SCRIPT_DIR}/fixtures/waiting-on-coord"

if [[ ! -r "$SWEEP" ]]; then
    echo "FAIL: sweep script not found: $SWEEP" >&2
    exit 1
fi
if [[ ! -d "$FIXTURE_DIR" ]]; then
    echo "FAIL: fixture dir not found: $FIXTURE_DIR" >&2
    exit 1
fi

pos_pass=0
pos_fail=0
neg_pass=0
neg_fail=0

# Positive fixtures: must flag (matcher returns 0 + at least one label)
for f in "$FIXTURE_DIR"/[0-9]*.txt; do
    [[ -r "$f" ]] || continue
    name=$(basename "$f")
    if labels=$(bash "$SWEEP" --test-fixture "$f" 2>/dev/null); then
        echo "PASS (positive): $name → $(echo "$labels" | paste -sd ',' -)"
        pos_pass=$((pos_pass + 1))
    else
        echo "FAIL (positive — expected match, got none): $name" >&2
        pos_fail=$((pos_fail + 1))
    fi
done

# Negative fixtures: must NOT flag (matcher returns non-zero)
for f in "$FIXTURE_DIR"/neg_*.txt; do
    [[ -r "$f" ]] || continue
    name=$(basename "$f")
    if labels=$(bash "$SWEEP" --test-fixture "$f" 2>/dev/null); then
        echo "FAIL (negative — unexpected match): $name → $(echo "$labels" | paste -sd ',' -)" >&2
        neg_fail=$((neg_fail + 1))
    else
        echo "PASS (negative): $name"
        neg_pass=$((neg_pass + 1))
    fi
done

echo
echo "Positive: $pos_pass pass / $pos_fail fail"
echo "Negative: $neg_pass pass / $neg_fail fail"

if (( pos_pass < 3 )); then
    echo "FAIL: fewer than 3 positive fixtures matched (thrum-e1n0 acceptance requires >= 3)" >&2
    exit 1
fi

if (( pos_fail > 0 || neg_fail > 0 )); then
    exit 1
fi

echo "OK — all fixtures behaved as expected ($pos_pass positive matched; $neg_pass negative correctly skipped)"
