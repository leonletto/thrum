package main

import (
	"fmt"

	"github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// emailPollIntervalDefaultSeconds is the substrate cadence for the
// internal.email_poll job when EmailConfig.PollIntervalSeconds is unset.
// Matches the historic 60-second pump cadence shipped in D-B1.14.
const emailPollIntervalDefaultSeconds = 60

// emailQueueDrainIntervalDefaultSeconds is the substrate cadence for the
// internal.email_queue_drain job when EmailQueue.PollIntervalSeconds is
// unset. Matches the 5-second default that lived inside Worker.Run pre
// thrum-6qmf.8.
const emailQueueDrainIntervalDefaultSeconds = 5

// wireEmailInternal registers the three substrate-scheduled jobs that
// drive the email bridge:
//
//   - internal.email_poll          (cfg.PollIntervalSeconds, default 60s)
//   - internal.email_dedup_cleanup (@daily)
//   - internal.email_queue_drain   (cfg.Queue.PollIntervalSeconds, default 5s)
//
// thrum-6qmf.8 substrate-adoption: replaces the in-bridge ticker
// goroutines that shipped with D-B1.14. The handlers grab their target
// state via the bridge's atomic getters and no-op cleanly when the
// bridge is between restart cycles.
//
// PANICS if sched.RegisterInternal panics — duplicate ID or shape error
// is a programmer mistake and the daemon should crash early per A-B1
// spec §5.3.
func wireEmailInternal(sched *scheduler.Scheduler, bridge *email.Bridge, cfg config.EmailConfig) {
	pollSeconds := cfg.PollIntervalSeconds
	if pollSeconds <= 0 {
		pollSeconds = emailPollIntervalDefaultSeconds
	}
	queueSeconds := cfg.Queue.PollIntervalSeconds
	if queueSeconds <= 0 {
		queueSeconds = emailQueueDrainIntervalDefaultSeconds
	}

	sched.RegisterInternal(
		"internal.email_poll",
		fmt.Sprintf("@every %ds", pollSeconds),
		scheduler.InternalOpts{
			// RunAtStart=false: bridge.Run() may not have established the
			// IMAP connection yet on the first scheduler tick; let the
			// configured cadence cover it.
			RunAtStart: false,
			// CatchUp="skip": daemon-down for hours shouldn't replay
			// every missed tick; the 24-hour-window fetch covers
			// anything we missed in a single pass.
			CatchUp: "skip",
		},
		email.NewPollHandler(bridge),
	)

	sched.RegisterInternal(
		"internal.email_dedup_cleanup",
		"@daily",
		scheduler.InternalOpts{
			RunAtStart: false,
			CatchUp:    "skip",
		},
		email.NewDedupCleanupHandler(bridge),
	)

	sched.RegisterInternal(
		"internal.email_queue_drain",
		fmt.Sprintf("@every %ds", queueSeconds),
		scheduler.InternalOpts{
			// RunAtStart=false: queue starts empty on a fresh daemon;
			// startup orphan-recovery (bridge.run()) already covered
			// any survivors before this handler is registered.
			RunAtStart: false,
			CatchUp:    "skip",
		},
		email.NewQueueDrainHandler(bridge),
	)
}
