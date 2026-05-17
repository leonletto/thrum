package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/daemon/permission"
	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
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
	DB             *sql.DB
	PendingAfter   time.Duration
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

	worktrees, err := enumerateRepoWorktrees(ctx, opts.RepoPath)
	if err != nil {
		// A missing-or-malformed `git worktree list` result is recoverable:
		// the substrate can still serve list/show/promote against the
		// main worktree; we just won't fan mirror events anywhere.
		// Surface as a warning, not a hard error.
		worktrees = []string{opts.RepoPath}
	}

	destinations := destinationsForWorktrees(worktrees)
	sourceRoot := filepath.Join(opts.RepoPath, ".thrum", "skills")

	worker := mirror.New(mirror.WorkerOpts{
		SourceRoot:   sourceRoot,
		Destinations: destinations,
	})
	if err := worker.Start(ctx); err != nil {
		return nil, fmt.Errorf("start mirror worker: %w", err)
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
func newSkillChainResolver(db *sql.DB) skills.ChainResolver {
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

// enumerateRepoWorktrees parses `git worktree list --porcelain` and
// returns every worktree path. Matches the parser at line 3581 in the
// CLI worktree-list command — extracted as a helper so the daemon
// boot path can share the logic without duplicating the parse.
//
// A missing or empty result is NOT an error: callers can fall back to
// "just the main worktree" if the git invocation fails. The caller
// here is the substrate builder, and a slot-empty result still lets
// list/show/promote work against the main repo.
func enumerateRepoWorktrees(ctx context.Context, repoPath string) ([]string, error) {
	out, err := safecmd.Git(ctx, repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, p)
		}
	}
	return paths, nil
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
