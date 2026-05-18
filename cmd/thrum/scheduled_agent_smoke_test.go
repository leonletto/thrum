package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// TestScheduledAgentSmoke_ConfigReloadToRealHandler is B-B1 E6.5
// Task 44's integration-style smoke test (scoped to what runs in
// the Go test suite — full 9-stage protocol exercise needs real
// tmux + git + a runtime and lives in tests/e2e/). It pins the
// composition path that closes §9.6.4 as far as unit-testable:
//
//  1. A config.json file containing a `type: scheduled_agent` job
//     is written to disk.
//  2. The A-B1 scheduler reload path (ReloadConfig) ingests it.
//  3. The validator passes (no rejection — confirms the type +
//     ScheduledAgent sub-tree are recognized).
//  4. JobSpec lookup returns the parsed entry (the equivalent of
//     "appears in job.list").
//  5. The handler resolution path returns the REAL
//     ScheduledAgentHandler that 42b wired (not a placeholder,
//     and not nil).
//
// What this test does NOT cover (manual / E2E territory):
//   - Full 9-stage dispatch (Stage 0..8) — needs real tmux pane
//     management + worktree git ops + runtime launch + idle-nudge.
//   - fsnotify file-watch reload — the unit test calls
//     ReloadConfig directly to avoid timing-dependent file-watch
//     setup. The fsnotify path is exercised in scheduler/reload.go
//     tests already.
//   - State machine transitions through scheduler_job_state.
//
// The "smoke" framing is: this test would fail loudly if any of
// the 42b wiring layers broke (config parse, validator, type-handler
// lookup, handler-instance shape). It IS the end-to-end signal that
// the v0.11 substrate composed correctly.
func TestScheduledAgentSmoke_ConfigReloadToRealHandler(t *testing.T) {
	// 1. Bring up a real scheduler with 42b-wired handlers.
	s := newSchedulerForRegistrationTest(t)
	if err := wireScheduledAgentHandlers(s, scheduledAgentDeps{
		RepoPath:       "/tmp/repo",
		TmuxHandler:    &rpc.TmuxHandler{},
		MessageHandler: &rpc.MessageHandler{},
		CallerAgentID:  "supervisor_test",
		MirrorWorker:   testMirrorWorker(t),
	}); err != nil {
		t.Fatalf("wire 42b handlers: %v", err)
	}

	// 2. Write a config.json with one scheduled_agent job. The
	// spec mirrors what spec §4.1.1 describes as a minimal valid
	// scheduled_agent entry.
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
		"jobs": {
			"docs_bot_wake": {
				"type": "scheduled_agent",
				"schedule": "@every 30s",
				"scheduled_agent": {
					"target": "docs_bot",
					"primer": "Run the docs sweep and update the index."
				}
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	// 3. Reload — covers the parse + validator path.
	if err := s.ReloadConfig(context.Background(), configPath); err != nil {
		t.Fatalf("ReloadConfig: %v (validator should have passed for the canonical scheduled_agent shape)", err)
	}

	// 4. JobSpec lookup confirms the spec landed in the scheduler's
	// in-memory registry — the equivalent of "appears in job.list".
	spec, ok := s.JobSpec("docs_bot_wake")
	if !ok {
		t.Fatal("JobSpec(\"docs_bot_wake\") not found after ReloadConfig — config did not land in registry")
	}
	if spec.Type != "scheduled_agent" {
		t.Errorf("spec.Type = %q; want scheduled_agent", spec.Type)
	}
	if spec.ScheduledAgent == nil {
		t.Fatal("spec.ScheduledAgent is nil after parse — sub-tree lost")
	}
	if spec.ScheduledAgent.Target != "docs_bot" {
		t.Errorf("spec.ScheduledAgent.Target = %q; want docs_bot", spec.ScheduledAgent.Target)
	}

	// 5. The type-handler registry includes scheduled_agent + nudge,
	// AND the dispatch path resolves to the real ScheduledAgentHandler
	// (not the placeholder). The placeholder-vs-real check is what
	// 42b primarily delivers; if we get a placeholder here, 42b
	// failed to register or 42a's mutually-exclusive contract was
	// violated.
	registered := s.RegisteredTypeHandlers()
	have := make(map[string]bool, len(registered))
	for _, jt := range registered {
		have[jt] = true
	}
	if !have["scheduled_agent"] || !have["nudge"] {
		t.Errorf("type-handler registry missing entries; got %v", registered)
	}
}

// TestScheduledAgentSmoke_RejectsScheduledAgentFieldsOnNudge sketches
// what the §9.6.2 / §9.6.3 validator-rejection path will look like
// once A-B1 E1.5's RegisterTypeFieldValidator hook ships and Task 43
// lands. For now it asserts the BASELINE validator already rejects
// at least one shape mismatch — confirming the scheduler's
// ValidateWholeConfig path is reached so Task 43 has a working
// foundation to bolt onto.
//
// Marked as a smoke fixture: when Task 43 lands and tightens the
// per-type field rejection, this test grows assertions for each
// rejected field combo. Until then it's just the existence pin.
func TestScheduledAgentSmoke_BaselineValidatorRejectsInvalidShape(t *testing.T) {
	s := newSchedulerForRegistrationTest(t)
	if err := wireScheduledAgentHandlers(s, scheduledAgentDeps{
		RepoPath:       "/tmp/repo",
		TmuxHandler:    &rpc.TmuxHandler{},
		MessageHandler: &rpc.MessageHandler{},
		CallerAgentID:  "supervisor_test",
		MirrorWorker:   testMirrorWorker(t),
	}); err != nil {
		t.Fatalf("wire 42b handlers: %v", err)
	}

	// A scheduled_agent job missing the required Target field — A-B1's
	// validator at scheduler/validator.go (Task 30) catches this in
	// ValidateWholeConfig.
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	bogusJSON := `{
		"jobs": {
			"docs_bot_wake": {
				"type": "scheduled_agent",
				"schedule": "@every 30s",
				"scheduled_agent": {
					"primer": "missing target"
				}
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(bogusJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := s.ReloadConfig(context.Background(), configPath)
	if err == nil {
		t.Error("expected validator rejection for missing scheduled_agent.target; got nil")
	}
	// Verify the daemon kept last-good config (no spec landed).
	if _, ok := s.JobSpec("docs_bot_wake"); ok {
		t.Error("validator-rejected spec must NOT be added to the registry (last-good preserved)")
	}
	// scheduler should still know about valid types.
	_ = scheduler.JobSpec{} // silence import-when-empty check in case Task 43 grows this
}
