---
title: "Local Security Model"
description:
  "How Thrum's daemon decides who's calling — kernel-verified peer credentials,
  anonymous-caller read-only allowlist, author-only message operations, and
  WebSocket origin restriction. Covers the v0.9.0 sec.1–sec.8 hardening."
category: "reference"
order: 4
tags: ["security", "peercred", "trust-model", "daemon", "rpc"]
last_updated: "2026-04-24"
---

## Local Security Model

### Overview

Before v0.9.0, the daemon trusted the `caller_agent_id` field in every RPC
request at face value. Your client said "I'm agent X" and the daemon believed
it. Any process on your machine that could reach the unix socket — and the
socket was `0600`, so that meant any process you owned — could impersonate any
registered agent, delete anyone's messages, or bulk-wipe entire scopes.

Concretely: a script running from `/tmp` could pass
`"caller_agent_id": "my-coordinator"` in a JSON-RPC payload and call
`message.deleteByScope("project:main")`, erasing the coordinator's entire
message history. No identity check. No ownership requirement. Just a raw socket
write.

v0.9.0 replaces that with a three-layer trust stack:

1. **Socket permissions** — the unix socket is `0600`. Only the user who started
   the daemon can connect at all.
2. **Kernel peer credentials** — on every accepted connection the daemon reads
   the connecting process's PID from the kernel (`SO_PEERCRED` on Linux,
   `LOCAL_PEERPID` on macOS), walks `PID → CWD → git root`, and matches against
   registered agent worktrees. Callers that match get a kernel-verified
   identity. Callers that don't are "anonymous."
3. **Handler-level checks** — after dispatch, write handlers re-verify.
   `message.delete` and `message.edit` require caller == author.
   `message.deleteByAgent` requires caller == target agent.

The client cannot influence layers 2 or 3. They run entirely server-side.

---

### How the daemon resolves your identity

When you run a thrum CLI command, here's what happens server-side from the
moment your connection is accepted:

1. The daemon calls `peercred.PIDFromConn(conn)` — this is a thin wrapper around
   `tailscale/peercred` that extracts the connecting process PID from the kernel
   without any client cooperation.
2. `gopsutil/v3` resolves `PID → CWD` — the absolute path your process was in
   when it connected.
3. The resolver walks the CWD upward looking for a `.git` directory or `.git`
   file (the git worktree case). That gives it the git root.
4. Both the git root and every registered worktree path are canonicalized via
   `filepath.EvalSymlinks` — this handles the macOS `/var → /private/var` rename
   and similar symlink mismatches.
5. The canonicalized git root is matched against `session_refs`, the table of
   registered agent worktree paths in the state DB.
6. If a match is found: `ResolvedIdentity{AgentID, Worktree, PID}` is injected
   into the request context. If not: `ErrAnonymous` — the caller is _provably_
   anonymous (their CWD is outside every registered worktree).

**Since v0.9.1 (thrum-ndtw):** the resolver distinguishes _unknown state_ from
_provably anonymous_. Only steps 3 and 5 — no git root above your CWD, or a git
root that matches no registered worktree — return `ErrAnonymous` and trigger the
allowlist rejection below. Steps 1 and 2 are introspection steps: if the kernel
refuses peer credentials, or if gopsutil can't read the CWD for the connecting
PID (short-lived subprocess, permission drift, race window), the resolver
returns a raw error instead of wrapping `ErrAnonymous`. The daemon treats that
as "we don't know who you are" and falls through to legacy behavior —
client-asserted `caller_agent_id`, the pre-v0.9.0 path — rather than rejecting a
caller the daemon simply couldn't classify. Both introspection failures emit
`slog.Warn` with `step=pid failed` or `step=cwd failed`, which is the diagnostic
path when this matters.

The resolution runs once per connection, not once per request. Your process's
CWD doesn't change within a single unix-socket connection's lifetime, so doing
it per-request would be wasted work.

A "registered worktree" is any git root that was recorded in `session_refs` when
an agent ran `agent.register` and `session.start` from that directory. If you've
run `thrum quickstart` in a repo, that repo's root is registered. A plain git
repo you've never quickstarted in won't match — running thrum from there makes
you anonymous. This is intentional: the daemon only knows about agents it's been
introduced to.

Subdirectory depth doesn't matter. If your agent is registered at
`/Users/you/projects/myapp` and you run a thrum command from
`/Users/you/projects/myapp/internal/daemon/rpc`, the resolver walks up through
`.git` to `myapp`, matches it, and you're authenticated. You don't need to be at
the repo root.

