#!/usr/bin/env bash
# tests/release/helpers/behavioral.sh — YAML test-card driver.
# Reads a card, iterates steps, drives sends + polled assertions,
# writes JSONL outcome records.
#
# Required env: FIXTURE_REPO, FIXTURE_WORKSPACES, FIXTURE_THRUM, RUNTIME.
# Optional env: PREAMBLE_COORDINATOR, PREAMBLE_IMPLEMENTER.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=assert-fs.sh
source "${SCRIPT_DIR}/assert-fs.sh"
# shellcheck source=assert-daemon.sh
source "${SCRIPT_DIR}/assert-daemon.sh"
# shellcheck source=assert-tmux.sh
source "${SCRIPT_DIR}/assert-tmux.sh"
# shellcheck source=assert-llm.sh
source "${SCRIPT_DIR}/assert-llm.sh"

# Substitute ${UPPER_NAME} variables in a string from current env.
_behavioral_substitute() {
  local s="$1"
  local fr="${FIXTURE_REPO:-}" fw="${FIXTURE_WORKSPACES:-}" ft="${FIXTURE_THRUM:-}"
  local pc="${PREAMBLE_COORDINATOR:-}" pi="${PREAMBLE_IMPLEMENTER:-}" rt="${RUNTIME:-}"
  s="${s//'${FIXTURE_REPO}'/$fr}"
  s="${s//'${FIXTURE_WORKSPACES}'/$fw}"
  s="${s//'${FIXTURE_THRUM}'/$ft}"
  s="${s//'${PREAMBLE_COORDINATOR}'/$pc}"
  s="${s//'${PREAMBLE_IMPLEMENTER}'/$pi}"
  s="${s//'${RUNTIME}'/$rt}"
  printf '%s' "$s"
}

# Epoch milliseconds — portable across GNU and BSD date.
# GNU `date +%s%3N` works; BSD date emits literal "3N". Use python3 fallback.
_behavioral_epoch_ms() {
  local v
  v="$(date +%s%3N 2>/dev/null || true)"
  if [[ "$v" =~ ^[0-9]+$ ]]; then
    printf '%s' "$v"
    return
  fi
  python3 -c 'import time; print(int(time.time()*1000))'
}

# Convert "30s", "2m", "1h" to integer seconds. Default 30.
_behavioral_parse_timeout() {
  local s="$1"
  if [[ -z "$s" || "$s" == "null" ]]; then echo 30; return; fi
  case "$s" in
    *s) echo "${s%s}" ;;
    *m) echo $(( ${s%m} * 60 )) ;;
    *h) echo $(( ${s%h} * 3600 )) ;;
    *)  echo "$s" ;;
  esac
}

