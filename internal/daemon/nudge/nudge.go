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
	"sync"

	"github.com/leonletto/thrum/internal/config"
	ttmux "github.com/leonletto/thrum/internal/tmux"
)

// Keystroke / pane seams. Production points at the real tmux helpers; tests
// substitute fakes (mirrors internal/tmux/nudge.go's nudgeSendKeys pattern).
// capturePaneLines is the tail size DispatchTmux reads to decide safe-to-type —
// 30 matches the permission SessionPoller's CaptureLines so both see the same
// active region.
const capturePaneLines = 30

// dispatchPoolSize bounds how many recipient nudges DispatchTmux processes
// concurrently. Before thrum-yz0a the dispatch spawned one goroutine PER
// recipient with no cap, so a broadcast with N recipients forked N concurrent
// goroutines — each spawning tmux subprocesses (has-session + capture-pane
// quiet-gate polls). Under a message burst that fan-out became fork-bomb
// adjacent. A small fixed pool caps the concurrent tmux-subprocess load
// regardless of recipient count or burst depth; nudges are advisory, so the
// modest serialisation a full pool introduces is an acceptable trade for
// bounded process-table pressure.
const dispatchPoolSize = 8

var (
	hasSessionFn  = ttmux.HasSession
	capturePaneFn = ttmux.CapturePane
)

// realNudge is the production keystroke-injection target for nudgeFn (deferred.go).
func realNudge(target, sender string) error { return ttmux.Nudge(target, sender) }

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
func DispatchTmux(ctx context.Context, thrumDir string, recipients []string, senderName string) {
	if thrumDir == "" || len(recipients) == 0 {
		slog.Info("[nudge] nudge.DispatchTmux skip empty",
			"sender", senderName,
			"thrum_dir_empty", thrumDir == "",
			"recipient_count", len(recipients),
		)
		return
	}

	// thrum-1zfk: never nudge the sender's own pane. HandleSend now
	// intentionally KEEPS the author in the recipients list (see HandleSend's
	// recipientSet loop in rpc/message.go: "Keep author in recipientSet on
	// explicit agent or role mention that resolves to author") so the projector
	// can stamp read_at on the self-delivery row. That invariant change (commit
	// c6b2072e04, 2026-05-16) broke the unguarded assumption here. Mirror the
	// spool-dispatcher guard in cmd/thrum/main.go's SetOnEventWrite closure so
	// the tmux-nudge path is symmetric defense-in-depth.
	//
	// The self-skip filter runs SYNCHRONOUSLY in the caller's goroutine so its
	// 'tmux.skip self' log is observable the instant DispatchTmux returns (the
	// thrum-1zfk regression tests assert this without waiting on background
	// goroutines). Only the surviving recipients are handed to the bounded
	// async pool below.
	work := make([]string, 0, len(recipients))
	for _, recipientName := range recipients {
		if recipientName == senderName {
			slog.Info("[nudge] tmux.skip self",
				"site", "nudge.DispatchTmux",
				"sender", senderName,
				"recipient", recipientName,
			)
			continue
		}
		work = append(work, recipientName)
	}
	if len(work) == 0 {
		return
	}

	// thrum-yz0a: bounded recipient fan-out. Previously this spawned one
	// goroutine PER recipient with no cap — a broadcast with N recipients forked
	// N concurrent goroutines, each spawning tmux subprocesses (has-session +
	// capture-pane quiet-gate polls). Under a message burst that fan-out became
	// fork-bomb adjacent. A single dispatcher goroutine now feeds the recipients
	// through a bounded semaphore so at most dispatchPoolSize nudges run
	// concurrently. DispatchTmux stays fire-and-forget: it returns immediately
	// after launching the dispatcher (the semaphore send blocks the dispatcher,
	// never the caller).
	go func() {
		sem := make(chan struct{}, dispatchPoolSize)
		var wg sync.WaitGroup
		for _, name := range work {
			sem <- struct{}{}
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				defer func() { <-sem }()
				dispatchOne(ctx, thrumDir, name, senderName)
			}(name)
		}
		wg.Wait()
	}()
}

