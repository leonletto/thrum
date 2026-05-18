package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/spf13/cobra"
)

// jobCmd returns the "job" subcommand group. Currently a single
// leaf — `thrum job done` — but the group is created so future
// commands (`job list`, `job history`, etc.) can land here without
// promoting `done` to a top-level command.
func jobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Scheduled-agent lifecycle CLI (B-B1)",
		Long: "Commands for the scheduled-agent lifecycle introduced in v0.11.\n" +
			"Today exposes: done — signal completion of the current run.\n",
	}
	cmd.AddCommand(jobDoneCmd())
	return cmd
}

// jobDoneCmd returns the "done" subcommand wrapping the daemon's
// job.done RPC per A-B1 §5.2. Agents inside a scheduled_agent run
// call `thrum job done [--summary "..."]` from anywhere in the
// worktree to close the run cleanly; the substrate then runs
// stage-8 teardown (CtrlC + grace + drain + kill + destroy) on
// the runtime's behalf.
func jobDoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done",
		Short: "Signal completion of the current scheduled-agent run",
		Long: "Closes the active scheduled-agent run by sending job.done to the daemon.\n" +
			"Defaults --run-id (and informational --job-id) from the agent's\n" +
			"run-context file at .thrum/agents/<agent>/run_context.json,\n" +
			"which is written by the agent.wake lean-prime skill at wake time.\n",
		RunE: runJobDone,
	}
	cmd.Flags().String("summary", "", "Optional summary of the completed run")
	cmd.Flags().String("job-id", "", "Job ID (informational; defaults from run context)")
	cmd.Flags().String("run-id", "", "Run ID (required; defaults from run context)")
	return cmd
}

// agentRunContext is the JSON shape persisted by the agent.wake
// lean-prime skill (E6.2 ships the writer; E6.4 ships the reader).
// Located at .thrum/agents/<agent_id>/run_context.json in the
// agent's worktree. The job_id field is informational — the
// daemon's job.done RPC only consumes run_id — but kept for
// operator-debugging symmetry with the agent.wake message body
// (spec §7.4 includes both fields).
type agentRunContext struct {
	JobID string `json:"job_id"`
	RunID string `json:"run_id"`
}

// runJobDone is the cobra RunE for `thrum job done`. Resolves the
// run_id (flag → context file → error) and forwards to the daemon's
// job.done RPC. The fire-and-forget shape per A-B1 §5.2: a
// successful call only confirms the daemon received the signal;
// stage-8 teardown happens asynchronously.
func runJobDone(cmd *cobra.Command, _ []string) error {
	summary, _ := cmd.Flags().GetString("summary")
	jobID, _ := cmd.Flags().GetString("job-id")
	runID, _ := cmd.Flags().GetString("run-id")

	if jobID == "" || runID == "" {
		ctx, err := loadAgentRunContext(flagRepo)
		if err == nil {
			if jobID == "" {
				jobID = ctx.JobID
			}
			if runID == "" {
				runID = ctx.RunID
			}
		} else if runID == "" {
			// Only error when run_id is still missing — the daemon
			// doesn't need job_id, so a partial context-load
			// (e.g. file present but RunID empty) is fatal for
			// run_id but not job_id.
			return fmt.Errorf("--run-id required and run context unavailable: %w", err)
		}
	}

	if runID == "" {
		return fmt.Errorf("--run-id required (run context found but RunID empty)")
	}

	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	agentID, _ := resolveLocalAgentID()

	req := scheduler.JobDoneRequest{
		CallerAgentID: agentID,
		RunID:         runID,
		Summary:       summary,
	}
	var resp scheduler.JobDoneResponse
	if err := client.Call("job.done", req, &resp); err != nil {
		return fmt.Errorf("job.done RPC: %w", err)
	}

	// jobID surfaces only for the human-facing confirmation so
	// operators can correlate `thrum job done` output with
	// `thrum cron history <job_id>` in subsequent diagnostics.
	if jobID != "" {
		fmt.Printf("✓ Run %s (job %s) marked done\n", runID, jobID)
	} else {
		fmt.Printf("✓ Run %s marked done\n", runID)
	}
	return nil
}

// loadAgentRunContext reads the agent.wake run-context file written
// by E6.2's lean-prime skill. Returns the parsed shape or an error
// describing what went wrong (file missing, parse failure). Callers
// distinguish "no context" (benign — user must pass flags) from
// "parse failure" (worth investigating — corrupted file) via the
// error type wrapping.
//
// Factored out as a package-level helper rather than a private
// closure so the JSON parse logic is unit-testable without spinning
// up a daemon (the integration end of the test pipeline lives in
// E6.5 smoke per the dispatch's cross-epic notes).
func loadAgentRunContext(repoPath string) (agentRunContext, error) {
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return agentRunContext{}, fmt.Errorf("resolve agent id: %w", err)
	}
	path := filepath.Join(repoPath, ".thrum", "agents", agentID, "run_context.json")
	// gosec G304: path is composed from flagRepo (caller-controlled
	// CLI flag, sanitized by cobra) + the resolved agent id
	// (validated by identity.GenerateAgentID) + a fixed leaf. The
	// agent id passes through identity.GenerateAgentID which
	// constrains it to a known character set, so traversal via
	// agent id is structurally impossible. flagRepo CAN traverse
	// (operator can pass --repo /any/path), but that's the caller's
	// intended workspace, not a confused-deputy risk.
	data, err := os.ReadFile(path) //nolint:gosec // see comment above
	if err != nil {
		return agentRunContext{}, fmt.Errorf("read %s: %w", path, err)
	}
	var ctx agentRunContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		return agentRunContext{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return ctx, nil
}

// parseAgentRunContext is the file-format-only entry point for unit
// tests that don't want to depend on the full file system + agent-
// id resolution. Production callers always go through
// loadAgentRunContext.
func parseAgentRunContext(data []byte) (agentRunContext, error) {
	var ctx agentRunContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		return agentRunContext{}, fmt.Errorf("parse run_context.json: %w", err)
	}
	return ctx, nil
}
