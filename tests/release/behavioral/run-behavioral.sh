#!/usr/bin/env bash
# tests/release/behavioral/run-behavioral.sh — entry-point runner.
set -euo pipefail

RUNNER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${RUNNER_DIR}/../../.." && pwd)"
HELPERS_DIR="${REPO_ROOT}/tests/release/helpers"
CARDS_DIR="${RUNNER_DIR}/cards"
RESULTS_DIR_DEFAULT="${REPO_ROOT}/dev-docs/behavioral"

# Self-isolating launcher (same mechanism as run.sh): if invoked from inside
# an agent pane (claude/codex ancestor), re-exec into a detached default-
# server tmux session so the harness runs with clean process ancestry.
# Must fire BEFORE any further setup / CLI parsing side effects.
# shellcheck disable=SC1091
source "${HELPERS_DIR}/self-isolate.sh"
thrum_release_self_isolate "${RUNNER_DIR}/run-behavioral.sh" "$@"

# CLI defaults
RUNTIME="claude"
# RUNTIME_EXPLICIT: 0 = auto-select per card via filename convention
# (NN-codex-* -> codex; else claude); 1 = user passed --runtime= override
# which then applies uniformly to every card. The auto-select case is the
# default so a single `bash run-behavioral.sh` invocation runs card 01 under
# claude AND cards 02-05 under codex without flags (the "codex two-pass").
RUNTIME_EXPLICIT=0
declare -A PREAMBLES=()
FILTER="*.yaml"
NO_AUTO_DIAGNOSE=0
CAPTURE=""
COMPARE=""
RESULTS_DIR="${THRUM_BEHAVIORAL_RESULTS_DIR:-$RESULTS_DIR_DEFAULT}"

usage() {
  cat <<'USAGE'
Usage: run-behavioral.sh [options]
  --runtime=<name>            runtime to test (default: claude)
  --preamble=<role>:<path>    candidate preamble for a role (repeatable)
  --filter=<glob>             card-file glob (default: *.yaml)
  --no-auto-diagnose          disable LLM auto-diagnose on failed steps
  --capture <name>            capture-mode: save baseline to baselines/<name>/
  --compare <name>            compare-mode: score against baselines/<name>/
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --runtime=*) RUNTIME="${1#--runtime=}"; RUNTIME_EXPLICIT=1; shift ;;
    --preamble=*)
      val="${1#--preamble=}"
      role="${val%%:*}"
      path="${val#*:}"
      PREAMBLES[$role]="$path"
      shift
      ;;
    --filter=*) FILTER="${1#--filter=}"; shift ;;
    --no-auto-diagnose) NO_AUTO_DIAGNOSE=1; shift ;;
    --capture) CAPTURE="$2"; shift 2 ;;
    --compare) COMPARE="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown arg: $1" >&2; usage >&2; exit 2 ;;
  esac
done

# Load .env from the main repo so LLM_CLIENT_PATH and ZAI_API_KEY are
# available to the LLM-judge auto-diagnose path. .env lives at the main
# repo root (worktrees don't share gitignored files); locate it via
# git rev-parse --git-common-dir whose parent is the main repo.
_main_repo="$(cd "$REPO_ROOT" && cd "$(git rev-parse --git-common-dir)/.." && pwd 2>/dev/null || true)"
if [[ -n "$_main_repo" && -f "$_main_repo/.env" ]]; then
  set -a; source "$_main_repo/.env"; set +a
fi

# Preflight. Note: the AI runtime (claude/codex) is intentionally NOT in this
# list — runtime selection is per-card (see _card_runtime below) and each
# card's required runtime is checked at its turn, with a clear skip+fail
# message if the binary is absent. That way a partial install (e.g. claude
# only, no codex) still runs the cards it CAN run instead of failing the
# whole invocation.
for tool in thrum tmux jq yq git; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "ERROR: required tool '$tool' not found in PATH" >&2
    exit 2
  fi
