#!/usr/bin/env bash
# tests/release/behavioral/validate-card.sh — schema validation for
# behavioral test cards. Requires mikefarah/yq v4+.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: validate-card.sh <card.yaml>" >&2
  exit 2
fi
CARD="$1"

if ! command -v yq >/dev/null 2>&1; then
  echo "validate-card: yq not found on PATH (need mikefarah/yq v4+)" >&2
  exit 2
fi
yq_version="$(yq --version 2>&1 | head -1)"
if ! { grep -q 'mikefarah' <<<"$yq_version" && grep -q -E 'v?4\.' <<<"$yq_version"; }; then
  echo "validate-card: incompatible yq ('$yq_version'); need mikefarah/yq v4+" >&2
  exit 2
fi
if [[ ! -f "$CARD" ]]; then
  echo "validate-card: file not found: $CARD" >&2
  exit 2
fi

errors=0
err() { echo "validate-card($CARD): $*" >&2; errors=$((errors+1)); }

# Top-level required keys
for key in id description agents steps; do
  if [[ "$(yq ".$key" "$CARD")" == "null" ]]; then
    err "missing top-level key: $key"
  fi
done

# steps must be a non-empty array
steps_len="$(yq '.steps | length' "$CARD")"
if [[ "$steps_len" == "0" || "$steps_len" == "null" ]]; then
  err "steps must be a non-empty array"
fi

# Each step needs id and timeout
for ((i=0; i<steps_len; i++)); do
  if [[ "$(yq ".steps[$i].id" "$CARD")" == "null" ]]; then
    err "step[$i] missing id"
  fi
  if [[ "$(yq ".steps[$i].timeout" "$CARD")" == "null" ]]; then
    err "step[$i] missing timeout"
  fi

  # Each assert entry needs kind and predicate
  asserts_len="$(yq ".steps[$i].assert | length" "$CARD" 2>/dev/null || echo 0)"
  if [[ "$asserts_len" =~ ^[0-9]+$ ]]; then
    for ((j=0; j<asserts_len; j++)); do
      if [[ "$(yq ".steps[$i].assert[$j].kind" "$CARD")" == "null" ]]; then
        err "step[$i].assert[$j] missing kind"
      fi
      if [[ "$(yq ".steps[$i].assert[$j].predicate" "$CARD")" == "null" ]]; then
        err "step[$i].assert[$j] missing predicate"
      fi
    done
  fi
done

if (( errors > 0 )); then
  echo "validate-card: $errors error(s)" >&2
  exit 1
fi
echo "validate-card: $CARD OK" >&2
