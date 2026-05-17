package main

import (
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/websocket"
)

// wireScheduler constructs the A-B1 scheduler primitive, registers its
// 10 JSON-RPC methods on both the Unix-socket server and the WebSocket
// registry, and registers the canonical internal.scheduler_event_cleanup
// housekeeping job. The caller invokes Start on the returned *Scheduler
// after handler registration is complete.
//
// retentionDays controls scheduler_job_events pruning cadence. The
// substrate's NewCleanupHandler clamps non-positive values to 7 days; we
// pass through whatever cfg.Daemon.Scheduler.EventRetentionDays produced
// (zero is the documented "use default" sentinel — canonical §4.4).
//
// Downstream consumers (A-B4 stalled_agent_sweep, D-B1 email_poll,
// C-B1 skill_staleness_check, MB-1.S6 telemetry_persistent_poll,
// A-B2 backup + peer_sync) RegisterInternal against the returned
// scheduler from main.go before lifecycle.Run starts the listeners.
func wireScheduler(
	server *daemon.Server,
	wsReg *websocket.SimpleRegistry,
	db *safedb.DB,
	daemonID string,
	retentionDays int,
) *scheduler.Scheduler {
	sched := scheduler.New(scheduler.Config{
		DB:       db,
		DaemonID: daemonID,
	})

	for method, handler := range scheduler.Methods(sched) {
		server.RegisterHandler(method, daemon.Handler(handler))
		wsReg.Register(method, websocket.Handler(handler))
	}

	cleanup := scheduler.NewCleanupHandler(scheduler.NewStateStore(db), retentionDays)
	sched.RegisterInternal(
		"internal.scheduler_event_cleanup", "@daily",
		scheduler.InternalOpts{RunAtStart: false, CatchUp: "skip"},
		cleanup,
	)

	return sched
}
