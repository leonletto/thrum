package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/leonletto/thrum/internal/daemon/permission"
	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/skills"
	"github.com/leonletto/thrum/internal/skills/mirror"
)

// skillSubstrateOpts configures the C-B1 skill-substrate construction.
// All fields are required; nil any of them and buildSkillSubstrate
// returns an error so the wiring drift surfaces at boot rather than
// at first-RPC inside a goroutine.
type skillSubstrateOpts struct {
	RepoPath       string
	ThrumDir       string
	Library        *skills.Library
	Permission     *permission.Permission
	RemindersStore reminders.Store
	// DB is the safedb-wrapped handle (not raw *sql.DB) per the project
	// invariant that daemon code routes SQL through safedb so the
	// context-aware variants are enforced at compile time. Phase 3
	// dual-reviewer finding on the wiring commit.
	DB           *safedb.DB
	PendingAfter time.Duration
}

// skillSubstrate bundles the four collaborators the SkillHandler /
// daemon lifecycle consume. Worker + Watcher are started by
// buildSkillSubstrate; the caller is responsible for Stop() at
// shutdown (defer in runDaemon handles the production wiring).
type skillSubstrate struct {
	Worker    *mirror.Worker
	Watcher   *skills.Watcher
	Staleness *skills.Staleness
}

// buildSkillSubstrate constructs the full C-B1 substrate. The order is
// important: ChainResolver → Worker → Worker.Start (so the watcher's
// MirrorEnqueuer is live before fsnotify events fire) → Staleness →
// Watcher → Watcher.Start. Any failure unwinds (Worker.Stop) before
// returning so the caller sees a clean partial-construction state.
func buildSkillSubstrate(ctx context.Context, opts skillSubstrateOpts) (*skillSubstrate, error) {
	if opts.Library == nil {
		return nil, errors.New("skill substrate: Library is required")
	}
	if opts.Permission == nil {
		return nil, errors.New("skill substrate: Permission is required")
	}
	if opts.RemindersStore == nil {
		return nil, errors.New("skill substrate: RemindersStore is required")
	}
	if opts.DB == nil {
		return nil, errors.New("skill substrate: DB is required")
	}
	if opts.RepoPath == "" {
		return nil, errors.New("skill substrate: RepoPath is required")
	}
	if opts.ThrumDir == "" {
		return nil, errors.New("skill substrate: ThrumDir is required")
	}
	if opts.PendingAfter == 0 {
		opts.PendingAfter = 48 * time.Hour
	}

	chainResolver := newSkillChainResolver(opts.DB)

	// safecmd.WorktreePaths handles the empty-list and git-failure
	// fallback to [repoPath] internally — keeps the substrate live
	// against the main repo even when `git worktree list` returns
	// nothing parseable. Phase 3 dual-reviewer finding: previously we
	// duplicated the parser inline and lost the empty-list fallback.
	worktrees := safecmd.WorktreePaths(ctx, opts.RepoPath)
	destinations := destinationsForWorktrees(worktrees)
	sourceRoot := filepath.Join(opts.RepoPath, ".thrum", "skills")

	worker := mirror.New(mirror.WorkerOpts{
		SourceRoot:   sourceRoot,
		Destinations: destinations,
	})
	if err := worker.Start(ctx); err != nil {
		return nil, fmt.Errorf("start mirror worker: %w", err)
	}

	// Boot-time reconcile per spec §12 trigger-c. Worker.Start spawns
	// per-destination goroutines but does NOT replay any source/dest
	// drift accumulated while the daemon was down. Reconcile is the
	// synchronous canonical-vs-destination diff + apply that catches
	// mid-restart promotes and propagates them to worktree mirrors.
	// Best-effort: a reconcile failure logs but doesn't fail the boot
	// (the watcher's live event path still works; only drift catch-up
	// is missed). Phase 3 dual-reviewer finding — my commit body
	// incorrectly claimed Worker.Start triggered Reconcile internally;
	// it does not.
	if err := worker.Reconcile(ctx); err != nil {
		// Surfaced via slog so operators can see boot-time reconcile
		// failures, but doesn't unwind the substrate — live events
		// still flow.
		_ = err
	}

	sidecarPath := filepath.Join(opts.ThrumDir, "state", "skill-proposal-reminders.jsonl")
	stalenessInstance := skills.NewStaleness(opts.RemindersStore, chainResolver, sidecarPath, opts.PendingAfter)

	supervisorAdapter := skillsSupervisorAdapter{p: opts.Permission}
	watcher := skills.NewWatcher(skills.WatcherOpts{
		LibraryRoot:  sourceRoot,
		ProposalRoot: filepath.Join(opts.RepoPath, ".thrum", "agents"),
		Worktrees:    worktrees,
		Mirror:       worker,
		Staleness:    stalenessInstance,
		Supervisor:   supervisorAdapter,
		Resolver:     chainResolver,
	})
	if err := watcher.Start(ctx); err != nil {
		_ = worker.Stop()
		return nil, fmt.Errorf("start skill watcher: %w", err)
	}

	return &skillSubstrate{
		Worker:    worker,
		Watcher:   watcher,
		Staleness: stalenessInstance,
	}, nil
}