// dispatchOne resolves a single recipient's pane and fires (or defers/drops)
// its nudge. Extracted from DispatchTmux's per-recipient loop so the bounded
// pool has a single unit of work to run (thrum-yz0a).
func dispatchOne(ctx context.Context, thrumDir, name, senderName string) {
	target, runtime := resolveTargetAndRuntime(thrumDir, name)
	if target == "" {
		slog.Info("[nudge] nudge.DispatchTmux no-target",
			"sender", senderName,
			"recipient", name,
		)
		return
	}
	session, _, _ := ttmux.ParseTarget(target)
	if !hasSessionFn(session) {
		slog.Info("[nudge] nudge.DispatchTmux dead-session",
			"sender", senderName,
			"recipient", name,
			"target", target,
			"session", session,
		)
		return
	}
	// Chrome-quiet gate (thrum-nlel / thrum-3i2s) composed with the thrum-7phu
	// dialog gate: poll until the input chrome is quiet (no human typing),
	// spinner-permissive. The gate also owns the 7phu dialog check (re-checked
	// every poll) — a dialog defers to the RedeliverIfSafe queue; a
	// daemon-shutdown ctx drops the poke (the spool still carries the message).
	// See quiet_gate.go.
	switch paneQuietForNudge(ctx, thrumDir, target, runtime) {
	case nudgeDefer:
		DeferNudge(session, target, senderName)
		return
	case nudgeDrop:
		slog.Info("[nudge] nudge.DispatchTmux dropped (ctx cancelled during chrome-quiet wait)",
			"sender", senderName, "recipient", name, "target", target, "session", session,
		)
		return
	case nudgeFire:
		slog.Info("[nudge] nudge.DispatchTmux fire",
			"sender", senderName,
			"recipient", name,
			"target", target,
			"session", session,
		)
		_ = nudgeFn(target, senderName)
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
	target, _ := resolveTargetAndRuntime(thrumDir, agentName)
	return target
}

// resolveTargetAndRuntime is ResolveTarget plus the agent's runtime (claude,
// codex, …) from the same identity file, so the caller can consult
// permission.IsPaneSafeToType without a second scan (thrum-7phu). Returns
// ("", "") when no identity with a tmux session is found. The first identity
// file carrying a non-empty TmuxSession wins, and its Runtime is returned
// alongside (may be "" for a pre-quickstart/legacy identity — callers treat an
// empty runtime as "generic detection only", matching DetectPaneState).
func resolveTargetAndRuntime(thrumDir, agentName string) (target, runtime string) {
	// Check main repo identity dir first.
	if t, rt := readTmuxAndRuntime(filepath.Join(thrumDir, "identities"), agentName); t != "" {
		return t, rt
	}

	// Fall through to worktree identity dirs.
	repoDir := filepath.Dir(thrumDir)
	for _, wtPath := range cachedWorktreePaths(repoDir) {
		if wtPath == repoDir {
			continue // already checked
		}
		idDir := filepath.Join(wtPath, ".thrum", "identities")
		if t, rt := readTmuxAndRuntime(idDir, agentName); t != "" {
			return t, rt
		}
	}
	return "", ""
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
	for _, wtPath := range cachedWorktreePaths(repoDir) {
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
	for _, wtPath := range cachedWorktreePaths(repoDir) {
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

// readTmuxAndRuntime loads <identitiesDir>/<agentName>.json and returns the
// TmuxSession + Runtime fields, or ("", "") on any error (including
// file-not-found, the common case when an agent isn't registered in this
// particular worktree). Runtime is only meaningful when TmuxSession is non-empty.
func readTmuxAndRuntime(identitiesDir, agentName string) (target, runtime string) {
	p := identityPath(identitiesDir, agentName)
	if p == "" {
		return "", ""
	}
	data, err := os.ReadFile(p) // #nosec G304 -- path is .thrum/identities/<name>.json
	if err != nil {
		return "", ""
	}
	var idFile config.IdentityFile
	if err := json.Unmarshal(data, &idFile); err != nil {
		return "", ""
	}
	rt := idFile.Runtime
	if rt == "" {
		rt = idFile.PreferredRuntime // mirror config.Load's fallback
	}
	return idFile.TmuxSession, rt
}
