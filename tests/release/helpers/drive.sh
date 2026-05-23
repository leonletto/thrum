#!/usr/bin/env bash
# tests/release/helpers/drive.sh — drive panes (send keystrokes) + poll JSONL
# for matching entries. Depends on paths.sh (sourced via helpers/all.sh).

# send_command <pane-name> <text>
# Sends `text` followed by Enter into the named tmux pane.
# Pane name is the tmux session name (e.g. "coord", "impl").
#
# Uses raw `tmux send-keys` (not `thrum tmux send`) for two reasons:
#
#   1. `thrum tmux send` delivers text-only (no Enter), so the next send
#      would concatenate on the same input line and never execute.
#
#   2. Claude Code's bash-prefix mode (where `! cmd` runs as a sub-shell
#      and emits `<bash-stdout>...</bash-stdout>` markers — what
#      assert_jsonl reads) is a keystroke-time UI switch: typing `!` as
#      the FIRST character changes input mode BEFORE the rest is typed.
#      Sending the whole `! cmd` line in one batch makes Claude treat it
#      as chat content and miss the bash-prefix entirely. Sending `!`
#      discretely (with a brief pause for the UI to switch), then the
#      rest of the line, triggers the mode change correctly.
send_command() {
  local pane="$1"
  shift
  local text="$*"
  # Wait for the pane to be idle (no rendering activity) before sending.
  # When scenarios fire ! commands rapid-fire, the next ! can land while
  # claude is still rendering the previous response. The ! must hit at
  # column 0 of an empty input line to trigger Claude Code's bash-prefix
  # mode; if it lands during a render, it gets typed as a literal char
  # in the input area instead and bash mode never engages. Measured: a
  # bash command in coord/impl finishes rendering in ~1.4s.
  wait_for_pane_idle "$pane" 10
  # Send text and Enter in SEPARATE tmux send-keys calls with a brief pause.
  # Bundling them as `tmux send-keys -t <pane> "<text>" Enter` delivers the
  # Enter key while Claude Code is still rendering the long input — claude
  # sees the Enter as part of pending render-state input, not as a submit.
  # Splitting them, with a short settle delay between, lets Enter land on
  # the rendered prompt and trigger submit. Verified empirically: bundled
  # Enter never produced a bash-stdout entry; separate Enter does.
  if [[ "$text" == "! "* ]]; then
    tmux send-keys -t "$pane" "!"
    sleep 0.3
    tmux send-keys -t "$pane" "${text#! }"
    sleep 0.5
    tmux send-keys -t "$pane" Enter
  else
    tmux send-keys -t "$pane" "$text"
    sleep 0.5
    tmux send-keys -t "$pane" Enter
  fi
}

# send_slash_command <pane> <command>
# Sends a Claude Code slash command (e.g. "/thrum:load-context",
# "/thrum:update-project") into a tmux pane in two pieces: the leading
# `/` first, brief settle, then the rest of the command, brief settle,
# then Enter.
#
# Same rationale as send_command's `!`-prefix split — Claude Code's UI
# treats `/` as a keystroke-time mode switch (slash command palette).
# Bundling "/foo:bar" in a single tmux send-keys call sometimes lets
# the rest of the text race the mode switch, causing claude to receive
# the command as plain chat instead of registering it as a slash
# invocation.
#
# Accepts the command WITH or WITHOUT the leading `/` (canonicalizes
# internally).
send_slash_command() {
  local pane="$1" cmd="$2"
  # Strip a leading slash so the explicit `/` keystroke is the only one.
  local body="${cmd#/}"
  # Settle the pane first — same gating as send_command, so callers
  # don't need a separate wait_for_pane_idle.
  wait_for_pane_idle "$pane" 10
  tmux send-keys -t "$pane" "/"
  sleep 0.5
  tmux send-keys -t "$pane" "$body"
  sleep 0.8
  tmux send-keys -t "$pane" Enter
}