done

# _card_runtime <card-file>
# Resolve the AI runtime a card should run under. If the user passed an
# explicit --runtime= override on the command line, that wins (uniform across
# every card). Otherwise auto-derive from the card filename: NN-codex-*
# uses codex, anything else uses claude. The behavioral set ships as 01
# (worktree-create-launch — runtime-agnostic, runs as claude) + 02-05
# (codex-* — codex-specific hook tests).
_card_runtime() {
  local card="$1"
  if [ "$RUNTIME_EXPLICIT" = "1" ]; then
    printf '%s' "$RUNTIME"
    return
  fi
  case "$(basename "$card")" in
    *-codex-*) printf '%s' "codex" ;;
    *)         printf '%s' "claude" ;;
  esac
}
yq_v="$(yq --version 2>&1 | head -1)"
if ! { grep -q 'mikefarah' <<<"$yq_v" && grep -q -E 'v?4\.' <<<"$yq_v"; }; then
  echo "ERROR: incompatible yq ('$yq_v'); need mikefarah/yq v4+" >&2
  exit 2
fi

# Intentionally NOT sourcing all.sh / setup-repo.sh / teardown.sh: those
# wire up the scenarios fixture (fixed coord/impl panes via tmux-exec) and
# their sourcing has side effects that conflict with the ephemeral-daemon
# fixture. The behavioral harness sources only the helpers it needs.
source "${HELPERS_DIR}/ephemeral-daemon.sh"
source "${HELPERS_DIR}/render-preamble.sh"
source "${HELPERS_DIR}/behavioral.sh"
source "${HELPERS_DIR}/extract-tool-calls.sh"
# fixture-perms.sh + drive.sh are pure function definitions; safe to source
# here without dragging run.sh-specific state in. fixture-perms gives
# write_fixture_perms (per-tool Bash allowlist for autonomous-tool-use cards);
# drive.sh gives clear_trust (sends Enter to clear the folder-trust dialog on
# daemon-launched panes — needed by behavioral's _register_card_agents).
source "${HELPERS_DIR}/fixture-perms.sh"
source "${HELPERS_DIR}/drive.sh"
# runtime_version is defined in assert-tmux.sh (sourced transitively by
# behavioral.sh).

# Fixture lifecycle (one fixture for the whole run)
RUN_TIMESTAMP="$(date -u +%Y-%m-%dT%H-%M-%S)"
SHORT_SHA() { echo "$1" | cut -c1-8; }
COORD_SHA="baseline"
IMPL_SHA="baseline"
if [[ -n "${PREAMBLES[coordinator]:-}" ]]; then
  COORD_SHA="$(SHORT_SHA "$(shasum -a 256 "${PREAMBLES[coordinator]}" | awk '{print $1}')")"
fi
if [[ -n "${PREAMBLES[implementer]:-}" ]]; then
  IMPL_SHA="$(SHORT_SHA "$(shasum -a 256 "${PREAMBLES[implementer]}" | awk '{print $1}')")"
fi
RUN_DIR="${RESULTS_DIR}/runs/${RUN_TIMESTAMP}-${RUNTIME}-coord:${COORD_SHA}_impl:${IMPL_SHA}"
mkdir -p "$RUN_DIR"

# Use /tmp directly with a short prefix: macOS AF_UNIX socket path limit is
# 104 bytes, and $TMPDIR on macOS is /var/folders/... (~50 bytes) which leaves
# little room for the fixture's daemon socket. Long mktemp prefixes inside
# $TMPDIR push the .thrum/daemon.sock path past the limit and cause
# "timeout waiting for daemon to start".
FIXTURE_BASE="$(mktemp -d /tmp/bh-XXXXXX)"

