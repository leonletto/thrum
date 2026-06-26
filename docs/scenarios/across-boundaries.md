
## Agents Across Repos/Machines

Peers connect Thrum daemons running in separate repos or on completely different
machines. On a local network, same-subnet discovery handles the pairing
automatically. For remote machines, Tailscale gives you an encrypted mesh — both
sides authenticate once and Thrum treats them as neighbors. Either way, it's the
same team, the same message routing, and the same `thrum team` view you'd get
working in a single repo.

## Prerequisites

- Thrum installed on both sides
- Both daemons running (`thrum daemon start`)
- Same-subnet connectivity **or** Tailscale installed and authenticated on both
  machines

## Walkthrough

Work through these in order:

1. [Peers](../peers.md) — the concept and the pairing flow
2. [Cross-Machine Sync](../guides/cross-machine-sync.md) — end-to-end
   walkthrough
3. [Tailscale Sync](../tailscale-sync.md) — the Tailscale transport
4. [Tailscale Security](../tailscale-security.md) — how the mesh is secured
5. [Messaging](../messaging.md) — how messages flow across peers
6. [Multi-Agent Setup](../multi-agent.md) — coordinator/implementer roles

## When you're ready for more

[Automated Plan Execution](orchestration.md) — hand a plan to a coordinator and
let agents drive the work end to end.
