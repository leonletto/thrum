## Troubleshooting Identity Issues

### How to use this page

Paste your error string into your browser's Find (Cmd-F / Ctrl-F). Jump to the
matching section. Each section starts with the verbatim error text, explains
what the daemon was protecting against, and gives the fix.

If you've never seen identity guards before, start with the
[Identity Guards](identity.md#identity-guards) section in the main identity doc
for the conceptual model. This page is purely operational — cause and fix for
each error, nothing more.

---

### `cross_worktree` — pid_mismatch

#### Error

```text
identity guard "cross_worktree" fired: pid_mismatch
  expected agent: furiosa
  detected agent: coord_main
  expected pid: 98412
  caller pid: 12050
  caller cwd: /Users/leon/.workspaces/thrum/coord
  remediation: cd to the correct worktree or run 'thrum prime' to re-claim
```

#### What happened

This is the most common guard. Before the RPC went out, Thrum walked your
calling process's ancestor PID chain looking for the agent PID recorded in the
identity file. The PID wasn't there. Your shell is inside one worktree but the
identity it resolved belongs to a different one — the two names in the error
message are the expected owner versus the agent your CWD actually belongs to.

This used to be a silent misattribution: the send or write went through under
the wrong agent's name. Now it's a loud error you can act on.

#### Fix

1. Check which worktree you're in: `pwd` and compare to the `caller cwd` in the
   error.
2. `cd` to the correct worktree for the agent you meant to act as.
3. Retry the command.

If you genuinely want to act as the agent in your current worktree (not the one
in `expected agent`), run `thrum prime` from your current directory to
re-register identity here.

If you're running a script that `cd`s between worktrees mid-session, set
`THRUM_HOME` to pin the repo path:

```bash
export THRUM_HOME=/path/to/your/repo
```

---

### `unauthenticated_rpc` — identity_mismatch (peercred path)

#### Error

```text
identity guard "unauthenticated_rpc" fired: identity_mismatch
  expected agent: furiosa
  detected agent: coord_main
  remediation: your current directory is inside "coord_main"'s worktree. cd into "furiosa"'s worktree and retry, or drop the identity claim to act as "coord_main".
```

#### What happened

The daemon connected to your process via `SO_PEERCRED` (on Linux) or
`LOCAL_PEERPID` (on macOS) and got a kernel-verified identity. That identity
doesn't match the `CallerAgentID` your client sent in the RPC frame. This is a
forgery detection — the daemon refuses to trust a client-supplied claim that
contradicts what the kernel says.

Most of the time this isn't intentional forgery. It's a stale credential: a tool
or wrapper sent an old `CallerAgentID` from a previous session.

#### Fix

1. If you didn't set `CallerAgentID` manually, the offending tool is sending
   stale credentials. Restart it.
2. If you're writing a client, stop sending a hardcoded `CallerAgentID`. Let the
   daemon resolve identity from peercred.
3. To act as `coord_main` (the agent your CWD actually belongs to), drop the
   explicit identity claim entirely and retry from your current directory.

**Config mode doesn't help.** Setting `unauthenticated_rpc` to `warn` or `off`
has no effect on `identity_mismatch` — forgery rejection is foundational. See
[Guard mode downgrade](#guard-mode-downgrade-incident-diagnosis) for what you
can and can't configure.

**Narrow exception — shared-worktree claim trust.** If the worktree hosts
multiple registered agents (e.g. Playwright E2E harness, multi-agent test
scenarios), and your claimed agent is also registered in the peercred-resolved
worktree, the daemon trusts the claim and skips the mismatch. You won't see this
error in that case. It only fires when peercred puts you in worktree A and the
claimed agent lives in worktree B — that's still forgery.

---

### `unauthenticated_rpc` — identity_mismatch (ancestor-chain path)

#### Error

```text
identity guard "unauthenticated_rpc" fired: identity_mismatch
  expected agent: furiosa
  detected agent: coord_main
  remediation: your process ancestor chain belongs to "coord_main". retry from "coord_main"'s worktree or tmux pane, or drop the identity claim to act as "coord_main".
```

#### What happened

CWD-based peercred didn't match any registered worktree, so the daemon walked
your process's ancestor chain instead. It found a registered agent PID in that
chain — but it was `coord_main`, not the `furiosa` your client claimed. Same
outcome as the peercred path: kernel-level evidence contradicts the claim.

This usually happens when you've changed directory into another worktree during
a session and then issued a thrum command without re-priming.

#### Fix

1. `cd` back to the worktree you intended to act from and retry.
2. Or drop the `CallerAgentID` claim and let the daemon use `coord_main` — the
   agent your ancestor chain actually belongs to.

**Config mode doesn't help here either.** Note that the shared-worktree claim
trust from the previous section does NOT apply to the ancestor-chain path — it
only fires when peercred CWD didn't match a registered worktree at all, so
there's no worktree to check the claim against.

---

### `unauthenticated_rpc` — anonymous_mutating_rpc

#### Error

```text
identity guard "unauthenticated_rpc" fired: anonymous_mutating_rpc
  remediation: cd into a registered agent worktree and retry
```

#### What happened

Peercred ran and resolved your PID. Your CWD didn't map to any registered
agent's worktree, and the ancestor-chain walk also came up empty. You're calling
a mutating RPC from outside any known agent context — typically from `~` or a
directory that has no `.thrum/identities/` associated with it.

#### Fix

1. `cd` into a worktree that has a registered agent:

   ```bash
   cd /path/to/your/repo
   thrum quickstart --name myagent --role implementer --module mymodule
   ```

2. Retry your original command.

If you're starting fresh in a new repo, run `thrum init` first to initialize the
`.thrum/` directory, then `thrum quickstart` to register.

---

### `unauthenticated_rpc` — no_caller_agent_id

#### Error

```text
identity guard "unauthenticated_rpc" fired: no_caller_agent_id
  remediation: run 'thrum quickstart' to register an identity; CLI callers must forward CallerAgentID on every RPC
```

#### What happened

You're on a non-peercred transport — typically a WebSocket client or a
browser-based tool — and the RPC payload didn't include `CallerAgentID`.
Peercred isn't available on these transports, so G3 falls back to trusting the
`CallerAgentID` field. Without it, there's nothing to trust.

#### Fix

1. If you're using the CLI, this shouldn't happen. Run `thrum quickstart` in the
   current directory to register an agent and retry:

   ```bash
   thrum quickstart --name myagent --role implementer --module mymodule
   ```

2. If you're a client developer sending RPCs over WebSocket, include a non-empty
   `CallerAgentID` in every mutating RPC frame.

---

### `non_git_bootstrap` — not_a_git_repo

#### Error

```text
identity guard "non_git_bootstrap" fired: not_a_git_repo
  caller cwd: /Users/leon
  remediation: run from a git-anchored directory, or pass --force for ephemeral non-anchored use
```

#### What happened

You ran `thrum daemon start` or `thrum init` from a directory with no `.git`
ancestor. Thrum walks up from your CWD looking for `.git` — if it doesn't find
one, G2 refuses to bootstrap. Identity files derive their repo scope and
supervisor slugs from git state. Without a git root, those fields are
meaningless.

Before v0.9.0, this silently created a `.thrum/` with nonsense values in `~` or
wherever you happened to be. Now it fails loudly.

#### Fix

1. `cd` to your git repo first, then run the command:

   ```bash
   cd /path/to/your/repo
   thrum daemon start
   ```

2. If you genuinely need a non-git bootstrap (ephemeral testing, one-off
   tooling), pass `--force`:

   ```bash
   thrum daemon start --force
   ```

   This is rare and usually a mistake. Prefer using a git-anchored directory.

---

### `daemon_writer_liveness` — subject_pid_dead

#### Error

```text
identity guard "daemon_writer_liveness" fired: subject_pid_dead
  expected pid: 98412
  remediation: daemon refusing to write to dead agent's identity file
```

#### What happened

The daemon was about to write to an identity file on behalf of an agent whose
PID is no longer running. G4 blocks the write. This prevents ghost state: a
crashed agent's stale RPC shouldn't be able to update the identity file after
the agent has already exited.

In normal operation this auto-heals: on the next daemon boot the dead-PID
auto-reclaim path fires and cleans up the stale file. You'll see this in logs
after a crashed runtime restarts.

#### Fix

If it persists after a daemon restart:

```bash
thrum daemon restart
```

If the stale file is blocking a fresh registration and the daemon restart didn't
clear it, manually retire the file:

```bash
mv .thrum/identities/<agentname>.json .thrum/identities/<agentname>.json.deleted
```

Then run `thrum quickstart` again to register fresh. The `.deleted` file is left
on disk but ignored by all guard scans.

---

### `prime_ownership` — caller_not_topmost_runtime

#### Error

```text
identity guard "prime_ownership" fired: caller_not_topmost_runtime
  expected pid: 20981
  caller pid: 21049
  remediation: you appear to be running inside a sub-agent; the parent runtime owns this identity — run prime from the top-level runtime instead
```

#### What happened

You called `thrum prime` from inside a sub-agent — a tool call running under a
Claude Code session that already has a different runtime as the outermost
process. G5 checks whether your closest runtime ancestor matches the PID
recorded in the identity file. If it doesn't, the guard refuses because the
sub-agent doesn't own this identity.

The typical scenario: a coordinator's tool call tries to run `thrum prime` to
orient itself. The coordinator's parent Claude process owns the identity. The
tool call's process tree is a descendant, not the owner.

#### Fix

Run `thrum prime` from the top-level runtime — the actual `claude` or `codex`
process that owns the session — not from a tool call inside it.

If you're building automation that needs to prime inside a sub-agent, use
`--force` to bypass G5:

```bash
thrum prime --force
```

Use `--force` only when you're sure the sub-agent is intentionally taking over
the session, not just checking context mid-task.

---

### `quickstart_self_rename` — caller_already_owns_identity

#### Error

```text
identity guard "quickstart_self_rename" fired: caller_already_owns_identity
  expected agent: furiosa
  caller pid: 12050
  remediation: use --force to rename the existing identity to .deleted and register fresh
```

#### What happened

You called `thrum quickstart` but your ancestor PID chain already owns an
identity file in this directory — registered under the name `furiosa`. G1a
refuses to silently overwrite or re-register under a new name because that would
abandon the existing identity without any record.

This often happens when an orchestrator calls `thrum quickstart` multiple times
with different `--name` values, rotating agent names.

#### Fix

If you want to rename (replace the old identity with a fresh one):

```bash
thrum quickstart --force --name <newname> --role <role> --module <module>
```

`--force` renames the old identity file to `.deleted` first, then registers
fresh. The old file is retained on disk as a `.deleted` sidekick in case you
need to inspect it.

If you want to keep the existing identity and just re-prime, run:

```bash
thrum prime
```

---

### `quickstart_name_collision` — name_held_by_live_foreign_pid

#### Error

```text
identity guard "quickstart_name_collision" fired: name_held_by_live_foreign_pid
  expected agent: nux
  expected pid: 33812
  caller pid: 34001
  remediation: choose another --name or pass --force to displace the existing identity
```

#### What happened

The name you passed to `--name` is already registered to a different process
that's still alive. G1b refuses because overwriting a live agent's identity file
is a data-integrity problem — that other agent is still running and owns the
file.

Before v0.9.0 this was a silent overwrite. Now it fails so you can decide what
to do.

#### Fix

**Option A — wait.** If the other process exits on its own, the next
`thrum quickstart` attempt will auto-reclaim the name. Dead agent files are
reclaimable without `--force`.

**Option B — pick a different name:**

```bash
thrum quickstart --name <differentname> --role <role> --module <module>
```

**Option C — force-displace** (only if you're certain the other process is hung
and won't recover):

```bash
thrum quickstart --force --name nux --role <role> --module <module>
```

`--force` renames the existing file to `.deleted` and takes the name. If the
other process was still alive and recovers after this, it'll get a
`cross_worktree` error on its next RPC — it will need to re-register.

---

## Guard mode downgrade (incident diagnosis)

Each guard is independently configurable in `.thrum/config.json` under the
`identity_guard` block. Three modes are available:

| Mode     | Behavior                                                                                      |
| -------- | --------------------------------------------------------------------------------------------- |
| `strict` | Guard fires and the RPC or command fails. This is the default for all guards.                 |
| `warn`   | Guard fires, logs a structured `identity_guard_fire` event, and allows the operation through. |
| `off`    | Guard is skipped entirely.                                                                    |

Example — downgrade `cross_worktree` to `warn` for incident diagnosis:

```json
{
  "identity_guard": {
    "cross_worktree": "warn",
    "non_git_bootstrap": "strict"
  }
}
```

Any guard you don't list keeps the strict default. The full set of config keys:

```json
{
  "identity_guard": {
    "cross_worktree": "strict",
    "dead_pid_auto_reclaim": "strict",
    "quickstart_self_rename": "strict",
    "quickstart_name_collision": "strict",
    "non_git_bootstrap": "strict",
    "unauthenticated_rpc": "strict",
    "daemon_writer_liveness": "strict",
    "prime_ownership": "strict"
  }
}
```

For runtime overrides without a repo commit, write to
`.thrum/var/guard-daemon.json` instead. The daemon checks this file every cycle
and merges it on top of the repo config. This lets you temporarily loosen a
guard on a running daemon without touching the committed config.

**`unauthenticated_rpc` / `identity_mismatch` cannot be downgraded via config.**
The forgery rejection — where a client-asserted `CallerAgentID` contradicts the
kernel-verified identity — ignores the `unauthenticated_rpc` mode setting. This
is a deliberate security decision. Disabling it would re-open the v0.8.x
impersonation hole where any caller could claim any agent's identity. Every
other guard is configurable; this one isn't.

The one runtime exception (not a config knob) is the **shared-worktree claim
trust** described under the
[peercred path](#unauthenticated_rpc--identity_mismatch-peercred-path) above: a
claim for an agent co-located in the peercred-resolved worktree is honored
because it's kernel-verified to belong to the same worktree. This is narrower
than a mode downgrade — it applies only when `state.IsAgentInWorktree` confirms
the claim.

---

## auto_reclaim: dead_pid_auto_reclaim (informational)

Not an error. When `thrum quickstart` or the cross_worktree check encounters a
name or identity file registered to a PID that is no longer alive, it
auto-reclaims: overwrites the stale file with the new caller's registration and
logs a `warn`-level `identity_guard_fire` event with `outcome=auto_reclaimed`.

You'll see this in daemon logs after a crashed runtime restarts and re-registers
in the same worktree. The reclaim is silent on success in strict mode; in warn
mode it logs and then proceeds.

If you want to monitor for reclaim events in your logging pipeline, bind on:

```text
identity_guard_fire where guard=dead_pid_auto_reclaim and outcome=auto_reclaimed
```

The `dead_pid_auto_reclaim` guard mode in config controls the reclaim path
independently from the main `cross_worktree` check. Setting it to `off` disables
auto-reclaim, which means dead-PID mismatches always fall through to the
`cross_worktree` strict/warn handling instead of self-healing. Leave it at
`strict` (the default) unless you have a specific reason not to.

---

## See also

- [Identity](identity.md) — conceptual model for identity files, PID resolution,
  and what identity guards are
- [Configuration](configuration.md) — full `identity_guard` config block schema
  and the daemon-override precedence order
