// Package pending implements an in-memory pool of orphaned messages that
// could not be fully projected because one or more referenced state entities
// (bridge groups, agents) had not yet arrived on this clone.
//
// The pool is rebuilt from the SQLite projection on daemon restart; it is
// never persisted to disk. Re-attempt logic is purely external — callers
// drive resolution by calling ResolveOnStateLand whenever new state files
// land on the a-sync branch.
package pending

import (
	"context"
	"sync"
	"time"
)

// Pool holds messages that reference state files (e.g., bridge groups) not
// yet present on this clone. Re-resolves on every state-file land.
// In-memory only; rebuilt from SQLite projection on daemon restart.
type Pool struct {
	mu      sync.Mutex
	orphans map[string]OrphanedMessage // keyed by MessageID
}

// New returns an initialised, empty Pool.
func New() *Pool {
	return &Pool{
		orphans: make(map[string]OrphanedMessage),
	}
}

// Add stashes a message that couldn't be fully resolved at projection time.
// blockedBy lists the missing references (group_id, agent_id, etc.).
// If a message with the same MessageID is already in the pool, Add is a no-op.
func (p *Pool) Add(msg OrphanedMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.orphans[msg.MessageID]; exists {
		return
	}
	p.orphans[msg.MessageID] = msg
	// TODO E8 telemetry: slog.Info("pending_pool.added", "message_id", msg.MessageID, "blocked_by", msg.BlockedBy)
}

// ResolveOnStateLand is called by the projection after applying any new
// state-file land. It re-attempts every orphan whose BlockedBy intersects
// the newly-known IDs. Removes resolved orphans from the pool.
// Returns the count of orphans resolved (i.e., removed because Resolver
// returned (true, nil)).
//
// Resolution semantics:
//   - If an orphan's BlockedBy has any element that is also in newKnownIDs,
//     Resolver.Resolve is called. The resolver is the authoritative source
//     on whether all prerequisites are satisfied.
//   - Only (true, nil) from the resolver marks an orphan as resolved and
//     removes it from the pool.
//   - (false, nil) or (false, non-nil error) leaves the orphan in the pool.
//   - (true, non-nil error) is treated as failure (not removed); callers
//     should not return a non-nil error alongside true per spec §5.4.
func (p *Pool) ResolveOnStateLand(ctx context.Context, newKnownIDs []string, resolver Resolver) int {
	if len(newKnownIDs) == 0 {
		return 0
	}

	// Build a lookup set for O(1) intersection checks.
	known := make(map[string]struct{}, len(newKnownIDs))
	for _, id := range newKnownIDs {
		known[id] = struct{}{}
	}

	p.mu.Lock()
	// Snapshot candidates while holding the lock, then release before
	// calling the resolver (which may acquire other locks or do I/O).
	candidates := make([]OrphanedMessage, 0)
	for _, orphan := range p.orphans {
		if intersects(orphan.BlockedBy, known) {
			candidates = append(candidates, orphan)
		}
	}
	p.mu.Unlock()

	resolved := 0
	for _, orphan := range candidates {
		ok, err := resolver.Resolve(ctx, orphan)
		if ok && err == nil {
			p.mu.Lock()
			delete(p.orphans, orphan.MessageID)
			p.mu.Unlock()
			resolved++
			// TODO E8 telemetry: slog.Info("pending_pool.resolved", "message_id", orphan.MessageID, "wait_ms", time.Since(orphan.LandedAt).Milliseconds())
		}
	}
	return resolved
}

// Size returns the current orphan count (for telemetry / debugging).
func (p *Pool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.orphans)
}

// List returns a snapshot copy of all current orphans. Order is not
// guaranteed (map iteration). Returned slice is safe to use after
// release of the pool's mutex — entries are value-copied.
//
// Intended for the sync.pending_pool.list diagnostics RPC; do NOT
// rely on this for live pool queries inside the projector hot path
// (use ResolveOnStateLand which performs the targeted lookup under
// a single mutex acquisition).
func (p *Pool) List() []OrphanedMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]OrphanedMessage, 0, len(p.orphans))
	for _, o := range p.orphans {
		out = append(out, o)
	}
	return out
}

// intersects reports whether any element of blockedBy is present in known.
func intersects(blockedBy []string, known map[string]struct{}) bool {
	for _, b := range blockedBy {
		if _, ok := known[b]; ok {
			return true
		}
	}
	return false
}

// OrphanedMessage is a message that arrived referencing state entities not
// yet present on this clone.
type OrphanedMessage struct {
	MessageID  string
	AuthorID   string
	Recipients []string
	BlockedBy  []string // group_id / agent_id references not yet on this clone
	LandedAt   time.Time
}

// Resolver checks if all of msg.BlockedBy are now resolvable; if so, applies
// the message to the projection and returns true.
// Only (true, nil) indicates success. Any other combination leaves the orphan
// in the pool.
type Resolver interface {
	Resolve(ctx context.Context, msg OrphanedMessage) (bool, error)
}
