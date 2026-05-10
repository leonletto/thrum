#!/usr/bin/env bash
# tests/release/helpers/assert-llm.sh — dispatch to the Go llm-judge CLI.
#
# Functions exported:
#   assert_llm_transcript_satisfies_rubric <rubric_file> <transcript_file> [threshold]
#       Runs the rubric subcommand. Returns 0 if score >= threshold,
#       3 if below threshold (soft fail), 1 on infrastructure error,
#       2 on argument error. Echoes the JSON result on stdout.
#   llm_diagnose <test_desc> <step_desc> <failed_predicate> <transcript_file> <state_file>
#       Runs the diagnose subcommand. Echoes
#       {"reasoning":"...","prompt_v":N,"model":"..."} on stdout.

# Path to the runnable judge command. Allow override via env so callers can
# point at a prebuilt binary instead of `go run` to skip the compile cost.
LLM_JUDGE_DIR="${LLM_JUDGE_DIR:-${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)}/tests/release/cmd/llm-judge}"

_llm_judge() {
  ( cd "$LLM_JUDGE_DIR" && go run . "$@" )
}

assert_llm_transcript_satisfies_rubric() {
  local rubric_file="$1" transcript_file="$2" threshold="${3:-4}"
  local out rc
  out=$(_llm_judge rubric \
    --rubric "$(cat "$rubric_file")" \
    --transcript "$transcript_file" \
    --threshold "$threshold")
  rc=$?
  echo "$out"
  return $rc
}

llm_diagnose() {
  local test_desc="$1" step_desc="$2" failed_predicate="$3"
  local transcript_file="$4" state_file="$5"
  _llm_judge diagnose \
    --test-description "$test_desc" \
    --step-description "$step_desc" \
    --failed-predicate "$failed_predicate" \
    --transcript "$transcript_file" \
    --state "$state_file"
}
