package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// TestFindIdentityForSession_CwdFastPath_NoGit is the thrum-0a9x regression:
// `thrum tmux restart` resolved the session's identity ONLY via the git-based
// worktree enumeration (AllIdentityDirs → safecmd.WorktreePaths → `git
// worktree list`, 5s timeout) — which SILENTLY truncates to the main repo on
// any git error, including timeouts under full-gate host load. The identity
// file existed on disk the whole time, but restart failed "no identity file
// found for session" (scenario 69, 2× under load). The launch side never hit
// this because writeTmuxToIdentity's Pass 0 uses the in-memory sessionCwds
// map — no git subprocess. This test pins the same no-git fast path on the
// LOOKUP side.
//
// Deterministic repro of the truncation shape: thrumDir lives outside any git
// repo, so WorktreePaths' git call fails and the scan sees only the (empty)
// primary identities dir. The session's identity lives in its registered cwd,
// reachable only via the sessionCwds fast path.
func TestFindIdentityForSession_CwdFastPath_NoGit(t *testing.T) {
	tmp := t.TempDir() // not a git repo: the worktree enumeration degrades
	thrumDir := filepath.Join(tmp, "main", ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// The session's worktree, with its sole identity file bound to the session.
	wt := filepath.Join(tmp, "force-wt")
	wtThrum := filepath.Join(wt, ".thrum")
	if err := os.MkdirAll(filepath.Join(wtThrum, "identities"), 0o750); err != nil {
		t.Fatalf("mkdir wt: %v", err)
	}
	idFile := &config.IdentityFile{
		Agent:       config.AgentConfig{Kind: "agent", Name: "force_agent", Role: "tester", Module: "testing"},
		TmuxSession: "force-restart-test:0.0",
		Runtime:     "shell",
		Worktree:    wt,
	}
	if err := config.SaveIdentityFile(wtThrum, idFile); err != nil {
		t.Fatalf("save identity: %v", err)
	}

	h := NewTmuxHandler(thrumDir, nil)
	h.sessionMu.Lock()
	h.sessionCwds["force-restart-test"] = wt
	h.sessionMu.Unlock()

	agentName, got, idDir := h.findIdentityForSession(context.Background(), "force-restart-test")
	if agentName == "" {
		t.Fatal("findIdentityForSession returned empty — the git-truncation blind spot (scenario-69 'no identity file found for session')")
	}
	if agentName != "force_agent" {
		t.Errorf("agentName = %q, want force_agent", agentName)
	}
	if got == nil || got.Runtime != "shell" {
		t.Errorf("idFile = %+v, want Runtime=shell", got)
	}
	if want := filepath.Join(wtThrum, "identities"); idDir != want {
		t.Errorf("idDir = %q, want %q", idDir, want)
	}
}

// TestFindIdentityForSession_CwdFastPath_FallsBackToScan pins that the fast
// path degrades safely: when the session has no cwd mapping (e.g. the daemon
// restarted and the in-memory map is empty), the existing git-backed scan
// still resolves identities reachable from the primary dir.
func TestFindIdentityForSession_CwdFastPath_FallsBackToScan(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	idFile := &config.IdentityFile{
		Agent:       config.AgentConfig{Kind: "agent", Name: "main_agent", Role: "tester", Module: "testing"},
		TmuxSession: "main-session:0.0",
		Runtime:     "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity: %v", err)
	}

	h := NewTmuxHandler(thrumDir, nil)
	// No sessionCwds entry — pure scan path.
	agentName, got, _ := h.findIdentityForSession(context.Background(), "main-session")
	if agentName != "main_agent" || got == nil {
		t.Fatalf("scan fallback broken: agentName=%q idFile=%+v", agentName, got)
	}
}
