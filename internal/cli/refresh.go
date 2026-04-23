package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/process"
	ttmux "github.com/leonletto/thrum/internal/tmux"
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

	// Step 1: DETECT
	detectedPID, detectedRuntime := detectAncestor(ctx)

	tmuxTarget := ""
	if ttmux.InTmux() {
		if target, err := ttmux.PaneTarget(); err == nil {
			tmuxTarget = target
		}
	}

	branch := ""
	effectiveRepo := paths.EffectiveRepoPath(repoPath)
	if out, err := safecmd.Git(ctx, effectiveRepo, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = trimNewline(string(out))
	}

	// Step 2: LOAD
	idFile, _, err := config.LoadIdentityWithPath(repoPath)
	if err != nil {
		// No identity file → (nil, nil). Any other error propagates.
		if isNoIdentityFile(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load identity: %w", err)
	}
	if idFile == nil {
		return nil, nil
	}

	// Resolve the .thrum directory for SaveIdentityFile. Use the same
	// effective repo path LoadIdentityWithPath uses so reads and writes
	// always target the same location.
	thrumDir := filepath.Join(effectiveRepo, ".thrum")

	result := &RefreshResult{
		DetectedPID:     detectedPID,
		DetectedRuntime: detectedRuntime,
	}

	// Step 3: DIFF + mutate idFile in place.
	//
	// Runtime vs PreferredRuntime: the two fields are tracked independently
	// because only PreferredRuntime is observed by the daemon-reconcile
	// branch in Step 5. A `--runtime` flag override on `thrum quickstart`
	// sets PreferredRuntime directly (user intent) without implying a
	// matching process-tree Runtime. In the refresh path, however, we do
	// want both to follow the detected runtime: if the agent's live
	// ancestor is codex, both Runtime and PreferredRuntime should reflect
	// that. The dual-update below keeps them in sync for detection-driven
	// writes while leaving user-intent writes in quickstart untouched.
	if detectedPID > 0 && idFile.AgentPID != detectedPID {
		idFile.AgentPID = detectedPID
		result.FileChanged = append(result.FileChanged, "agent_pid")
	}
	if detectedRuntime != "" && idFile.Runtime != detectedRuntime {
		idFile.Runtime = detectedRuntime
		result.FileChanged = append(result.FileChanged, "runtime")
	}
	if detectedRuntime != "" && idFile.PreferredRuntime != detectedRuntime {
		idFile.PreferredRuntime = detectedRuntime
		result.FileChanged = append(result.FileChanged, "preferred_runtime")
	}
	if tmuxTarget != "" && idFile.TmuxSession != tmuxTarget {
		idFile.TmuxSession = tmuxTarget
		result.FileChanged = append(result.FileChanged, "tmux_session")
	}
	if branch != "" && idFile.Branch != branch {
		idFile.Branch = branch
		result.FileChanged = append(result.FileChanged, "branch")
	}

	// Step 4: PERSIST FILE — must happen before Step 5 so the file is
	// authoritative even if the daemon round-trip fails.
	if len(result.FileChanged) > 0 {
		if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
			return result, fmt.Errorf("save identity: %w", err)
		}
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

// trimNewline removes a single trailing newline without touching interior
// whitespace. Used for git output cleanup where strings.TrimSpace would
// be too aggressive.
func trimNewline(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
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
