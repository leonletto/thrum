package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/sweep"
)

// wireSweep constructs the A-B4 sweep handler and registers it as the
// internal.stalled_agent_sweep job (canonical §6.3). Mirrors the
// wireScheduler shape so all daemon-boot internal-job wiring follows
// one pattern.
//
// Three collaborators come together here:
//   - reminders.Store — already constructed earlier in main.go
//     (the same store the dispatcher uses; sweep mints rows the
//     dispatcher fires).
//   - SchedulerState — wraps st.DB() (the scheduler_job_state read)
//   - sched.JobSpec (the job_id → agent join). Per
//     sweep/scheduler_state.go the placement here keeps A-B1's
//     substrate package isolated from cross-epic joins.
//   - PaneSource — wraps the daemon identity-file directory walk
//     (identityFileAgentRegistry below). Production-only adapter.
//   - ChainResolver — reads daemon.sweep.alert_chain with fallback
//     to escalation.supervisor_agent_name.
//
// Cadence comes from daemon.stalled_sweep.interval_minutes
// (canonical §4.4 default 15). DaemonConfig.StalledSweepIntervalMinutes
// clamps zero/negative to 15 so this helper sees a positive value
// even when the operator omits the block.
//
// PANICS if sched.RegisterInternal panics — that's a programmer
// error (duplicate ID / bad shape) and the daemon should crash early
// per A-B1 spec §5.3.
func wireSweep(
	sched *scheduler.Scheduler,
	store reminders.Store,
	db *safedb.DB,
	thrumDir string,
	cfg *config.DaemonConfig,
) {
	intervalMinutes := cfg.StalledSweepIntervalMinutes()
	threshold := time.Duration(intervalMinutes) * time.Minute

	handler := sweep.New(
		store,
		sweep.NewSchedulerState(db, sched),
		sweep.NewDaemonPaneSource(&identityFileAgentRegistry{
			identitiesDir: filepath.Join(thrumDir, "identities"),
		}),
		sweep.NewChainResolver(sweep.ChainConfig{
			AlertChain:          cfg.Sweep.AlertChain,
			SupervisorAgentName: cfg.Escalation.SupervisorAgentName,
		}),
		threshold,
	)

	sched.RegisterInternal(
		"internal.stalled_agent_sweep",
		fmt.Sprintf("@every %dm", intervalMinutes),
		scheduler.InternalOpts{
			// RunAtStart=false: a fresh daemon boot doesn't need a
			// stalled-agent scan in its first second; the canonical
			// cadence picks it up within the configured interval.
			RunAtStart: false,
			// CatchUp="skip" (default): daemon-down for hours
			// shouldn't replay every missed tick; one sweep covers
			// all currently-stale agents in a single pass via
			// idempotency match-key.
			CatchUp: "skip",
		},
		handler,
	)
}

// identityFileAgentRegistry satisfies sweep.AgentRegistry by walking
// the per-agent identity files in .thrum/identities/. Each file maps
// to one AgentInfo{Name, TmuxSession}; files without a tmux_session
// field are passed through with TmuxSession="" — the sweep PaneSource
// filters those out at the next layer.
//
// This is the production adapter for sweep.AgentRegistry. Tests in
// sweep/panes_test.go inject stubRegistry instead.
type identityFileAgentRegistry struct {
	identitiesDir string
}

// LiveAgents reads every *.json under identitiesDir and yields one
// AgentInfo per file. Errors loading individual files are logged and
// skipped — a corrupt identity file shouldn't break the sweep batch
// (matches the daemon's broader "tolerate single-row corruption"
// posture established in earlier permission/teleKey work).
//
// Directory-not-found returns a zero-length slice with nil error.
// This is the fresh-install case (no agents registered yet); the
// sweep handler treats it as "no panes to evaluate" and exits the
// tick cleanly.
func (r *identityFileAgentRegistry) LiveAgents(_ context.Context) ([]sweep.AgentInfo, error) {
	entries, err := os.ReadDir(r.identitiesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read identities dir %s: %w", r.identitiesDir, err)
	}

	out := make([]sweep.AgentInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(r.identitiesDir, e.Name())
		// #nosec G304 -- path is constrained to entries returned by os.ReadDir(r.identitiesDir);
		// identitiesDir comes from internal config, not user input.
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("[sweep] read identity %s: %v (skipping)", path, err)
			continue
		}
		var idFile config.IdentityFile
		if err := json.Unmarshal(data, &idFile); err != nil {
			log.Printf("[sweep] parse identity %s: %v (skipping)", path, err)
			continue
		}
		if idFile.Agent.Name == "" {
			// Identity file present but missing the required field
			// (legacy / partial state). Skip rather than send a
			// malformed AgentInfo downstream.
			continue
		}
		out = append(out, sweep.AgentInfo{
			Name:        idFile.Agent.Name,
			TmuxSession: idFile.TmuxSession,
		})
	}
	return out, nil
}

// Compile-time check mirrors the pattern in reminders_wire.go for
// messageHandlerSender — catches sweep.AgentRegistry signature drift
// at production build time rather than at test-suite time.
var _ sweep.AgentRegistry = (*identityFileAgentRegistry)(nil)
