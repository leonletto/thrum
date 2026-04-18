package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/identity/guard"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/process"
)

// RefreshResult describes the outcome of a RefreshLocalIdentity call.
// Zero-value result (empty FileChanged, DaemonUpdated=false) means the
// happy path: nothing drifted, nothing was written.
type RefreshResult struct {
	// FileChanged lists the identity file fields that were updated on disk.
	// Empty when nothing changed. Values: "agent_pid", "runtime",
	// "preferred_runtime", "tmux_session", "branch".
	FileChanged []string

	// DaemonUpdated is true iff the client re-registered with the daemon.
	// Implies FileChanged contains "agent_pid" or "runtime".
	DaemonUpdated bool

	// DetectedPID and DetectedRuntime capture the result of the process
	// tree walk. Populated whenever a runtime ancestor was found, even
	// if nothing changed downstream.
	DetectedPID     int
	DetectedRuntime string

	// SessionResumed is true when the daemon's agent.register handler
	// emitted a fresh agent.session.start because the agent's prior
	// session row was ended. Surfaced from RegisterResponse.SessionResumed
	// (thrum-xir.18). Implies the agent was offline in team.list before
	// this refresh and is now back to active.
	SessionResumed bool

	// ResumedSessionID is the new session_id created by the daemon's
	// resurrect path. Empty when SessionResumed is false. Callers may
	// log it for audit but should not cache it across quickstart, since
	// quickstart's recoverOrphanedSessions will close it and start a
	// fresh one.
	ResumedSessionID string
}

// detectAncestor is a package-level var so tests can swap in fakes via
// t.Cleanup. Production callers should go through RefreshLocalIdentity.
var detectAncestor = process.FindClaudeAncestor

