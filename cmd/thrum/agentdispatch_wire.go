package main

import (
	"fmt"
	"log/slog"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// listFilesProbeMethod is the RPC method daemon-boot probes to decide
// whether agentdispatch's stage-8 drain should run or short-circuit.
// MB-1.S2 (file-streaming epic) ships the handler; pre-MB-1.S2 daemons
// flip the tracker into skip-drain mode so teardown never polls for
// RPCs that can't exist.
const listFilesProbeMethod = "agent.listFiles"

// wireAgentDispatch performs the daemon-boot feature-detect step
// described in B-B1 plan Task 63 (and pinned by spec §9.7.4).
//
// Specifically:
//  1. Constructs the in-flight tracker that future agent.listFiles /
//     agent.getFile RPC handlers Begin/End through.
//  2. Probes the JSON-RPC server for the agent.listFiles handler.
//     If it isn't registered (the common v0.11 case — MB-1.S2 hasn't
//     shipped), flips the tracker into skip-drain mode so stage-8
//     drain returns immediately rather than polling a tracker that
//     would never see Begin calls.
//  3. Builds the Drainer that satisfies agentdispatch.RPCDrainer and
//     gets injected into ScheduledAgentHandler.Deps when the wider
//     B-B1 dispatch wiring lands.
//
// Returns (drainer, tracker) so the downstream wiring task can
// inject Drainer into Deps and the agent-side RPC adapter (lands
// with MB-1.S2) into the tracker's Begin/End surface.
//
// The tracker's concrete type is package-private to agentdispatch
// so the boot-time SetSkipDrain mutation is gated through this
// helper; callers receive only the InflightTracker interface
// (Begin/End/Count) so they can't flip skip-drain mid-flight.
//
// PANICS only if server is nil — that's a wiring bug, not a runtime
// failure mode.
func wireAgentDispatch(server *daemon.Server) (*agentdispatch.Drainer, agentdispatch.InflightTracker) {
	if server == nil {
		panic("wireAgentDispatch: nil server (wiring bug)")
	}

	tracker := agentdispatch.NewInflightTracker()
	if !server.HasHandler(listFilesProbeMethod) {
		tracker.SetSkipDrain(true)
		// Debug-level: this is the expected v0.11 steady state, not
		// an operational event. Operators investigating fast stage-8
		// teardowns can flip the log level to see the probe outcome.
		slog.Debug("agent.listFiles RPC not registered; stage-8 drain short-circuit active",
			"component", "agentdispatch",
			"probe_method", listFilesProbeMethod,
		)
	}
	drainer := agentdispatch.NewDrainer(tracker)
	return drainer, tracker
}

// userJobTypes lists the user-facing scheduler job types B-B1 E6.5
// owns. Kept here (not in agentdispatch) because cmd/thrum is the
// composition root that decides which types are "real for v0.11";
// agentdispatch can add new handler types over time without
// implicitly registering them.
var userJobTypes = []string{"scheduled_agent", "nudge"}

// registerPlaceholderHandlers performs E6.5 Task 42a: register a
// PlaceholderHandler for each user-facing job type so A-B1's
// validator + reactor recognize the type name even before E6.5
// Task 42b ships the real adapter glue. Returns an error if any
// registration fails (e.g. duplicate type — would be a wiring
// bug since this is called once at daemon boot).
//
// Idempotency note: scheduler.RegisterTypeHandler rejects
// duplicates, so calling registerPlaceholderHandlers a second
// time will fail on the first type. 42b will need to either swap
// the registration mechanism or land alongside a daemon-restart
// boundary; documented in main.go's call site.
func registerPlaceholderHandlers(sched *scheduler.Scheduler) error {
	if sched == nil {
		return fmt.Errorf("registerPlaceholderHandlers: nil scheduler")
	}
	for _, jobType := range userJobTypes {
		if err := sched.RegisterTypeHandler(jobType,
			agentdispatch.NewPlaceholderHandler(jobType)); err != nil {
			return fmt.Errorf("register %s type handler: %w", jobType, err)
		}
	}
	return nil
}