If the resolver is absent — early boot, or the state DB isn't ready yet — the
daemon falls back to legacy behavior (client-asserted `caller_agent_id`). Tests
also skip the resolver. Both paths keep working as before; the new enforcement
only activates when the resolver is wired in.

---

### Anonymous caller policy (sec.3)

A caller without a resolved identity is "anonymous." This covers the normal case
of running `thrum team` from your home directory, or from any path that isn't
under a registered agent worktree. Since v0.9.1, "anonymous" means _provably_
anonymous — the resolver walked your PID's CWD to a git root and found that git
root is not in `session_refs`. It does _not_ mean the resolver failed
mid-introspection; those cases fall through to legacy client-asserted identity
(see the callout above).

Anonymous callers can invoke these 32 methods. Everything else is rejected at
the dispatcher with a clear error before the handler runs.

| Category                     | Methods                                                                                                     |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------- |
| Observability / liveness     | `health`, `daemon.status`, `sync.status`, `tsync.peers.list`, `peer.list`, `peer.status`, `telegram.status` |
| Agent / team / session reads | `agent.list`, `agent.whoami`, `agent.listContext`, `team.list`, `session.list`                              |
| Context reads                | `context.show`, `context.preamble.show`                                                                     |
| Message / group reads        | `message.get`, `message.list`, `message.outbox`, `group.list`, `group.info`, `group.members`                |
| Monitor reads                | `monitor.list`, `monitor.show`, `monitor.logs`                                                              |
| Tmux reads                   | `tmux.status`, `tmux.capture`, `tmux.check-pane`, `tmux.queue-status`, `tmux.queue-wait`                    |
| User identify                | `user.identify`                                                                                             |
| Bootstrap                    | `agent.register`, `session.start`, `session.setIntent`                                                      |

The list is defined in `internal/daemon/server.go` as `anonymousAllowedMethods`.

When an anonymous caller tries a mutating RPC, the error looks like this:

```text
anonymous caller cannot invoke "message.send": cd into a registered agent worktree and retry
```

The intent here was to err on the side of more access for reads, not less.
`cd ~ && thrum team` works. `cd ~ && thrum send` doesn't.

If you see this error from a path you believe _should_ be under a registered
worktree, it's genuinely anonymous — your git root isn't in `session_refs`. If
you see it when peercred introspection failed (which would be a v0.9.0 bug,
fixed in v0.9.1), grep the daemon log for `step=pid failed` or `step=cwd failed`
to confirm.

---

### Forged caller_agent_id rejected

The `caller_agent_id` field in request payloads still exists for backward
compatibility. Clients that don't set a resolver (tests, non-unix-socket
transports) still use it. But when the peercred resolver is active, the daemon
cross-checks the client-asserted `caller_agent_id` against the kernel-resolved
identity. Mismatch → the request is rejected with "identity mismatch."

There's no config flag to downgrade this check. Setting `unauthenticated_rpc` to
`warn` or `off` doesn't help — forgery rejection is foundational.

In practice this mainly affects scripts. If you had a script that ran from
`/tmp/some-tool` and passed `caller_agent_id: "agent-in-some-repo"` to send
messages on behalf of that agent, that script now gets rejected. The fix is to
run the script from the agent's worktree.

#### Shared-worktree claim trust (v0.9.0)

One narrow exception: when a single worktree hosts multiple registered agents
(typical in Playwright E2E harnesses, multi-agent test scenarios, and peer
bridge proxies), peercred's `PID → CWD → git-root` walk matches the worktree
correctly but has to pick one of the co-located agents arbitrarily. If the CLI's
explicit `caller_agent_id` is a _different_ co-located agent in that same
worktree, the daemon trusts the claim — the claim is kernel-verified to belong
to the same worktree, so it's not forgery.

The check is:

1. Peercred resolves the caller's worktree from the connecting PID.
2. Client claims `caller_agent_id = X`, peercred picked `Y`. Mismatch.
3. Daemon consults `state.IsAgentInWorktree(X, peercred_worktree)`. If `X` is
   also a registered agent in that worktree (via `session_refs` or a live
   identity file), the claim is honored and the request proceeds as `X`.
4. If `X` is NOT a registered agent in that worktree, the request is rejected
   with `identity_mismatch` as before.

