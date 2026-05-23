#!/usr/bin/env bash
# Scenario: tmux-start-banner (migrates thrum-6hqy AC #1 + #2)
#
# Pins the daemon-side identity banner emitted at `thrum tmux start` /
# `thrum tmux launch` time. Two assertions against the COORD pane that
# setup-repo.sh's `thrum tmux start --name coord` produced:
#
#   1. Banner present — pane scrollback contains "Agent: @<agent_id>"
#      and other banner lines (Role/Worktree/Branch). The fixture
#      coord agent registers as test_coordinator_main with role
#      coordinator. Captured via `tmux capture-pane -S -1000` so the
#      banner is reachable even after claude takes over the visible
#      screen — banner lands at the shell prompt before the runtime
#      launches and stays in scrollback.
#
#   2. /thrum:prime NOT sent post-launch for claude (has SessionStart
#      hook). Pre-thrum-6hqy the daemon's HandleLaunch goroutine sent
#      "/thrum:prime" + Enter 10s after launchCmd; xupf+2qe2 made that
#      redundant since inject-prime-context.sh auto-injects the
#      briefing. The fix gates the prime-send on
#      runtimeHasSessionStartHook(runtime) — claude's preset has the
#      flag set, so the goroutine should skip the send. Assertion: the
#      coord agent's JSONL has zero <command-name>/thrum:prime
#      </command-name> entries (the slash-routing tag claude emits when
#      it processes a slash command — the same anchor scenarios
#      54/55/57 use for routing assertions).
#
# Read-only against the run-level fixture's coord pane. No fixture
# mutation — both assertions are post-hoc reads of the existing setup.

SID="99-tmux-start-banner"
PANE="$COORD_PANE"
REPO="$COORD_REPO"

