package sessionarchive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	agentpkg "github.com/leonletto/thrum/internal/agent"
)

// Opts carries the optional knobs for Archive. Both fields are
// optional; nil values trigger their package-default behavior.
//
// Logger captures filesystem-side warnings (chmod / chtimes /
// empty-snapshot remove failures) that should not block the archive
// itself but DO need operator visibility. nil → slog.Default()
// (which inherits the cli sloghint bridge in CLI command paths).
//
// Now is injected by tests to control the time.Now() fallback chain
// (parseSavedAtFrontmatter → mtime → now). Production callers leave
// nil → time.Now.
type Opts struct {
	Logger *slog.Logger
	Now    func() time.Time
}

// ArchiveResult is the spec §3.1 return shape, extended with Content
// for CLI-side prime-context inclusion per the Task 7 adaptation:
//
//	{ archived_path: string | null,
//	  big_picture:   string | null,
//	  content:       string | null }   ← Task 7 addition
//
// Spec §3.6 (Q-Spec-1) was written assuming a daemon-orchestrated
// prime builder; the actual code orchestrates prime CLI-side. To
// preserve the spec's intent (single source of truth on archive
// timing; runtime inject scripts unchanged), the CLI calls the
// session.archive RPC and uses Content as the snapshot body it
// inserts into the prime output — eliminating the prior
// ConsumeInPrime/CleanupConsumed two-step.
//
// Content is nil when ArchivedPath is nil (missing source, 0-byte
// source). Non-nil Content always points at the full pre-archive
// file bytes as UTF-8 string.
type ArchiveResult struct {
	ArchivedPath *string
	BigPicture   *string
	Content      *string
}

// agentMutexes provides per-agent serialization. sync.Map is the
// canonical Go idiom for concurrently-readable maps with cold-write
// initialization — same-agent Archive() calls serialize through
// their dedicated mutex while cross-agent calls proceed in parallel
// (spec §3.4 idempotency + concurrency guarantee).
var agentMutexes sync.Map

func mutexFor(agentID string) *sync.Mutex {
	if m, ok := agentMutexes.Load(agentID); ok {
		// Safe assertion: this map only ever Stores *sync.Mutex (see
		// LoadOrStore below). A panic here would indicate a code bug
		// in this file, not a runtime data issue.
		if mu, ok := m.(*sync.Mutex); ok {
			return mu
		}
	}
	actual, _ := agentMutexes.LoadOrStore(agentID, &sync.Mutex{})
	if mu, ok := actual.(*sync.Mutex); ok {
		return mu
	}
	// Unreachable: LoadOrStore returned a non-*sync.Mutex value, which
	// means something else wrote to agentMutexes. Fail loudly.
	panic("session-archive: agentMutexes contained non-*sync.Mutex value")
}

// Archive moves a /thrum:restart snapshot from srcPath into the
// agent's sessions/ folder, returning the destination path and the
// parsed §1 "Big picture" body.
//
// Return contract: never returns (nil, nil). The first return value
// is always a non-nil *ArchiveResult whose pointer-fields convey
// the present-or-absent state (nil ArchivedPath = no archive
// happened, vs valid path + Content for a successful archive).
// Errors always surface as the second return value with a nil first
// return value.
//
// The caller passes both thrum-root candidates (mainRepoThrumDir +
// worktreeThrumDir). SessionsDir picks per agent.Mode internally.
// At RPC call sites the daemon already carries both as a pair
// (`h.thrumDir` + per-RPC `wtThrumDir`).
//
// Behavior matrix per spec §3.2:
//
//	srcPath missing                   → ({nil, nil}, nil)        — not an error
//	0-byte file                       → ({nil, nil}, nil) + remove the empty src
//	saved_at frontmatter parses       → use it for filename + mtime
//	saved_at missing / malformed      → fall back to source mtime
//	source mtime is zero              → fall back to opts.Now() (or time.Now)
//	destination collision (10 retries)→ (nil, error) — caller logs + propagates
//	atomic rename failure             → (nil, error)
//	chmod / chtimes failure           → warn-only via opts.Logger
//
// Concurrency: same-agent calls serialize via mutexFor(agent.AgentID);
// cross-agent calls proceed in parallel. The src→dst move uses
// os.Rename for filesystem-level atomicity — both paths must share
// the same filesystem (true for .thrum/restart/ ↔ .thrum/agents/.../sessions/
// under one .thrum/ root). Cross-filesystem moves surface as a hard
// error rather than silently falling back to copy+delete.
//
// File mode 0600, directory mode 0700 per spec §4.x permission Q.
func Archive(
	ctx context.Context,
	agent agentpkg.Agent,
	srcPath string,
	mainRepoThrumDir, worktreeThrumDir string,
	opts Opts,
) (*ArchiveResult, error) {
	_ = ctx // reserved for future cancellation; src→dst move is fast enough today

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	mu := mutexFor(agent.AgentID)
	mu.Lock()
	defer mu.Unlock()

	info, err := os.Stat(srcPath)
	if errors.Is(err, os.ErrNotExist) {
		return &ArchiveResult{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat snapshot: %w", err)
	}

	if info.Size() == 0 {
		if rmErr := os.Remove(srcPath); rmErr != nil {
			logger.Warn("session-archive: empty snapshot remove failed",
				"agent", agent.AgentID, "src", srcPath, "err", rmErr)
		}
		return &ArchiveResult{}, nil
	}

	content, err := os.ReadFile(srcPath) // #nosec G304 -- srcPath supplied by daemon/RPC caller from its trusted thrum-dir tree
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}

	savedAt := ParseSavedAtFrontmatter(string(content), info.ModTime())
	if savedAt.IsZero() {
		savedAt = nowFn()
	}
	bigPicture := ParseBigPicture(content, false)

	destDir := SessionsDir(agent, mainRepoThrumDir, worktreeThrumDir)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir sessions: %w", err)
	}

	dst, err := UniqueDestPath(destDir, FormatTimestamp(savedAt))
	if err != nil {
		return nil, err
	}

	if err := os.Rename(srcPath, dst); err != nil {
		return nil, fmt.Errorf("atomic rename: %w", err)
	}

	if err := os.Chmod(dst, 0o600); err != nil {
		logger.Warn("session-archive: chmod failed on destination",
			"agent", agent.AgentID, "path", dst, "err", err)
	}

	if err := os.Chtimes(dst, savedAt, savedAt); err != nil {
		logger.Warn("session-archive: chtimes failed on destination",
			"agent", agent.AgentID, "path", dst, "err", err)
	}

	contentStr := string(content)
	result := &ArchiveResult{
		ArchivedPath: &dst,
		Content:      &contentStr,
	}
	if bigPicture != "" {
		result.BigPicture = &bigPicture
	}
	return result, nil
}