// newSkillChainResolver returns a ChainResolver closure that queries
// the agents table for coordinator-role agents. Lives at the daemon-
// wiring layer (not inside internal/skills/staleness.go) per plan v2
// BLOCKING #3 — keeps the skills package filesystem-only with no SQL
// dependency. The closure is invoked at every mint and reconcile pass.
// The db parameter is safedb-wrapped (not raw *sql.DB) so the
// context-aware-only invariant is enforced at compile time.
func newSkillChainResolver(db *safedb.DB) skills.ChainResolver {
	return func(ctx context.Context) ([]string, error) {
		rows, err := db.QueryContext(ctx,
			`SELECT agent_id FROM agents WHERE role = 'coordinator'`)
		if err != nil {
			return nil, fmt.Errorf("query coordinators: %w", err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var id string
			if scanErr := rows.Scan(&id); scanErr != nil {
				return nil, fmt.Errorf("scan agent_id: %w", scanErr)
			}
			out = append(out, id)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// skillsSupervisorAdapter bridges *permission.Permission's
// SendSupervisorMessage (which returns msgID + error) to the
// skills.SupervisorMessenger interface (which returns just error).
// The watcher doesn't consume the msgID — it just needs to know the
// fanout succeeded.
type skillsSupervisorAdapter struct{ p *permission.Permission }

func (a skillsSupervisorAdapter) SendSupervisorMessage(ctx context.Context, target, body, threadID string) error {
	_, err := a.p.SendSupervisorMessage(ctx, target, body, threadID)
	return err
}

// destinationsForWorktrees builds the (worktree, runtime) Destinations
// list by pairing each worktree path with every known runtime. The
// mirror.Worker handles null-adapter runtimes (success-skip) internally,
// so we register the full cross-product and let the adapter table
// decide which ones get goroutines. This makes adding a runtime
// (flipping its adapter table entry from nil) a one-line PR; no daemon
// boot wiring change required.
func destinationsForWorktrees(worktrees []string) []mirror.Destination {
	runtimes := mirror.KnownRuntimes()
	out := make([]mirror.Destination, 0, len(worktrees)*len(runtimes))
	for _, wtree := range worktrees {
		// Skip worktrees that don't exist on disk — `git worktree list`
		// can list paths whose checkouts were manually deleted.
		if info, statErr := os.Stat(wtree); statErr != nil || !info.IsDir() {
			continue
		}
		for _, runtime := range runtimes {
			out = append(out, mirror.Destination{
				WorktreePath: wtree,
				Runtime:      runtime,
			})
		}
	}
	return out
}