# wait_for_bash_stdout_contains <repo-path> <substring> [timeout-seconds] [floor-ts]
# Specialization of wait_for_jsonl_match for the most common assertion
# shape: "wait for a `!`-prefix bash command's <bash-stdout> entry whose
# content contains <substring>". Uses jq --arg so <substring> can contain
# any characters (quotes, parens, etc.) without escaping headaches.
# Echoes the matching JSONL line on success, exit 0. Empty + exit 1 on timeout.
# Default timeout: 60s.
#
# floor-ts (optional, RFC3339): when supplied, only entries with
# .timestamp >= floor-ts match. Without this filter, a stale bash-stdout
# entry from a PRIOR scenario whose content happens to contain the
# substring would short-circuit the wait — a silent false positive
# masking the real command's failure (root cause of v0.10.6 RC1
# kafm.6 cascade: scen 02/21's earlier "thrum tmux restart" output
# stale-matched scen 69's "restarted" substring wait, hiding scen
# 69's actual --force! typo and allowing the cascade to roll forward).
# Use a `date -u +%Y-%m-%dT%H:%M:%S` captured BEFORE the send_command
# that produces the awaited stdout. Lexicographic comparison against
# claude's RFC3339 timestamps (with trailing 'Z') works correctly
# because the floor's shorter form sorts before any same-second JSONL
# entry. thrum-rbp6 covers the underlying keystroke race; this
# floor_ts filter contains the silent-false-positive symptom.
wait_for_bash_stdout_contains() {
  local repo="$1" substring="$2" timeout="${3:-60}" floor_ts="${4:-}"
  local project_dir="$HOME/.claude/projects/$(encode_cwd "$repo")"
  local elapsed=0
  local interval=1
  while [ "$elapsed" -lt "$timeout" ]; do
    if [ -d "$project_dir" ]; then
      local match
      if [ -n "$floor_ts" ]; then
        match=$(jq -c --arg sub "$substring" --arg floor "$floor_ts" \
          'select(.type == "user" and (.message.content | type == "string") and (.message.content | startswith("<bash-stdout>")) and (.message.content | contains($sub)) and (.timestamp // "" | tostring) >= $floor)' \
          "$project_dir"/*.jsonl 2>/dev/null | head -n1 || true)
      else
        match=$(jq -c --arg sub "$substring" \
          'select(.type == "user" and (.message.content | type == "string") and (.message.content | startswith("<bash-stdout>")) and (.message.content | contains($sub)))' \
          "$project_dir"/*.jsonl 2>/dev/null | head -n1 || true)
      fi
      if [ -n "$match" ]; then
        printf '%s' "$match"
        return 0
      fi
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done
  return 1
}

# send_bash_and_wait <pane> <repo-path> <bash-cmd> <expected-substring> [timeout]
# Sends `! <bash-cmd>` to <pane> via send_command (which handles the
# discrete-! pattern, the wait_for_pane_idle gate, and the separate-Enter
# trick) and then waits for a bash-stdout JSONL entry that contains
# <expected-substring>. Returns 0 on match, 1 on timeout.
#
# Captures an RFC3339 floor timestamp BEFORE send_command and passes it
# to wait_for_bash_stdout_contains so the wait can never short-circuit
# on a stale bash-stdout entry from a prior scenario. Without this
# guard, a substring like "restarted" or "Launched" or "Session
# created" that appears in earlier scenarios' JSONL would let this
# function return success before the in-flight command has even been
# typed. See wait_for_bash_stdout_contains' floor-ts doc + thrum-rbp6
# for the cascade history.
#
# Use this instead of inlining `tmux send-keys ... Enter` + a verbose
# wait_for_jsonl_match jq filter — too easy to typo the filter and break
# silently. Default timeout: 60s.
send_bash_and_wait() {
  local pane="$1" repo="$2" cmd="$3" expected="$4" timeout="${5:-60}"
  local floor_ts
  floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"
  send_command "$pane" "! $cmd"
  wait_for_bash_stdout_contains "$repo" "$expected" "$timeout" "$floor_ts" >/dev/null
}

# wait_for_pane_idle <pane> [max-seconds]
# Polls `tmux capture-pane` every 500ms; returns once the captured content
# is unchanged across 2 consecutive samples (≈1s of stability). Returns
# with success on timeout too — we'd rather over-send than block forever.
wait_for_pane_idle() {
  local pane="$1"
  local max_seconds="${2:-10}"
  local prev_hash=""
  local stable=0
  local elapsed_ms=0
  local max_ms=$((max_seconds * 1000))
  while [ "$elapsed_ms" -lt "$max_ms" ]; do
    local hash
    hash=$(tmux capture-pane -t "$pane" -p | md5sum | cut -d' ' -f1)
    if [ "$hash" = "$prev_hash" ]; then
      stable=$((stable + 1))
      if [ "$stable" -ge 2 ]; then
        return 0
      fi
    else
      stable=0
      prev_hash="$hash"
    fi
    sleep 0.5
    elapsed_ms=$((elapsed_ms + 500))
  done
  return 0
}

# wait_for_jsonl_match <repo-path> <jq-filter> [timeout-seconds] [floor-ts]
# Polls all .jsonl files under the agent's Claude project dir for the first
# line where <jq-filter> evaluates truthy. Echoes the matching line on success,
# exit 0. Empty + exit 1 on timeout. Default timeout: 30s.
#
# Searches ALL .jsonl files in the project dir (not just the newest) because
# Claude Code can write multiple JSONLs in parallel — a main-session file
# plus agent-*.jsonl files for sub-agent runs (e.g. SessionStart hooks that
# spawn skill agents). The entry we want may be in any of them, and the
# "newest by mtime" at poll-start may not be the one that ends up carrying
# the bash-stdout entry once the conversation lands.
#
# floor-ts (optional, RFC3339): when supplied, the function adds an
# additional `.timestamp >= floor-ts` clause to the user's filter via jq's
# `and`. Stale-match guard — same rationale as
# wait_for_bash_stdout_contains' floor-ts. Use a captured
# `date -u +%Y-%m-%dT%H:%M:%S` from BEFORE the command that produces
# the awaited entry. Lexicographic comparison works against claude's
# RFC3339 timestamps including milliseconds (the floor's "no Z" form
# sorts strictly before any same-second JSONL entry).
wait_for_jsonl_match() {
  local repo="$1" filter="$2" timeout="${3:-30}" floor_ts="${4:-}"
  local project_dir="$HOME/.claude/projects/$(encode_cwd "$repo")"
  local elapsed=0
  local interval=1
  # Fast-fail validation for floor_ts when supplied. A multi-line or
  # otherwise-malformed value (e.g. a subshell whose output bled stderr
  # into the captured string) would inline into the jq filter and
  # produce a parse error on every poll, which the `|| true` below
  # swallows — resulting in a silent timeout that looks like the
  # awaited event never fired. Validating the prefix at entry catches
  # this loudly and immediately. RFC3339 calendar prefix is sufficient:
  # we don't need to validate the full format, just that it isn't a
  # newline-separated multi-value or a non-timestamp string.
  if [ -n "$floor_ts" ]; then
    # Strip trailing whitespace (handles the common `$(date ...)`
    # trailing-newline case explicitly so callers who forget to
    # `printf '%s'` get sane behavior).
    floor_ts="${floor_ts%$'\n'}"
    if [[ ! "$floor_ts" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2} ]]; then
      echo "wait_for_jsonl_match: floor_ts '$floor_ts' is not RFC3339-prefixed; refusing to inline into jq filter" >&2
      return 1
    fi
  fi
  local effective_filter="$filter"
  if [ -n "$floor_ts" ]; then
    # Wrap the caller's filter in an outer expression that ANDs in the
    # timestamp clause. Parens preserve the user's filter precedence
    # regardless of internal `and`/`or`. floor_ts validated above so
    # the inlined string is known-safe.
    effective_filter="(${filter}) and ((.timestamp // \"\") | tostring) >= \"${floor_ts}\""
  fi
  while [ "$elapsed" -lt "$timeout" ]; do
    if [ -d "$project_dir" ]; then
      local match
      match=$(jq -c "select($effective_filter)" "$project_dir"/*.jsonl 2>/dev/null | head -n1 || true)
      if [ -n "$match" ]; then
        printf '%s' "$match"
        return 0
      fi
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done
  return 1
}

# wait_for_session_start <repo-path> [timeout-seconds]
# Specialization: waits for the first SessionStart hook attachment to appear.
# Used after spawning a claude pane to know the SessionStart hooks have flushed.
wait_for_session_start() {
  local repo="$1" timeout="${2:-30}"
  wait_for_jsonl_match "$repo" \
    '.type == "attachment" and .attachment.hookEvent == "SessionStart"' \
    "$timeout" >/dev/null
}

# wait_for_attachment <repo> <hook-event> <body-substring> [timeout] [floor-ts]
# Specialization: polls claude's JSONL for a hook attachment matching
# <hook-event> whose stdout or content body contains <body-substring>.
# Covers SessionStart, UserPromptSubmit, Stop, PreToolUse, PostToolUse —
# any hook that writes an attachment record into the transcript.
#
# Single deterministic surface for "did the hook produce the expected
# content?" — pivots tests away from pane-scrollback grepping (cosmetic,
# subject to TUI rendering quirks and alt-screen replays) toward the
# authoritative JSONL representation that claude actually feeds into its
# model context.
#
# Returns 0 on match (echoes matching JSONL line), 1 on timeout. Default
# timeout: 30s.
#
# Use this instead of inlining the `.attachment.hookEvent` + stdout/content
# union filter; many scenarios need exactly that shape (the prior inlined
# versions in scens 80, 99, and the kafm.6 chain were all variants of the
# same filter that subtly disagreed on which body field to check). This
# helper checks both `.attachment.stdout` AND `.attachment.content` to
# tolerate claude version variance in attachment shape.
#
# Args:
#   repo            agent's repo path (resolved to project dir via encode_cwd)
#   hook-event      e.g. "SessionStart", "UserPromptSubmit", "Stop"
#   body-substring  literal substring to match inside the attachment body
#   timeout         optional poll timeout seconds (default 30)
#   floor-ts        optional RFC3339 floor (suppresses stale matches; see
#                   wait_for_jsonl_match for details)
wait_for_attachment() {
  local repo="$1" hook_event="$2" substring="$3" timeout="${4:-30}" floor_ts="${5:-}"
  local project_dir="$HOME/.claude/projects/$(encode_cwd "$repo")"
  local elapsed=0
  local interval=1
  # Fast-fail floor_ts validation, same shape as wait_for_jsonl_match.
  if [ -n "$floor_ts" ]; then
    floor_ts="${floor_ts%$'\n'}"
    if [[ ! "$floor_ts" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2} ]]; then
      echo "wait_for_attachment: floor_ts '$floor_ts' is not RFC3339-prefixed; refusing to inline into jq filter" >&2
      return 1
    fi
  fi
  # hook_event and substring are passed via jq --arg so embedded
  # double-quotes, backslashes, or other shell metacharacters in
  # caller input cannot break the filter expression. The prior
  # inline-interpolation approach was acknowledged as a footgun
  # in the original review; --arg eliminates the risk entirely.
  # floor_ts is interpolated as a literal because jq cannot use
  # --arg inside a string-comparison position in the same way; the
  # prefix validation above is the safety guarantee.
  local jq_floor_clause=""
  if [ -n "$floor_ts" ]; then
    jq_floor_clause=' and ((.timestamp // "" | tostring) >= "'"$floor_ts"'")'
  fi
  local filter='.type == "attachment"
        and (.attachment.hookEvent == $ev)
        and (((.attachment.stdout // "" | tostring) | contains($sub))
             or ((.attachment.content // "" | tostring) | contains($sub)))'"$jq_floor_clause"
  while [ "$elapsed" -lt "$timeout" ]; do
    if [ -d "$project_dir" ]; then
      local match
      match=$(jq -c --arg ev "$hook_event" --arg sub "$substring" \
        "select($filter)" "$project_dir"/*.jsonl 2>/dev/null | head -n1 || true)
      if [ -n "$match" ]; then
        printf '%s' "$match"
        return 0
      fi
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done
  return 1
}

# wait_for_banner_emit <pane> [timeout-seconds] [since-line-count]
# Polls a tmux pane's scrollback for the daemon's identity banner
# sentinel — `If the prime output was truncated, you must read it now.`
# (identitybanner.PrimeTruncationSentinel) — which is the LAST line of
# the banner printf. Its presence proves the post-launch goroutine's
# emitIdentityBanner has completed sendKeysAndSubmit and all banner
# keystrokes have landed in the pane.
#
# Canonical sync point for scenarios that send subsequent keystrokes
# (slash commands, `!`-bash) after a tmux start/restart and need the
# daemon's async banner emit to finish first. Without syncing on this
# sentinel, the scenario's typed text can splice with the daemon's
# in-flight printf keystrokes (root cause of scen 21's intermittent
# concatenation bug in the v0.10.6 RC1 gate; see commit fa45e6e834).
#
# STICKINESS GUARD: an earlier scenario in the same shared pane (e.g.
# a prior restart in scens 70-76) leaves the sentinel in scrollback;
# a fresh call without anchoring would false-positive-match the stale
# line and return immediately, racing the new banner that is still
# mid-print. The optional `since-line-count` parameter caps how far
# back to look (capture-pane -S -<N>) — caller passes the pane's
# history_size as of BEFORE the new launch/restart, then the helper
# only sees lines emitted AFTER that point.
#
# Capture the pre-emit history_size with:
#   pre_lines=$(tmux display-message -p -t "$pane" '#{history_size}')
# then call:
#   wait_for_banner_emit "$pane" 45 "$pre_lines"
#
# Without `since-line-count` the helper falls back to the legacy 1000-
# line window (backward-compat with existing call sites; safe for
# single-restart scenarios but a stickiness footgun in multi-restart
# sequences).
#
# Returns 0 on match, 1 on timeout. Default timeout: 45s (covers the
# daemon's 10s pre-emit sleep + waitForPaneReady + claude render time).
#
# Args:
#   pane              tmux session name to capture
#   timeout           optional poll timeout seconds (default 45)
#   since-line-count  optional anchor: only check lines newer than the
#                     captured #{history_size} count. Omit for
#                     legacy 1000-line lookback.
wait_for_banner_emit() {
  local pane="$1" timeout="${2:-45}" since_lines="${3:-}"
  local elapsed=0
  while [ "$elapsed" -lt "$timeout" ]; do
    local capture
    if [ -n "$since_lines" ]; then
      # Read only the lines newer than the anchor: current
      # history_size minus the anchor + visible pane rows is the
      # NEW range. We approximate by capturing back to the anchor
      # offset directly: -S -(current-since_lines) lines.
      local now_lines
      now_lines=$(tmux display-message -p -t "$pane" '#{history_size}' 2>/dev/null || echo "$since_lines")
      local delta=$(( now_lines - since_lines ))
      if [ "$delta" -lt 0 ]; then delta=0; fi
      capture=$(tmux capture-pane -t "$pane" -S "-$delta" -p 2>/dev/null || true)
    else
      capture=$(tmux capture-pane -t "$pane" -S -1000 -p 2>/dev/null || true)
    fi
    if printf '%s' "$capture" | grep -qF "If the prime output was truncated, you must read it now."; then
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  return 1
}

# assert_inbox_contains <agent-name> <repo> <marker-substring> [timeout]
# Polls <agent-name>'s inbox via tmux-exec for a message whose
# .body.content contains <marker-substring>. Returns 0 on found, 1 on
# timeout. Default timeout: 30s.
#
# Encapsulates the boilerplate previously duplicated across scens
# 22/23/24/25 (and similar): mktemp -> capture_thrum_json ... inbox ->
# jq for message count -> poll loop. Standardizes:
#   - the `.messages[]?` null-safety against hints-only daemon responses
#     (the under-load failure mode that bit scen 95 in the gate)
#   - jq --arg substring escaping (no manual `"` shell-quoting headaches)
#   - sensible default timeout (matches scens 22/23/24)
#   - tempfile cleanup
#
# Caller is responsible for choosing a sufficiently unique marker
# substring (RUNID-anchored markers like `kafm2-23-filter-${RUNID}` work
# well; bare common words don't).
#
# Args:
#   agent-name        THRUM_NAME pin for the inbox query
#   repo              repo path for tmux-exec --cwd
#   marker-substring  literal substring jq matches inside .body.content
#   timeout           optional poll timeout seconds (default 30)
#
# Note: if the daemon returns a hints-only response (no .messages
# field), the `.messages[]?` null-safety treats it as zero matches —
# the function correctly times out rather than crashing on the missing
# field. If your test specifically needs to verify daemon-side message
# routing under load conditions where the inbox-RPC may fail, prefer
# wait_for_jsonl_match against the recipient's claude JSONL instead;
# see scen 95 for the precedent.
assert_inbox_contains() {
  local agent_name="$1" repo="$2" substring="$3" timeout="${4:-30}"
  # Single mktemp invocation, no .json suffix dance. The prior shape
  # `$(mktemp -t thrum-rel-inbox.XXXXXX).json` created TWO paths: the
  # unsuffixed file mktemp actually created (orphaned), and the
  # .json-suffixed path we assigned to (the one we used). jq doesn't
  # care about extension; the suffix-less form is simpler and leaks
  # zero files.
  local out_file
  out_file="$(mktemp -t thrum-rel-inbox.XXXXXX)"
  local elapsed=0
  local interval=2
  while [ "$elapsed" -lt "$timeout" ]; do
    capture_thrum_json "$repo" "$agent_name" "$out_file" inbox >/dev/null 2>&1 || true
    if [ -s "$out_file" ]; then
      local n
      n=$(jq -r --arg m "$substring" \
        '[.messages[]? | select(.body.content // "" | contains($m))] | length' \
        < "$out_file" 2>/dev/null)
      if [[ "$n" =~ ^[1-9][0-9]*$ ]]; then
        rm -f "$out_file"
        return 0
      fi
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done
  rm -f "$out_file"
  return 1
}

# spawn_sub_fixture_claude <tmux-name> <cwd> [launch-cmd]
# Spawns a non-thrum-managed claude pane in a fresh cwd, handling Claude
# Code's first-time-cwd "trust this folder?" dialog automatically.
#
# Why raw tmux + send-keys instead of `thrum tmux create`: the daemon's
# create RPC enforces a worktree-identity guard ("caller pane belongs to
# a different worktree") and rejects calls that target directories
# outside the calling repo's worktree set. Sub-fixtures live outside any
# thrum worktree by design.
#
# Trust-dialog handling: option 1 ("Yes, I trust this folder") is
# pre-highlighted, so a single Enter once the dialog has rendered
# confirms it. We use wait_for_pane_idle to gate the second Enter rather
# than a fixed sleep — render time varies with shell-init load.
#
# Args:
#   tmux-name   tmux session name (must be unique within the run)
#   cwd         working directory for the new session
#   launch-cmd  command to launch claude (default: "claude"). Override
#               for PATH-stripped variants, e.g. "env PATH=/foo:/bin claude".
spawn_sub_fixture_claude() {
  local tmux_name="$1" cwd="$2" launch_cmd="${3:-claude}"
  tmux new-session -d -s "$tmux_name" -x 500 -y 50 -c "$cwd"
  wait_for_pane_idle "$tmux_name" 10
  tmux send-keys -t "$tmux_name" "$launch_cmd"
  sleep 0.5
  tmux send-keys -t "$tmux_name" Enter
  # Trust dialog renders once shell-claude handshake completes.
  wait_for_pane_idle "$tmux_name" 30
  tmux send-keys -t "$tmux_name" Enter
}

# clear_trust <pane>
# Clears Claude Code's first-time-cwd folder-trust dialog for a DAEMON-launched
# fixture pane (thrum tmux launch). The daemon launches claude but it sits at
# the "Quick safety check: Is this a project you created or one you trust?"
# dialog; the daemon also skips its own post-launch prime inject while the gate
# is up. This drives the pane past the dialog:
#   1. wait for the dialog to render (pane-idle gate, generous timeout),
#   2. content-confirm the trust dialog is actually on screen (defeats the
#      render-timing race — an Enter sent mid-render is swallowed),
#   3. send Enter to confirm "1. Yes, I trust this folder" (the proven
#      spawn_sub_fixture_claude:226 primitive — the ONLY thing that clears it).
#
# We do NOT send /thrum:prime here: the daemon's own post-launch inject
# (HandleLaunch runPostLaunchInject) auto-primes once the gate clears, and a
# manual prime-send RACES it (two keystroke senders collide). We also do NOT
# write .claude/settings.local.json (bypassPermissions) — empirically that adds
# a SECOND modal ("running in Bypass Permissions mode … 1. No, exit / 2. Yes")
# whose default is exit, and the `!`-bash-prefix probes run fine without it.
clear_trust() {
  local pane="$1"
  wait_for_pane_idle "$pane" 30
  local waited=0
  while [ "$waited" -lt 30 ]; do
    if tmux capture-pane -t "$pane" -p 2>/dev/null \
        | grep -qiE "quick safety check|trust this folder|1\. Yes"; then
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done
  if [ "$waited" -ge 30 ]; then
    echo "WARN: clear_trust($pane): trust dialog text not detected within 30s; sending Enter best-effort" >&2
  fi
  tmux send-keys -t "$pane" Enter
}

# capture_thrum_json <repo> <thrum-name> <out-file> <thrum-args... (no --json)>
# Runs a thrum subcommand inside an ephemeral tmux-exec pane (PID-chain
# break, identity pinned via THRUM_NAME) and writes the --json output to
# <out-file> on the host filesystem. Caller then runs jq against
# <out-file> directly — no `tmux capture-pane | jq` involved, sidestepping
# the 80-col wrap that mangles JSON strings (memory:
# tmux-capture-pane-json-wrap).
#
# CONTRACT: callers must NOT include `--json` in <thrum-args>. The helper
# appends it. Callers that need a non-JSON capture should call tmux-exec
# directly.
#
# Args:
#   repo         working directory for tmux-exec --cwd (typically $COORD_REPO
#                or a sub-fixture root)
#   thrum-name   value for THRUM_NAME inside the ephemeral pane
#   out-file     host-side file path to write JSON to (use a $RUNID-anchored
#                /tmp path; the helper does not handle paths containing
#                single quotes)
#   thrum-args   thrum subcommand + flags, NO `--json` (helper appends)
#
# Exit code: whatever tmux-exec returned. The helper does NOT verify the
# file is parseable JSON — caller jq's it.
#
# Quoting note: thrum-args are marshalled via printf %q so embedded spaces,
# quotes, and shell metacharacters survive the bash -c hop intact.
capture_thrum_json() {
  local repo="$1" thrum_name="$2" out_file="$3"
  shift 3
  local te="${THRUM_RELEASE_REPO_ROOT}/scripts/tmux-exec"
  local args_quoted=""
  local a
  for a in "$@"; do
    args_quoted+=" $(printf %q "$a")"
  done
  "$te" exec --cwd "$repo" --clean -- bash -c \
    "env THRUM_NAME=$(printf %q "$thrum_name") thrum${args_quoted} --json > $(printf %q "$out_file") 2>/dev/null"
}

# assert_tool_use_bash <repo> <sid> <name> <floor_ts> <command-substring> [timeout]
# Polls the agent's JSONL for an assistant entry whose .message.content[]
# contains a tool_use with .name == "Bash" and .input.command containing
# <command-substring>, constrained to entries with .timestamp >= <floor_ts>.
# Emits PASS / FAIL via emit_pass / emit_fail.
#
# Specialization for the "natural-language → claude shells out via Bash"
# assertion shape used by NL-driven scenarios (precedent: scenario 21's
# slash-chain sub-assertion 2). The tool_use entry is the deterministic
# anchor: regardless of what the model says in chat, if it actually
# invoked the requested command, the tool_use lands.
#
# Argument shape mirrors assert_jsonl (<repo> <sid> <name> ... <secs>) with
# command-substring substituted for assert_jsonl's expected-line-prefix.
# Default timeout: 60s.
#
# floor_ts is REQUIRED (not optional) because without it the helper would
# false-match earlier tool_use entries from setup-repo.sh's whoami probes
# or prior scenarios. Generate immediately before sending the NL prompt:
#
#   local floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"
#   send_command "$pane" "ask claude something here"
#   assert_tool_use_bash "$repo" "$SID" "name" "$floor_ts" "thrum send" 90 \
#     "scenarios/${SID}.test.sh:$LINENO"
#
# Args:
#   repo                agent's repo path (used to locate JSONL via encode_cwd)
#   sid                 scenario id, for output tagging
#   name                assertion name, for output tagging
#   floor_ts            RFC3339 cutoff; only entries at-or-after match
#   command-substring   substring jq finds inside .input.command (use
#                       distinctive tokens like "thrum send" / "thrum reply")
#   timeout             optional poll timeout seconds (default 60)
#   loc                 optional "scenarios/NN.test.sh:LINENO" for failure attribution
#                       (positional after timeout, mirroring assert_jsonl)
#
# Returns 0 on PASS, 1 on FAIL.
assert_tool_use_bash() {
  local repo="$1" sid="$2" name="$3" floor_ts="$4" substring="$5"
  local timeout="${6:-60}" loc="${7:-unknown}"
  local filter
  filter='.type == "assistant"
        and (.timestamp >= "'"$floor_ts"'")
        and (.message.content | type == "array")
        and (.message.content
             | map(select(.type == "tool_use"
                          and .name == "Bash"
                          and (.input.command | tostring | contains("'"$substring"'"))))
             | length > 0)'
  if wait_for_jsonl_match "$repo" "$filter" "$timeout" >/dev/null; then
    emit_pass "$sid" "$name"
    return 0
  fi
  emit_fail "$sid" "$name" \
    "assistant tool_use Bash with .input.command containing '${substring}' within ${timeout}s after ${floor_ts}" \
    "(no matching JSONL entry)" \
    "$loc"
  return 1
}

# kick_session_then_wait <pane> <cwd> [timeout-seconds]
# Forces a fresh claude pane to flush its first JSONL (creating the
# project dir) so subsequent assertion helpers don't ERROR with "no
# project dir at …".
#
# Background: claude writes ZERO JSONL until first user input — the
# welcome screen is keystrokes-only. check-context-value.sh's existence
# check on PROJECT_DIR fails fast in that window, masking the actual
# "we haven't asked claude anything yet" condition behind a confusing
# ERROR line. Send a no-op `! true` (which still triggers SessionStart
# + flushes JSONL), then wait for the SessionStart attachment so we
# know the project dir is on disk.
#
# Default timeout: 60s.
kick_session_then_wait() {
  local pane="$1" cwd="$2" timeout="${3:-60}"
  send_command "$pane" "! true"
  wait_for_session_start "$cwd" "$timeout"
}