# Run a single assertion. Returns 0 on pass.
_behavioral_run_assertion() {
  local card="$1" step_idx="$2" assert_idx="$3"
  local kind predicate
  kind="$(yq -r ".steps[$step_idx].assert[$assert_idx].kind" "$card")"
  predicate="$(yq -r ".steps[$step_idx].assert[$assert_idx].predicate" "$card")"

  case "$kind" in
    fs)
      case "$predicate" in
        dir_exists)
          local path; path="$(_behavioral_substitute "$(yq -r ".steps[$step_idx].assert[$assert_idx].path" "$card")")"
          assert_fs_dir_exists "$path"
          ;;
        file_exists)
          local path; path="$(_behavioral_substitute "$(yq -r ".steps[$step_idx].assert[$assert_idx].path" "$card")")"
          assert_fs_file_exists "$path"
          ;;
        file_contains)
          local path needle
          path="$(_behavioral_substitute "$(yq -r ".steps[$step_idx].assert[$assert_idx].path" "$card")")"
          needle="$(yq -r ".steps[$step_idx].assert[$assert_idx].needle" "$card")"
          assert_fs_file_contains "$path" "$needle"
          ;;
        file_matches)
          local path regex
          path="$(_behavioral_substitute "$(yq -r ".steps[$step_idx].assert[$assert_idx].path" "$card")")"
          regex="$(yq -r ".steps[$step_idx].assert[$assert_idx].regex" "$card")"
          assert_fs_file_matches "$path" "$regex"
          ;;
        *) echo "behavioral: unknown fs predicate '$predicate'" >&2; return 1 ;;
      esac
      ;;
    daemon)
      case "$predicate" in
        agent_registered)
          local agent role module
          agent="$(yq -r ".steps[$step_idx].assert[$assert_idx].agent" "$card")"
          role="$(yq -r ".steps[$step_idx].assert[$assert_idx].role" "$card")"
          module="$(yq -r ".steps[$step_idx].assert[$assert_idx].module" "$card")"
          assert_daemon_agent_registered "$agent" "$role" "$module"
          ;;
        message_delivered)
          local to from pattern
          to="$(yq -r ".steps[$step_idx].assert[$assert_idx].to" "$card")"
          from="$(yq -r ".steps[$step_idx].assert[$assert_idx].from // \"\"" "$card")"
          pattern="$(yq -r ".steps[$step_idx].assert[$assert_idx].pattern // \"\"" "$card")"
          assert_daemon_message_delivered "$to" "$from" "$pattern"
          ;;
        agent_replied_to)
          local replier replied_to
          replier="$(yq -r ".steps[$step_idx].assert[$assert_idx].replier" "$card")"
          replied_to="$(yq -r ".steps[$step_idx].assert[$assert_idx].replied_to_msg" "$card")"
          assert_daemon_agent_replied_to "$replier" "$replied_to"
          ;;
        agent_session_active)
          local agent; agent="$(yq -r ".steps[$step_idx].assert[$assert_idx].agent" "$card")"
          assert_daemon_agent_session_active "$agent"
          ;;
        *) echo "behavioral: unknown daemon predicate '$predicate'" >&2; return 1 ;;
      esac
      ;;
    tmux)
      case "$predicate" in
        session_exists)
          local name; name="$(yq -r ".steps[$step_idx].assert[$assert_idx].name" "$card")"
          assert_tmux_session_exists "$name"
          ;;
        pane_running_runtime)
          local session runtime
          session="$(yq -r ".steps[$step_idx].assert[$assert_idx].session" "$card")"
          runtime="$(yq -r ".steps[$step_idx].assert[$assert_idx].runtime" "$card")"
          assert_tmux_pane_running_runtime "$session" "$runtime"
          ;;
        pane_contains)
          local session pattern
          session="$(yq -r ".steps[$step_idx].assert[$assert_idx].session" "$card")"
          pattern="$(yq -r ".steps[$step_idx].assert[$assert_idx].pattern" "$card")"
          assert_tmux_pane_contains "$session" "$pattern"
          ;;
        *) echo "behavioral: unknown tmux predicate '$predicate'" >&2; return 1 ;;
      esac
      ;;
    llm_judge)
      case "$predicate" in
        transcript_satisfies_rubric)
          local rubric session last_n threshold transcript_file rubric_file
          rubric="$(yq -r ".steps[$step_idx].assert[$assert_idx].rubric" "$card")"
          session="$(yq -r ".steps[$step_idx].assert[$assert_idx].transcript_source.session" "$card")"
          last_n="$(yq -r ".steps[$step_idx].assert[$assert_idx].transcript_source.last_n_lines // 80" "$card")"
          threshold="$(yq -r ".steps[$step_idx].assert[$assert_idx].threshold // 4" "$card")"

          rubric_file="$(mktemp)"; printf '%s' "$rubric" > "$rubric_file"
          transcript_file="$(mktemp)"
          tmux capture-pane -p -t "$session" 2>/dev/null | tail -n "$last_n" > "$transcript_file" || true

          local out rc
          out=$(assert_llm_transcript_satisfies_rubric "$rubric_file" "$transcript_file" "$threshold")
          rc=$?
          rm -f "$rubric_file" "$transcript_file"
          # Echo result on stderr so the polled-assertion runner can grab it
          # via stdout-redirect; the JSONL writer downstream prefers a clean
          # stdout for now. (Future: pipe into the JSONL record.)
          [[ -n "$out" ]] && echo "$out" >&2
          return $rc
          ;;
        *) echo "behavioral: unknown llm_judge predicate '$predicate'" >&2; return 1 ;;
      esac
      ;;
    *)
      echo "behavioral: unknown kind '$kind'" >&2
      return 1
      ;;
  esac
}