# Assertion 1: banner header present in pane scrollback. Per
# thrum-6hqy.1 the daemon emits the banner via a goroutine 10s AFTER
# launchCmd, so by the time scenario 99 runs (well after setup-repo's
# `thrum tmux start --name coord` + the 10s sleep) the banner is
# safely in scrollback — but possibly hundreds of lines back, since
# claude's TUI redraws + setup-repo's whoami probe + downstream
# scenarios accumulate output continuously after the banner fires.
# Capture all available history (`-S -`) so the banner remains
# reachable regardless of tmux's history-limit.
# Assertion surface: the SessionStart hook attachment in claude's
# JSONL (~/.claude/projects/<encoded-cwd>/*.jsonl). Per Leon's
# clarification + diagnostic in the v0.10.6 RC1 triage (2026-05-23):
# the hook is the authoritative source for identity-in-context for
# hook-aware runtimes. Pane scrollback is NOT a valid surface for
# this assertion since thrum-6hqy.1 (commit 93e98646fd, 28 Apr 2026):
#   - The daemon's emitIdentityBanner now sends its printf into
#     claude's running input prompt (post-launch goroutine path),
#     rendering inside claude's TUI response area with indentation.
#     Column-0 anchoring is impossible.
#   - On the INITIAL-LAUNCH path specifically, the banner emit
#     skips entirely because findIdentityForSession can't match
#     before writeTmuxToIdentity has populated the identity file's
#     TmuxSession field. Banner content never reaches the pane at
#     all (separate daemon-side issue tracked elsewhere — cosmetic,
#     not blocking; the hook covers identity-in-context).
#
# What we check: the hook's stdout contains the canonical identity
# markers `# 🎯 You are: @<name>` (markdown header rendered by
# claude-plugin/scripts/thrum-startup.sh) and `- **Role:** <role>`.
# These reach claude's model context via the SessionStart hook
# attachment regardless of what the daemon's cosmetic banner does.
# wait_for_jsonl_match handles the project dir resolution and
# multi-file rotation (encode_cwd + *.jsonl glob).
banner_agent_filter='.type == "attachment"
        and (.attachment.hookEvent == "SessionStart")
        and (((.attachment.stdout // "" | tostring) | contains("# 🎯 You are: @test_coordinator_main"))
             or ((.attachment.content // "" | tostring) | contains("# 🎯 You are: @test_coordinator_main")))'
if wait_for_jsonl_match "$REPO" "$banner_agent_filter" 30 >/dev/null; then
  emit_pass "$SID" "banner-agent-line"
else
  emit_fail "$SID" "banner-agent-line" \
    "SessionStart attachment in coord JSONL contains '# 🎯 You are: @test_coordinator_main'" \
    "(no matching attachment within 30s — thrum-startup.sh hook may not have rendered the identity header)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Role line — second-highest anchor. Fixture coord registers as
# role=coordinator; the hook renders this verbatim as
# `- **Role:** coordinator` in the SessionStart attachment body.
banner_role_filter='.type == "attachment"
        and (.attachment.hookEvent == "SessionStart")
        and (((.attachment.stdout // "" | tostring) | contains("- **Role:** coordinator"))
             or ((.attachment.content // "" | tostring) | contains("- **Role:** coordinator")))'
if wait_for_jsonl_match "$REPO" "$banner_role_filter" 30 >/dev/null; then
  emit_pass "$SID" "banner-role-line"
else
  emit_fail "$SID" "banner-role-line" \
    "SessionStart attachment in coord JSONL contains '- **Role:** coordinator'" \
    "(no matching attachment within 30s)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: claude (has SessionStart hook) should NOT have received
# a post-launch /thrum:prime send during the launch window. The daemon's
# HandleLaunch goroutine fires `time.Sleep(10s)` after launchCmd, so a
# regression that re-introduced the prime-send would show up as a
# `<command-name>/thrum:prime</command-name>` tag in coord's JSONL
# within the first ~30s of the session.
#
# Scoping is required: scenario 54 explicitly sends /thrum:prime to the
# COORD pane via send_slash_command, producing a tag in COORD JSONL,
# AND scenario 54 may run before scenario 99 in a full-suite run
# (alphabetic sort puts "54-" before "99-"). An unscoped grep would
# always be ≥1 in that ordering. Filter to entries within
# (session_start, session_start + LAUNCH_WINDOW_SEC). Anything outside
# that window is from a deliberate later /thrum:prime send by another
# scenario and irrelevant to thrum-6hqy.
LAUNCH_WINDOW_SEC=120
project_dir="$HOME/.claude/projects/$(encode_cwd "$REPO")"
launch_window_hits=0
if [ -d "$project_dir" ] && compgen -G "$project_dir/*.jsonl" >/dev/null; then
  # Single slurp-mode jq pass over every JSONL file in the project
  # dir: derive session_start as the earliest .timestamp seen, then
  # count user messages within session_start + LAUNCH_WINDOW_SEC
  # whose content contains the /thrum:prime tag. Robust to claude's
  # multiple-file session rotation.
  #
  # Millisecond strip: claude writes RFC3339 timestamps with
  # millisecond precision (e.g. `2026-05-23T09:31:12.227Z`). jq's
  # `fromdateiso8601` only accepts `%Y-%m-%dT%H:%M:%SZ` and errors
  # silently on the millisecond form — the filter then returns
  # empty stdout, which combined with the bash sanitization bug
  # below produced spurious failures with empty `got:` value.
  # `sub("\\.[0-9]+Z?$"; "Z")` strips the fractional seconds before
  # parsing.
  launch_window_hits=$(jq -s --argjson win "$LAUNCH_WINDOW_SEC" '
    def parse_ts: sub("\\.[0-9]+Z?$"; "Z") | fromdateiso8601;
    ([.[] | select(.timestamp != null) | .timestamp] | min) as $start
    | if $start == null then 0
      else
        ($start | parse_ts) as $start_epoch
        | [.[]
            | select(.type == "user"
                     and (.message.content | type == "string")
                     and (.message.content | contains("<command-name>/thrum:prime</command-name>"))
                     and (.timestamp != null))
            | select(((.timestamp | parse_ts) - $start_epoch) < $win)]
        | length
      end' \
    "$project_dir"/*.jsonl 2>/dev/null)
fi
# Sanitize: jq errors / empty output / non-numeric -> 0. The prior
# `case "${launch_window_hits:-0}"` only affected the case INPUT
# expansion via `:-0`, not the variable itself, so an empty value
# survived to the comparison below — failing emit_fail with an
# empty `got:` placeholder. This guard actually assigns 0 when the
# variable isn't a clean integer.
if [[ ! "$launch_window_hits" =~ ^[0-9]+$ ]]; then
  launch_window_hits=0
fi

if [ "$launch_window_hits" = "0" ]; then
  emit_pass "$SID" "no-post-launch-prime-for-claude"
else
  emit_fail "$SID" "no-post-launch-prime-for-claude" \
    "0 occurrences of <command-name>/thrum:prime</command-name> in coord JSONL within ${LAUNCH_WINDOW_SEC}s of session start (claude has SessionStart hook — daemon should skip the post-launch prime send)" \
    "got: ${launch_window_hits} occurrences inside the launch window" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