Cross-worktree impersonation — a claim from `/path/to/repo-A` asserting an
identity registered in `/path/to/repo-B` — still hits the strict deny. The
fallback only covers the shared-worktree case, which is legitimate.

---

### WebSocket origin restriction (sec.1)

The daemon's WebSocket server (used by the web UI and browser clients) now
validates the `Origin` header on every upgrade handshake. Accepted origins:

- `http://localhost:<port>`
- `http://127.0.0.1:<port>`
- `ws://localhost:<port>`
- `ws://127.0.0.1:<port>`

Any other origin gets HTTP 403 before the handshake completes.

Pre-v0.9.0, `CheckOrigin` returned `true` unconditionally. That meant any
website you had open in your browser could open a WebSocket connection to your
local daemon and call RPCs. This is a standard CSRF vector for locally-running
services. v0.9.0 closes it.

If you're developing a custom client that connects to the daemon over WebSocket
from a non-localhost origin, it won't work anymore. Use the unix socket instead,
or proxy through localhost.

---

### Author-only message operations (sec.4)

`message.delete` (soft-delete) now resolves the caller's identity and checks
that the caller is the message's author. Non-author → rejected.

`message.edit` already had this check. sec.4 brings `message.delete` into parity
with it.

If you're calling `message.delete` on a message you didn't author, you'll get an
error like `"only message author can delete"`. There's no override.

---

### Bulk hard-delete restrictions (sec.8)

Two RPCs had no identity check at all pre-v0.9.0:

**`message.deleteByAgent(agent_id)`** — hard-deletes all messages by the named
agent across 5+ FK-linked tables. Now requires `caller == agent_id`. You can
bulk-delete your own messages. You can't bulk-delete another agent's.

**`message.deleteByScope(scope)`** — hard-deletes all messages in a scope. Now
restricted to daemon-internal callers only. It's not reachable from the unix
socket, the CLI, or the WebSocket transport. A structural test guards against
re-registration on WebSocket.

Both operations being callable by any local process was the worst pre-v0.9.0
exposure: one command could wipe another agent's entire message history with no
audit trail and no identity requirement.

---

### Bootstrap exception

Three RPCs are on the anonymous allowlist even though they mutate state:
`agent.register`, `session.start`, and `session.setIntent`.

They have to be. A brand-new agent has no identity yet — that's exactly what
these calls create. The peercred resolver resolves identity by looking up the
connecting process's worktree against `session_refs`. Before `agent.register`
runs, there's nothing to look up.

There's also a subtler issue: peercred identity is resolved once per connection,
at accept time. Even if `agent.register` populates `session_refs`
mid-connection, the current connection stays tagged as anonymous for its
lifetime. That's why all three bootstrap RPCs need the anonymous exception — the
quickstart flow calls them in sequence on a single connection.

The `0600` socket permission is the only access control on these three calls.
Only the owning user can reach them.

---

### Breaking changes from v0.8.x

These behaviors changed in v0.9.0. If you have scripts or tooling that relied on
the old behavior, they'll need updating.

- **Forged `caller_agent_id` rejected.** Previously any process could claim any
  agent ID. Now mismatches on unix-socket connections with the resolver active
  are rejected with "identity mismatch." Narrow exception: claims for another
  agent co-located in the peercred-resolved worktree are honored (see
  [Shared-worktree claim trust](#shared-worktree-claim-trust-v090)).
- **`message.delete` by non-author rejected.** Previously any caller could
  soft-delete any message by ID. Now only the author can.
- **`message.deleteByAgent` requires caller == target.** Previously callable by
  any local process. Now you can only bulk-delete your own messages.
- **`message.deleteByScope` is daemon-internal only.** Previously callable from
  the CLI and WebSocket. Now unreachable from any external client.
- **WebSocket from non-localhost origin: HTTP 403.** Previously `CheckOrigin`
  returned `true` for everything. Now foreign origins are blocked at the upgrade
  handshake.

---

### Identity guards vs. the security model

The security model answers "who is calling?" — that's peercred, the allowlist,
and the author checks described above.

Identity guards answer a different question: "given the resolved caller, are
they allowed to do this specific thing right now?" Guards run after identity
resolution and add extra invariants, like cross-worktree CWD checks that verify
the caller is actually operating from within the expected agent worktree.

If you're getting guard errors rather than the authentication errors described
here, see [Troubleshooting Identity Issues](troubleshooting-identity.md) for the
specific error messages and recovery steps.
