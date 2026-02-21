# Anti-Patterns

Common mistakes when using Thrum and how to avoid them.

## 1. Using Thrum for Task Management

**Wrong:** Sending messages like "TODO: implement auth" to yourself. **Right:**
Use Beads (`bd create`) for task tracking. Use Thrum for coordination messages
between agents.

## 2. Polling Instead of Waiting

**Wrong:** Looping `thrum inbox` every 10 seconds. **Right:** Use `thrum wait`
which blocks efficiently until a message arrives. Or use the message-listener
sub-agent pattern for background monitoring.

## 3. Forgetting to Re-arm the Listener

**Wrong:** Processing messages from listener, then continuing work without
re-arming. **Right:** After processing listener results, always spawn a new
message-listener:

```
Task(subagent_type="message-listener", run_in_background=true, prompt="...")
```

## 4. Broadcasting Without the @everyone Group

All three forms are equivalent — use whichever reads best in context:

```bash
thrum send "msg" --broadcast
thrum send "msg" --to @everyone
thrum send "msg" --everyone
```

The `@everyone` group is auto-created and handles membership dynamically.

## 5. Skipping Registration

**Wrong:** Sending messages without running `thrum quickstart` first. **Right:**
Always register at session start. Without registration, messages won't be routed
correctly and `thrum inbox` won't know who you are.

## 6. Vague Intents

**Wrong:** `thrum quickstart --intent "Working on stuff"` **Right:**
`thrum quickstart --intent "Implementing JWT auth for login endpoint (bd-123)"`

Specific intents help other agents understand what you're doing via
`thrum team`.

## 7. Leaving Sessions Open

**Wrong:** Finishing work but not ending the session. **Right:** Run
`thrum session end` when done. Stale sessions make `thrum team` unreliable.

## 8. Reading Files Instead of Using CLI

**Wrong:** Reading `.git/thrum-sync/` files directly with the Read tool.
**Right:** Use `thrum inbox`, `thrum status`, `thrum prime`. The SKILL.md
`allowed-tools` is `Bash(thrum:*)` — no Read permission needed.

## 9. Sending Messages to Yourself

**Wrong:** `thrum send "note to self" --to @me` **Right:** Use Beads notes
(`bd update <id> --notes "..."`) for self-notes. Thrum is for inter-agent
communication.

## 10. Spamming Status Updates

**Wrong:** Sending a message after every line of code. **Right:** Batch updates
at natural breakpoints — after completing a subtask, hitting a blocker, or
finishing the main task.

## 11. Not Including Context in Messages

**Wrong:** `thrum send "done" --to @lead` **Right:**
`thrum send "Completed bd-123: JWT auth with tests passing. 3 files changed." --to @lead`

Include Beads IDs, file paths, commit hashes — anything that helps the recipient
act on the message.

## 12. Using @role to Address One Agent

**Wrong:** `thrum send "msg" --to @implementer` when you mean a specific agent.
**Right:** `thrum send "msg" --to @alice` — use the agent's name.

`@role` sends to **all agents** with that role (via the auto-created role group)
and emits a warning. Use `@name` for direct messages. Check names with
`thrum team`.

## 13. Sending to Unknown Recipients

**Wrong:** `thrum send "msg" --to @typo` — hard error if recipient doesn't
exist. **Right:** Verify the agent name with `thrum team` first, then send.

## 14. Agent Name Same as Role

**Wrong:** `thrum quickstart --name coordinator --role coordinator` —
registration rejects name==role. **Right:** Use a descriptive name that differs
from the role, e.g., `--name lead-agent --role coordinator`.