# Run all assertions for one step within a polling loop.
_behavioral_run_step_assertions() {
  local card="$1" step_idx="$2" timeout_s="$3" poll_interval="$4"
  local asserts_len; asserts_len="$(yq -r ".steps[$step_idx].assert | length // 0" "$card")"
  if [[ "$asserts_len" == "0" || "$asserts_len" == "null" ]]; then
    return 0  # no assertions on this step
  fi
  local deadline=$(( $(date +%s) + timeout_s ))
  while true; do
    local all_ok=1
    for ((j=0; j<asserts_len; j++)); do
      if ! _behavioral_run_assertion "$card" "$step_idx" "$j" 2>/dev/null; then
        all_ok=0
        break
      fi
    done
    if [[ $all_ok -eq 1 ]]; then return 0; fi
    if [[ $(date +%s) -ge $deadline ]]; then return 1; fi
    sleep "$poll_interval"
  done
}

# Auto-diagnose hook: when a step FAILed, ask the LLM judge for a one-line
# explanation, then patch the trailing JSONL record with .auto_diagnose
# (and rewrite .diagnostic if the model produced a clearer one). Skipped
# when:
#   - NO_AUTO_DIAGNOSE=1
#   - LLM_CLIENT_PATH unset (judge can't run without the upstream lib)
#   - jq, tmux, or thrum unavailable (defensive)
_behavioral_auto_diagnose() {
  local card="$1" step_idx="$2" out="$3"
  if [[ "${NO_AUTO_DIAGNOSE:-0}" == "1" ]]; then return 0; fi
  if [[ -z "${LLM_CLIENT_PATH:-}" ]]; then return 0; fi
  command -v jq >/dev/null 2>&1 || return 0

  local td sd fp transcript_file state_file diag_out
  td="$(yq -r '.description // ""' "$card")"
  sd="$(yq -r ".steps[$step_idx].id // \"\"" "$card") — $(yq -r ".steps[$step_idx].diagnostic // \"\"" "$card")"
  fp="$(yq -r ".steps[$step_idx].assert[0].kind // \"\"" "$card").$(yq -r ".steps[$step_idx].assert[0].predicate // \"\"" "$card")"
  transcript_file="$(mktemp)"
  tmux capture-pane -p -t coord 2>/dev/null | tail -200 > "$transcript_file" || true
  state_file="$(mktemp)"
  thrum --repo "${FIXTURE_REPO:-.}" agent list --json > "$state_file" 2>/dev/null || true

  diag_out=$(llm_diagnose "$td" "$sd" "$fp" "$transcript_file" "$state_file" 2>/dev/null || true)
  rm -f "$transcript_file" "$state_file"

  [[ -z "$diag_out" ]] && return 0
  local reasoning
  reasoning=$(printf '%s' "$diag_out" | jq -r '.reasoning // ""' 2>/dev/null || echo "")
  [[ -z "$reasoning" ]] && return 0

  # Patch the trailing JSONL record: add .auto_diagnose and overwrite
  # .diagnostic with the model's reasoning. macOS BSD head doesn't support
  # negative -n, so use sed '$d' to drop the last line.
  local last_line patched
  last_line="$(tail -1 "$out")"
  patched=$(printf '%s' "$last_line" | jq --arg r "$reasoning" --argjson d "$diag_out" \
    '. + {"diagnostic": $r, "auto_diagnose": $d}' 2>/dev/null || true)
  [[ -z "$patched" ]] && return 0
  sed '$d' "$out" > "$out.tmp" && printf '%s\n' "$patched" >> "$out.tmp" && mv "$out.tmp" "$out"
}

