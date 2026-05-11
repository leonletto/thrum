#!/usr/bin/env bash
# tests/release/behavioral/diff-behavioral.sh — pure structural diff
# between two run dirs. Walks each JSONL under <run-dir-A> and pairs it
# with the same-named file in <run-dir-B>, printing a per-step delta
# table with markers: '=' identical, '+' regression-fixed (A FAIL → B
# PASS), '-' new regression (A PASS → B FAIL), '?' unknown.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "Usage: diff-behavioral.sh <run-dir-A> <run-dir-B>" >&2
  exit 2
fi
A="$1"; B="$2"
[[ -d "$A" && -d "$B" ]] || { echo "ERROR: run dir(s) missing" >&2; exit 2; }

printf '%-30s %-30s %-6s %-6s %s\n' "TEST" "STEP" "A" "B" "DELTA"
shopt -s nullglob
for jsonl_a in "$A"/*.jsonl; do
  test_name="$(basename "$jsonl_a" .jsonl)"
  jsonl_b="$B/${test_name}.jsonl"
  [[ -f "$jsonl_b" ]] || continue
  diff_a=$(mktemp); diff_b=$(mktemp)
  jq -r 'select(.step!="__summary__") | "\(.step)\t\(.outcome)"' "$jsonl_a" > "$diff_a"
  jq -r 'select(.step!="__summary__") | "\(.step)\t\(.outcome)"' "$jsonl_b" > "$diff_b"
  paste "$diff_a" "$diff_b" | awk -v t="$test_name" -F'\t' '
    {
      step=$1; oa=$2; sb=$3; ob=$4
      delta = (oa==ob) ? "=" : ((oa=="FAIL" && ob=="PASS") ? "+" : ((oa=="PASS" && ob=="FAIL") ? "-" : "?"))
      printf "%-30s %-30s %-6s %-6s %s\n", t, step, oa, ob, delta
    }'
  rm -f "$diff_a" "$diff_b"
done
