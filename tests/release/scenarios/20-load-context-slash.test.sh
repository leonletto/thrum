#!/usr/bin/env bash
# Scenario: load-context-slash (migrates full_test_plan.md § 9.4)
#
# Verifies that typing the /thrum:load-context slash command into a
# claude pane causes claude to invoke `thrum prime` via its Bash tool —
# the literal contract spelled out by the skill body
# (claude-plugin/commands/load-context.md). When that round trip
# completes, the agent is reprimed from saved project + session
# context, restoring the work it was doing pre-compaction.
#
# Why it matters: /thrum:load-context is the primary recovery path
# after auto-compaction. A regression here (slash command not
# recognized, skill body changed and no longer routes to thrum prime,
# Bash tool invocation suppressed) would silently break compaction
# recovery — agents would compact and not re-load their state.
#
# Test approach: send the slash command (no `!`-prefix — it's a Claude
# Code slash command, not a bash command) via send_command's else
# branch. Then poll JSONL for an assistant tool_use entry where
# .name=="Bash" and .input.command starts with "thrum prime". That's
# the deterministic, code-path-anchored evidence the skill ran.
#
# Deviation from markdown § 9.4: the spec captures the pane and greps
# free-form claude prose for "thrum context" / "/tmp backup" markers.
# We assert against the underlying tool invocation instead, mirroring
# scenario 05's whoami --json deviation rationale: deterministic and
# grep-friendly vs claude's free-form output, and code-path-anchored
# vs surface-prose-anchored.
#
# Driven against COORD pane (matches markdown). Read-only at the
# fixture level — claude reading its own context doesn't mutate it.

SID="20-load-context-slash"
PANE="$COORD_PANE"
REPO="$COORD_REPO"

# Settle the coord pane. COORD's prime output is large + scenario 17
# left chat content in flight; allow up to 90s.
wait_for_pane_idle "$PANE" 90

# Capture an RFC3339 floor timestamp so we only match NEW assistant
# entries — without this we'd match the original /thrum:prime call
# fired by setup-repo.sh's tmux start. JSONL timestamps are RFC3339Z;
# our floor (no fractional seconds, no Z) sorts lexicographically
# before anything that includes a fractional/Z suffix at the same
# clock-second, which is the desired behavior for the comparison.
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

# Send the slash command in two pieces: leading `/` first, then the
# rest of the command string. Same rationale as drive.sh's `!`-prefix
# split — Claude Code's UI treats `/` as a mode switch (command
# palette) at keystroke-time. Bundling `/thrum:load-context` in a
# single tmux send-keys call sometimes lets the rest of the text race
# the mode switch, causing claude to receive the command as plain
# chat instead of registering it as a slash invocation. The two-step
# pattern with a brief settle is what works reliably in scenario
# 03's auto-prime path; mirroring it here removes the flake.
tmux send-keys -t "$PANE" "/"
sleep 0.5
tmux send-keys -t "$PANE" "thrum:load-context"
sleep 0.8
tmux send-keys -t "$PANE" Enter

# Allow claude time to read the skill body and dispatch the Bash tool
# call. Skill bodies are short; tool dispatch is fast; the bash command
# itself (thrum prime) takes a few seconds to render. 90s ceiling
# matches the rest of the suite's claude-mediated polls.
filter='.type == "assistant"
        and (.timestamp >= "'"$floor_ts"'")
        and (.message.content | type == "array")
        and (.message.content
             | map(select(.type == "tool_use"
                          and .name == "Bash"
                          and (.input.command | tostring | startswith("thrum prime"))))
             | length > 0)'

if wait_for_jsonl_match "$REPO" "$filter" 90 >/dev/null; then
  emit_pass "$SID" "skill-invokes-thrum-prime"
else
  emit_fail "$SID" "skill-invokes-thrum-prime" \
    'assistant tool_use Bash call with command starting "thrum prime" within 90s' \
    "(no matching JSONL entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