# Public: run a single test card. Writes JSONL records to $out.
behavioral_run_card() {
  local card="$1" out="$2"
  : > "$out"
  local test_id; test_id="$(yq -r '.id' "$card")"
  local steps_len; steps_len="$(yq -r '.steps | length' "$card")"

  # Send-step support is intentionally minimal in this task:
  # if steps[i].send is present, we shell out to `thrum send`.
  local passed=0 failed=0 skipped=0

  for ((i=0; i<steps_len; i++)); do
    local step_id timeout_str timeout_s poll_interval start_ms
    step_id="$(yq -r ".steps[$i].id" "$card")"
    timeout_str="$(yq -r ".steps[$i].timeout" "$card")"
    timeout_s="$(_behavioral_parse_timeout "$timeout_str")"
    poll_interval="$(yq -r ".steps[$i].poll_interval // 3" "$card")"
    start_ms=$(_behavioral_epoch_ms)

    # Optional send. Surface delivery failures as a FAIL JSONL record
    # rather than silently swallowing them — otherwise a kickoff message
    # that never lands would masquerade as "assertion timed out."
    local send_failed=0 send_err=""
    if [[ "$(yq -r ".steps[$i].send" "$card")" != "null" ]]; then
      local to msg
      to="$(yq -r ".steps[$i].send.to" "$card")"
      msg="$(_behavioral_substitute "$(yq -r ".steps[$i].send.message" "$card")")"
      send_err=$(thrum --repo "${FIXTURE_REPO:-.}" send --to "$to" "$msg" 2>&1 >/dev/null) || send_failed=1
    fi

    if [[ $send_failed -eq 1 ]]; then
      local end_ms; end_ms=$(_behavioral_epoch_ms)
      local err_json; err_json=$(printf '%s' "send failed: $send_err" | jq -Rs .)
      printf '{"test":"%s","step":"%s","outcome":"FAIL","duration_ms":%d,"diagnostic":%s}\n' \
        "$test_id" "$step_id" "$((end_ms - start_ms))" "$err_json" >> "$out"
      failed=$((failed+1))
    elif _behavioral_run_step_assertions "$card" "$i" "$timeout_s" "$poll_interval"; then
      local end_ms; end_ms=$(_behavioral_epoch_ms)
      printf '{"test":"%s","step":"%s","outcome":"PASS","duration_ms":%d}\n' \
        "$test_id" "$step_id" "$((end_ms - start_ms))" >> "$out"
      passed=$((passed+1))
    else
      local end_ms; end_ms=$(_behavioral_epoch_ms)
      local diagnostic diagnostic_json
      diagnostic="$(yq -r ".steps[$i].diagnostic // \"\"" "$card")"
      # Use jq to JSON-encode the diagnostic so backslashes, newlines, and
      # control chars are properly escaped (not just double-quotes).
      diagnostic_json=$(printf '%s' "$diagnostic" | jq -Rs .)
      printf '{"test":"%s","step":"%s","outcome":"FAIL","duration_ms":%d,"diagnostic":%s}\n' \
        "$test_id" "$step_id" "$((end_ms - start_ms))" "$diagnostic_json" >> "$out"
      failed=$((failed+1))
      _behavioral_auto_diagnose "$card" "$i" "$out"
    fi
  done

  printf '{"test":"%s","step":"__summary__","passed":%d,"failed":%d,"skipped":%d,"total":%d}\n' \
    "$test_id" "$passed" "$failed" "$skipped" "$((passed+failed+skipped))" >> "$out"
  if (( failed > 0 )); then return 1; fi
  return 0
}
