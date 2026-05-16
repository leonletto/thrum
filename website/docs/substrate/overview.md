---
title: "Personal Agent Substrate"
description:
  "v0.11 turns thrum into the substrate beneath personal-agent harnesses
  (scheduling, scheduled-agent lifecycle, skill registration, email transport,
  reminders). What it is, why it exists, and how the pieces fit together."
category: "substrate"
order: 1
tags: ["v0.11", "substrate", "personal-agent", "scheduler", "skills"]
last_updated: "2026-05-16"
---

## Personal Agent Substrate

v0.11 is a structural release. The pitch in one sentence:

> Thrum's role is to be the substrate that lets any harness implement
> personal-agent patterns cleanly — not to become a harness itself.

That framing locked in May 2026. v0.10 gave you the messaging foundation (direct
messages, role routing, critical broadcast, sync, peers, Telegram). v0.11 adds
the layer above it: scheduling, scheduled-agent lifecycle, a per-project skill
library that agents can extend at runtime, an email transport alongside
Telegram, and a unified reminder substrate.

If you're using thrum today for agent coordination, none of this is required
reading — the v0.10 messaging surface keeps working. The substrate is for anyone
wanting to build (or use) a personal-agent harness — Pi, Hermes, Open Claw,
Replicant, or your own — on top of thrum.

## The three-layer decomposition

Personal-agent frameworks decompose cleanly into three layers:

| Layer         | What it provides                                                                                 |
| ------------- | ------------------------------------------------------------------------------------------------ |
| **Substrate** | Messaging, scheduling, skill registry, state persistence, identity isolation, transport plumbing |
| **Behavior**  | Harness-provided meta-skills ("when/how/what to build")                                          |
| **Runtime**   | Underlying LLM CLI (Claude, Codex, Kiro, Cursor)                                                 |

A concrete trace: Pi schedules its next wake by calling the thrum daemon. The
daemon handles persistence and cron-style scheduling and idle-nudge escalation.
The runtime is whichever LLM CLI you've configured. Pi itself contains only the
behavior layer — the meta-skills that decide what to do on each wake. Thrum
carries the rest.

Each layer stays focused. The substrate makes the patterns possible across
runtimes; the harness picks which patterns to use; the runtime executes.

## The Pi / Hermes / Open Claw / Replicant pattern

The recent crop of personal-agent frameworks have a common shape:

- A **scheduler** (what to do, when)
- A **wake trigger** (something fires; the agent runs)
- An **ephemeral runtime** (a fresh LLM session, sometimes with continuity)
- **State persistence** (what the agent learned, scheduled, deferred)
- **Skill accumulation** (the agent grows new capabilities over time)

Thrum already had four of the five before v0.11. Messaging gave it identity
isolation and state persistence; the daemon and sync gave it transport plumbing;
the role-templates and skill plugins gave it accumulation. The gap was the
unified scheduler and the runtime-skill-registration plumbing. v0.11 fills that
gap.

The Pi/Hermes phrase "the agent that builds itself" maps to the three layers
above: the runtime executes, the harness's meta-skills decide _what_ to build
(and when), and the substrate makes the new skill discoverable next session.
v0.11 ships the substrate piece. The harnesses keep their own behaviour.

## What v0.11 adds

The five feature families in v0.11 — each gets its own page once the
implementation lands.

- **[Scheduler primitive](scheduler.md)** — a single daemon-side scheduling
  abstraction. Consolidates three existing ticker loops (backup, periodic sync,
  inbox poll) and gives the rest of the substrate (scheduled agents, email poll,
  telemetry, skill staleness, stalled-agent sweep) one bus to bind against.

- **[Scheduled-agent lifecycle](scheduled-agents.md)** — wake, run, sleep,
  observe as a first-class flow. Spawn an agent on a schedule, give it a prompt,
  watch what it does via inbox + beads memories, let it sleep until the next
  wake. Fresh-context-each-wake or resume-prior-session, configurable per agent.

- **[Skill library + registration API](skill-library.md)** — first-class
  per-project skill library (mirroring what works organically in repos that
  hand-rolled it). The registration API lets a meta-skill author a new skill
  mid-session and have future sessions discover it without restart.

- **[Email transport](email-transport.md)** — IMAP/SMTP adapter alongside the
  existing Telegram bridge. A single shared mailbox can serve multiple repos;
  subject-line conventions encode repo identity for demux. Reply threading uses
  email's native In-Reply-To header.

- **[Reminders + stalled-agent sweep](reminders.md)** — a unified reminder
  substrate. Time-triggered reminders (an agent or user defers an action) share
  the same table as condition-triggered ones (the daemon notices an agent has
  been silent too long and pings the coordinator).

- **[Headless worktree API](headless-worktrees.md)** — internal plumbing for
  scheduled agents to consume ephemeral worktrees without operator involvement.
  Mostly an implementation detail; brief note for anyone digging into the
  substrate internals.

## How v0.11 relates to v0.10

The two releases stack:

- **v0.10 messaging** is the bus. Agents send each other directed messages or
  broadcast critical events; the daemon syncs across machines and bridges out to
  Telegram if you've paired it. Identity isolation, peer transport, sync,
  persistence — all of it landed pre-v0.11.

- **v0.11 substrate** is the runtime that consumes that bus. A scheduled agent
  uses messaging to report back. A skill registered at runtime arrives via the
  same identity-resolved RPC plumbing. The email transport reuses the Telegram
  bridge's relay shape, just over IMAP/SMTP.

You don't need v0.11 to use v0.10's messaging features. You do need v0.10's
messaging to use v0.11's substrate features. The dependency is one-way.

## Status

v0.11 is in development. The substrate-track pre-release cycle starts when the
first `v0.11.0-rc.1` tag publishes from the `thrum-agents` branch — see the
[Beta Channel](../beta-channel.md) page for substrate-track install instructions
when it's ready.

The feature pages above will fill in as each piece lands and gets shaken out in
soak. Until then, the parent epic and per-feature locked plans live in the
source tree under `dev-docs/` for anyone who wants to dig.