# Cleanup honors THRUM_BEHAVIORAL_NO_TEARDOWN=1 so failed runs preserve the
# fixture for forensic inspection (mirrors the scenarios fixture pattern).
total_fail=0
_cleanup() {
  if [[ "${THRUM_BEHAVIORAL_NO_TEARDOWN:-0}" == "1" && ${total_fail:-0} -gt 0 ]]; then
    echo "THRUM_BEHAVIORAL_NO_TEARDOWN=1: fixture preserved at $FIXTURE_BASE (failures=${total_fail})" >&2
    ephemeral_daemon_stop
  else
    ephemeral_daemon_stop
    rm -rf "$FIXTURE_BASE"
  fi
}
trap _cleanup EXIT

if ! ephemeral_daemon_start "$FIXTURE_BASE"; then
  echo "ERROR: ephemeral-daemon setup failed" >&2
  exit 2
fi
# ephemeral_daemon_start exports FIXTURE_REPO, FIXTURE_THRUM, FIXTURE_WORKSPACES
# and patches FIXTURE_THRUM/config.json's worktrees.base_path so coord-spawned
# worktrees land at FIXTURE_WORKSPACES/<name> (not nested under FIXTURE_REPO).
export RUNTIME

# Seed all roles from project baseline first
thrum --repo "$FIXTURE_REPO" roles deploy >/dev/null 2>&1 || true

# Then overwrite swapped roles with candidates
for role in "${!PREAMBLES[@]}"; do
  src="${PREAMBLES[$role]}"
  if [[ ! -f "$src" ]]; then
    echo "ERROR: preamble file missing: $src" >&2
    exit 2
  fi
  render_preamble \
    --role "$role" \
    --src "$src" \
    --agent-name "test_${role}" \
    --module main \
    --worktree "$FIXTURE_REPO" \
    --coordinator-name test_coordinator \
    --repo-root "$FIXTURE_REPO"
  export "PREAMBLE_$(echo "$role" | tr 'a-z' 'A-Z')=$src"
done