// RefreshLocalIdentity inspects live process, tmux, and git state and
// reconciles the local identity file + daemon's agent record with reality.
//
// RepoPath is the worktree root (use "." for cwd). If client is nil the
// refresh is file-only and never round-trips to the daemon; otherwise the
// daemon record is re-registered on drift.
//
// Returns (nil, nil) when no identity file exists (pre-quickstart case).
// Returns a non-nil result on success; zero-valued result means the happy
// path (nothing drifted). Callers should log errors to stderr and continue
// — refresh failures must not abort the calling command.
func RefreshLocalIdentity(client *Client, repoPath string) (*RefreshResult, error) {
	ctx := context.Background()

	// Step 0: IDENTITY GUARD (Rule #4‴). Runs before any detection so
	// ownership violations short-circuit the refresh. Non-PID drift
	// reconciliation happens inside guard.Check; the remaining refresh
	// steps below handle daemon re-registration and session resume.
	if err := guard.Check(ctx, repoPath, loadGuardConfig(repoPath), nil); err != nil {
		return nil, err
	}

	// Step 1: DETECT. Detection runs for RefreshResult reporting
	// only — guard.Check above has already reconciled drift to disk,
	// so we do not diff or write here. The detectAncestor stub seam
	// is preserved for tests that want to pin the reported values.
	detectedPID, detectedRuntime := detectAncestor(ctx)

	// Step 2: LOAD the identity file (post-reconciliation) so the
	// daemon reconcile step below sees the latest Runtime / Branch /
	// TmuxSession written by guard.Check.
	idFile, _, err := config.LoadIdentityWithPath(repoPath)
	if err != nil {
		if isNoIdentityFile(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load identity: %w", err)
	}
	if idFile == nil {
		return nil, nil
	}

	result := &RefreshResult{
		DetectedPID:     detectedPID,
		DetectedRuntime: detectedRuntime,
	}

	// Step 5: RECONCILE DAEMON
	//
	// Always call AgentRegister when the file has a valid PID, even on
	// the happy path. The daemon's agent.register handler (Fix A in
	// thrum-pxz.14) is a no-op when the stored PID already matches, so
	// the happy-path cost is just one local socket RPC (~1ms). When the
	// DB is stale (legacy data from before this feature, rebuild from
	// events, or recovery scenarios), this ensures the DB catches up
	// without requiring an explicit drift event from the client.
	//
	// DaemonUpdated is set to true only when the client actually caused
	// a state change — i.e., when FileChanged included "agent_pid" or
	// "runtime". A bare no-op call does not set it.
	//
	// Anti-Pattern 4 (silent happy path): this block produces zero log
	// output when nothing drifted. The AgentRegister call is silent on
	// success, and the daemon's matching no-op branch is also silent.
	// The only log emission is the live-conflict guard below.
	if client != nil && idFile.AgentPID > 0 {
		// idFile.AgentPID is already the most current PID by this point:
		// if detectedPID drifted, Step 3 updated idFile.AgentPID in place,
		// so reading it here unconditionally covers both drift and no-drift
		// paths without branching.
		regResp, regErr := AgentRegister(client, AgentRegisterOptions{
			Name:       idFile.Agent.Name,
			Role:       idFile.Agent.Role,
			Module:     idFile.Agent.Module,
			Display:    idFile.Agent.Display,
			AgentPID:   idFile.AgentPID,
			ReRegister: false,
		})
		if regErr != nil {
			return result, fmt.Errorf("re-register with daemon: %w", regErr)
		}
		if regResp != nil && regResp.Status == "conflict" && regResp.Conflict != nil {
			// Live-conflict guard: if a DIFFERENT, still-running PID
			// owns this name, warn and bail out without marking the
			// daemon as updated. The file is already saved with our
			// detected PID — this is intentional: the client state
			// is authoritative locally, but we refuse to steal the
			// name in the daemon's view.
			cp := regResp.Conflict.ConflictPID
			if cp > 0 && cp != idFile.AgentPID && process.IsRunning(cp) {
				fmt.Fprintf(os.Stderr, "thrum: refusing to overwrite live agent %q at PID %d\n", idFile.Agent.Name, cp)
				return result, nil
			}
		}
		// Session resurrect surfacing (thrum-xir.18): if the daemon's
		// register handler emitted a fresh session.start because the
		// agent had no active session, propagate the flag and new
		// session_id so callers can observe the recovery without
		// re-querying whoami.
		if regResp != nil && regResp.SessionResumed {
			result.SessionResumed = true
			result.ResumedSessionID = regResp.SessionID
		}
		for _, f := range result.FileChanged {
			if f == "agent_pid" || f == "runtime" {
				result.DaemonUpdated = true
				break
			}
		}
	}

	return result, nil
}

// loadGuardConfig reads .thrum/config.json's identity_guard block
// from repoPath and merges it over guard.DefaultConfig(). Missing
// config is a legitimate first-run state — fall back to defaults
// silently rather than refusing. This is the client-side seed;
// Epic 6 adds the daemon>repo>defaults layered precedence.
func loadGuardConfig(repoPath string) guard.Config {
	effective := paths.EffectiveRepoPath(repoPath)
	cfgPath := filepath.Join(effective, ".thrum", "config.json")
	// #nosec G304 -- cfgPath is derived from the repo's own .thrum/.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return guard.DefaultConfig()
	}
	var tc struct {
		IdentityGuard *json.RawMessage `json:"identity_guard,omitempty"`
	}
	if err := json.Unmarshal(data, &tc); err != nil {
		return guard.DefaultConfig()
	}
	repoCfg, _ := guard.ParseConfigFromRaw(tc.IdentityGuard)
	return guard.Merge(guard.DefaultConfig(), repoCfg, guard.Config{})
}

// isNoIdentityFile returns true when err indicates "no identity file was
// found at repoPath" — a legitimate non-error case for refresh.
//
// The two sentinels from loadIdentityFromDir in internal/config/config.go:
//   - the identities directory does not exist → wraps os.ErrNotExist
//   - the directory exists but contains no .json files → "no identity files found"
func isNoIdentityFile(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return strings.Contains(err.Error(), "no identity files found")
}
