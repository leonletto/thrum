// Package nudge dispatches tmux pane "nudges" (visible bell + brief
// status injection) for incoming messages. It is the single source of
// truth for how the daemon notifies a tmux-managed agent that a new
// message has arrived.
//
// Background (thrum-wvpv): before this package existed, nudge dispatch
// was inlined inside HandleSend (internal/daemon/rpc/message.go), so it
// only ran on the local RPC write path. Messages arriving via Tailscale
// peer sync or the cross-repo bridge went through a different code path
// (sync_apply.applyEvent → State.WriteEvent → hook) which never called
// the inline nudge block. Cross-machine recipients silently never got
// notified — sync worked, projection worked, the message was in the DB,
// but the recipient's tmux pane stayed dark.
//
// The fix routes both paths through the SetOnEventWrite hook (wired in
// cmd/thrum/main.go). The hook already runs for every event write, both
// local and synced. This package provides the dispatch logic the hook
// invokes.
package nudge

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	ttmux "github.com/leonletto/thrum/internal/tmux"
)

// DispatchTmux fires asynchronous tmux nudges for every recipient in the
// list. Each recipient is resolved to a tmux pane via the on-disk
// identity file; recipients without a registered tmux session are
// silently skipped (legitimate — a CLI-only agent has no pane to nudge).
//
// ThrumDir is the daemon's primary .thrum directory (e.g.
// /Users/leon/dev/opensource/thrum/.thrum). The resolver walks
// worktree identity dirs from there.
//
// SenderName is shown in the nudge string ("[thrum] @sender") so the
// recipient can see who pinged them at a glance.
//
// This function is fire-and-forget: it spawns a goroutine per recipient
// and returns immediately. Failures are intentionally swallowed because
// nudges are advisory — losing one is acceptable, blocking the event
// pipeline on a slow tmux is not.
func DispatchTmux(thrumDir string, recipients []string, senderName string) {
	if thrumDir == "" || len(recipients) == 0 {
		slog.Info("[nudge] nudge.DispatchTmux skip empty",
			"sender", senderName,
			"thrum_dir_empty", thrumDir == "",
			"recipient_count", len(recipients),
		)
		return
	}
	for _, recipientName := range recipients {
		// thrum-1zfk: never nudge the sender's own pane. HandleSend now
		// intentionally KEEPS the author in the recipients list (per
		// rpc/message.go:586 "Keep author in recipientSet on explicit
		// agent or role mention that resolves to author") so the projector
		// can stamp read_at on the self-delivery row. That invariant change
		// (commit c6b2072e04, 2026-05-16) broke the unguarded assumption
		// here. Mirror the spool-dispatcher guard in cmd/thrum/main.go:5963
		// so the tmux-nudge path is symmetric defense-in-depth.
		if recipientName == senderName {
			slog.Info("[nudge] tmux.skip self",
				"site", "nudge.DispatchTmux",
				"sender", senderName,
				"recipient", recipientName,
			)
			continue
		}
		go func(name string) {
			target := ResolveTarget(thrumDir, name)
			if target == "" {
				slog.Info("[nudge] nudge.DispatchTmux no-target",
					"sender", senderName,
					"recipient", name,
				)
				return
			}
			session, _, _ := ttmux.ParseTarget(target)
			if !ttmux.HasSession(session) {
				slog.Info("[nudge] nudge.DispatchTmux dead-session",
					"sender", senderName,
					"recipient", name,
					"target", target,
					"session", session,
				)
				return
			}
			slog.Info("[nudge] nudge.DispatchTmux fire",
				"sender", senderName,
				"recipient", name,
				"target", target,
				"session", session,
			)
			_ = ttmux.Nudge(target, senderName)
		}(recipientName)
	}
}

// ResolveTarget reads the identity file for an agent and returns its
// tmux target ("session:window.pane"), or "" if the agent has no tmux
// session registered.
//
// The lookup walks every git worktree under the repo, checking
// .thrum/identities/<agentName>.json in each, so an agent registered
// in any worktree on this machine is resolvable.
func ResolveTarget(thrumDir, agentName string) string {
	// Check main repo identity dir first.
	if target := readTmuxFromIdentity(filepath.Join(thrumDir, "identities"), agentName); target != "" {
		return target
	}

	// Fall through to worktree identity dirs.
	repoDir := filepath.Dir(thrumDir)
	for _, wtPath := range safecmd.WorktreePaths(context.Background(), repoDir) {
		if wtPath == repoDir {
			continue // already checked
		}
		idDir := filepath.Join(wtPath, ".thrum", "identities")
		if target := readTmuxFromIdentity(idDir, agentName); target != "" {
			return target
		}
	}
	return ""
}

// HasLocalIdentity reports whether the named agent has an identity
// file reachable from this daemon (main identities dir OR any worktree
// identities dir). True means "local recipient" for the purpose of
// local-only operations like spool writes.
func HasLocalIdentity(thrumDir, agentName string) bool {
	if identityPath(filepath.Join(thrumDir, "identities"), agentName) != "" {
		return true
	}
	repoDir := filepath.Dir(thrumDir)
	for _, wtPath := range safecmd.WorktreePaths(context.Background(), repoDir) {
		if wtPath == repoDir {
			continue
		}
		if identityPath(filepath.Join(wtPath, ".thrum", "identities"), agentName) != "" {
			return true
		}
	}
	return false
}

// LocalAgentNames returns every agent whose identity file is reachable
// from this daemon's filesystem (main identities dir + any worktree
// identities dir). Used by the inbox janitor to enumerate local
// agents without a hostname comparison.
func LocalAgentNames(thrumDir string) []string {
	seen := map[string]struct{}{}
	scan := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if base, ok := strings.CutSuffix(e.Name(), ".json"); ok {
				seen[base] = struct{}{}
			}
		}
	}
	scan(filepath.Join(thrumDir, "identities"))
	repoDir := filepath.Dir(thrumDir)
	for _, wtPath := range safecmd.WorktreePaths(context.Background(), repoDir) {
		if wtPath == repoDir {
			continue
		}
		scan(filepath.Join(wtPath, ".thrum", "identities"))
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	return out
}

// identityPath returns the full path to <dir>/<agentName>.json if the
// file exists, else "". Factored out so ResolveTarget's existing
// worktree-walk and HasLocalIdentity share one source of truth for
// the per-dir existence probe.
func identityPath(dir, agentName string) string {
	p := filepath.Join(dir, agentName+".json") // #nosec G304 -- path is .thrum/identities/<name>.json
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// readTmuxFromIdentity loads <identitiesDir>/<agentName>.json and
// returns the TmuxSession field, or "" on any error (including
// file-not-found, which is the common case when an agent isn't
// registered in this particular worktree).
func readTmuxFromIdentity(identitiesDir, agentName string) string {
	p := identityPath(identitiesDir, agentName)
	if p == "" {
		return ""
	}
	data, err := os.ReadFile(p) // #nosec G304 -- path is .thrum/identities/<name>.json
	if err != nil {
		return ""
	}
	var idFile config.IdentityFile
	if err := json.Unmarshal(data, &idFile); err != nil {
		return ""
	}
	return idFile.TmuxSession
}