# Validate and run each card matching --filter
shopt -s nullglob
cards=("${CARDS_DIR}"/${FILTER})
if [[ ${#cards[@]} -eq 0 ]]; then
  echo "ERROR: no cards matched filter '$FILTER'" >&2
  exit 2
fi

# Register and tmux-launch any agents declared in the card's `agents:`
# block. Convention: derive agent name as `test_<role>`, use the YAML
# key as the tmux session name. Module defaults to "main" unless the
# card specifies one. Each call is best-effort; if registration or
# launch fails the dispatch step's send will surface it.
_register_card_agents() {
  local card="$1"
  local agent_key role module session_name agent_name
  # Pre-grant the Bash tool in the fixture repo so card 01's coord (and any
  # other autonomous-tool-use card) doesn't stall on per-tool prompts when
  # claude invokes `thrum worktree create` / `thrum tmux launch` / etc. via
  # the Bash tool. Idempotent overwrite (cards share a single $FIXTURE_REPO
  # but this is called per-card per the design-doc spec).
  write_fixture_perms "$FIXTURE_REPO"
  while IFS= read -r agent_key; do
    [[ -z "$agent_key" || "$agent_key" == "null" ]] && continue
    role="$(yq -r ".agents.${agent_key}.role // \"\"" "$card")"
    [[ -z "$role" || "$role" == "null" ]] && {
      echo "WARN: agent '${agent_key}' has no role; skipping" >&2
      continue
    }
    module="$(yq -r ".agents.${agent_key}.module // \"main\"" "$card")"
    session_name="${agent_key}"
    agent_name="test_${role}"
    echo "  registering agent: name=${agent_name} role=${role} module=${module} session=${session_name}"
    # `thrum tmux create` validates that --cwd is a registered worktree —
    # the fixture is a standalone git repo, so that path errors with
    # "not a worktree". Use the documented decomposed sequence instead:
    # quickstart (register agent), bare `tmux new-session` (host pane),
    # then `thrum tmux launch` (start the AI tool inside).
    ( cd "$FIXTURE_REPO" \
      && env -u THRUM_HOME -u THRUM_AGENT_ID -u THRUM_INTENT \
         thrum --repo "$FIXTURE_REPO" quickstart \
           --name "$agent_name" --role "$role" --module "$module" \
           --force >/dev/null 2>&1 ) || true
    # Scrub TMUX/TMUX_PANE so the host pane lands on the DEFAULT tmux server,
    # where the daemon's `thrum tmux launch` looks (safecmd.cleanTmuxEnv forces
    # the daemon onto the default server). Without this, running the harness
    # from inside a tmux-exec session inherits $TMUX and creates the fixture
    # session on the tmux-exec socket — the daemon can't find it and claude
    # never launches (bare-shell fixture). The default-server parent (pid 1)
    # carries no claude ancestry, so there's no PID contamination.
    env -u TMUX -u TMUX_PANE tmux new-session -d -s "$session_name" -c "$FIXTURE_REPO" 2>/dev/null || true
    ( cd "$FIXTURE_REPO" \
      && env -u THRUM_HOME -u THRUM_AGENT_ID -u THRUM_INTENT \
         thrum --repo "$FIXTURE_REPO" tmux launch "$session_name" \
           --runtime "$RUNTIME" >/dev/null 2>&1 ) || true
    # Clear claude's folder-trust dialog so the daemon's runPostLaunchInject
    # can auto-prime (it skips inject while the trust gate is up + does not
    # retry). Same drive.sh primitive run.sh setup-repo.sh uses on coord/impl.
    # Skip for non-claude runtimes (codex doesn't have this dialog).
    if [[ "$RUNTIME" == "claude" ]]; then
      clear_trust "$session_name"
    fi
  done < <(yq -r '.agents | keys // [] | .[]' "$card")
}

# Preflight: warn if any selected card declares an llm_judge predicate but
# LLM_CLIENT_PATH/ZAI_API_KEY are unset. The predicate fails closed at
# runtime; we surface the misconfiguration up front so the user knows
# which steps will fail and why before committing 5+ minutes per card.
if [[ -z "${LLM_CLIENT_PATH:-}" || -z "${ZAI_API_KEY:-}" ]]; then
  for card in "${cards[@]}"; do
    if yq -r '.steps[]?.assert[]?.kind' "$card" 2>/dev/null | grep -q '^llm_judge$'; then
      echo "WARN: $(basename "$card") declares llm_judge predicate(s) but LLM_CLIENT_PATH or ZAI_API_KEY is unset; those steps will fail closed." >&2
    fi
  done
fi

total_pass=0
for card in "${cards[@]}"; do
  bash "${RUNNER_DIR}/validate-card.sh" "$card" || exit 2
  test_id="$(yq -r '.id' "$card")"
  out="${RUN_DIR}/${test_id}.jsonl"
  # Per-card runtime resolution + availability check. If the resolved
  # runtime isn't on PATH, surface a clear fail-loud message and count it
  # as a failure (rather than silently running under the wrong runtime —
  # the Stop-hook-loop failure mode observed on codex cards run as claude).
  RUNTIME="$(_card_runtime "$card")"
  if ! command -v "$RUNTIME" >/dev/null 2>&1; then
    echo "==> ${test_id}"
    echo "    SKIP/FAIL: card requires --runtime=${RUNTIME}; '${RUNTIME}' not installed on PATH" >&2
    total_fail=$((total_fail+1))
    continue
  fi
  echo "==> ${test_id} (runtime: ${RUNTIME})"
  _register_card_agents "$card"
  if behavioral_run_card "$card" "$out"; then
    pass=1
    total_pass=$((total_pass+1))
  else
    pass=0
    total_fail=$((total_fail+1))
  fi
  # Print brief per-test summary
  yq_summary="$(grep '"step":"__summary__"' "$out" | tail -1 || true)"
  echo "    ${yq_summary}"

  # --capture: write a baseline JSON for this test
  if [[ -n "$CAPTURE" ]]; then
    baseline_dir="${RESULTS_DIR}/baselines/${CAPTURE}"
    mkdir -p "$baseline_dir"
    tc_file="$(mktemp)"
    extract_tool_calls "$RUNTIME" "${HOME}/.claude/projects" > "$tc_file"
    runtime_v="$(runtime_version "$RUNTIME" 2>/dev/null || echo "")"
    # Build the preamble manifest: per-role {path, sha256} for the candidate
    # preambles supplied via --preamble. Empty when only project baseline is
    # in effect. Spec §--capture requires this for reproducibility.
    pre_file="$(mktemp)"
    {
      echo "{"
      first=1
      for r in "${!PREAMBLES[@]}"; do
        p="${PREAMBLES[$r]}"
        sha=$(shasum -a 256 "$p" 2>/dev/null | awk '{print $1}')
        [[ $first -eq 1 ]] || echo ","
        first=0
        printf '  "%s": {"path": %s, "sha256": "%s"}' "$r" "$(printf '%s' "$p" | jq -Rs .)" "$sha"
      done
      echo
      echo "}"
    } > "$pre_file"
    transcripts_sidecar="${out%.jsonl}.transcripts.json"
    [[ -f "$transcripts_sidecar" ]] || echo "{}" > "$transcripts_sidecar"
    jq -s --arg test "$test_id" --arg runtime "$RUNTIME" --arg rtv "$runtime_v" \
          --slurpfile tc "$tc_file" --slurpfile pre "$pre_file" --slurpfile tr "$transcripts_sidecar" \
       '{ test: $test, runtime: $runtime, runtime_version: $rtv,
          preamble: ($pre[0] // {}),
          steps: [.[]? | select(.step!="__summary__")
                  | { step_id: .step, outcome: .outcome, duration_ms: .duration_ms,
                      transcript_excerpt: ($tr[0][.step] // "") }],
          tool_calls: ($tc[0] // []) }' \
       "$out" > "${baseline_dir}/${test_id}.json"
    rm -f "$tc_file" "$pre_file"
    echo "    captured baseline → ${baseline_dir}/${test_id}.json"
  fi

  # --compare: emit similarity.json by calling the judge per step
  if [[ -n "$COMPARE" ]]; then
    baseline_file="${RESULTS_DIR}/baselines/${COMPARE}/${test_id}.json"
    if [[ ! -f "$baseline_file" ]]; then
      echo "    --compare: no baseline at $baseline_file (skipping)" >&2
    else
      observed_file="$(mktemp)"
      runtime_v="$(runtime_version "$RUNTIME" 2>/dev/null || echo "")"
      jq -s --arg test "$test_id" --arg runtime "$RUNTIME" --arg rtv "$runtime_v" \
         '{ test: $test, runtime: $runtime, runtime_version: $rtv,
            steps: [.[]? | select(.step!="__summary__") | { step_id: .step, outcome: .outcome, duration_ms: .duration_ms }] }' \
         "$out" > "$observed_file"
      sim_out="${RUN_DIR}/${test_id}.similarity.json"
      sim_judge_dir="${LLM_JUDGE_DIR:-${REPO_ROOT}/tests/release/cmd/llm-judge}"
      step_intent="$(yq -r '.description // ""' "$card")"
      ( cd "$sim_judge_dir" \
        && go run . similarity \
             --baseline "$baseline_file" \
             --observed "$observed_file" \
             --step-intent "$step_intent" 2>/dev/null > "$sim_out" ) || \
        echo "    --compare: similarity call failed" >&2
      rm -f "$observed_file"
      [[ -s "$sim_out" ]] && echo "    similarity → $sim_out"
    fi
  fi
done

echo ""
echo "Run complete. Results: ${RUN_DIR}"
echo "Tests passed: ${total_pass}, failed: ${total_fail}"
[[ $total_fail -eq 0 ]] || exit 1
