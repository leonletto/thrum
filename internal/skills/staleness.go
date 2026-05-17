package skills

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/daemon/reminders"
)

// Staleness owns the proposed-skill reminder lifecycle: mint a reminder
// when a proposal lands in `.thrum/agents/<author>/proposed-skills/`,
// cancel that reminder when the proposal is promoted (E10.4) or removed
// (watcher-detected dir-remove + E10.6 delete). The reminder substrate
// is the A-B4 reminders.Store; this type is the C-B1 sidecar layer
// translating proposal paths to reminder IDs.
//
// Sidecar persistence: the (proposalPath → reminderID) map is appended
// to a JSONL file at the supplied mapPath. Append-on-mint, tombstone-
// on-cancel, compact-on-startup-Reconcile. This file is gitignored —
// it's per-daemon-instance state that the reminders.Store rows already
// authoritatively track.
type Staleness struct {
	store        reminders.Store
	resolver     ChainResolver
	mapPath      string
	pendingAfter time.Duration
	logger       *slog.Logger

	mu       sync.Mutex
	pathToID map[string]string // proposalPath → reminderID (live entries only)
}

// sidecarRecord is one line in the .jsonl sidecar. Append-on-mint
// records carry MintedAt; tombstone-on-cancel records carry
// TombstonedAt (and reference the same path so a startup compact
// pass can drop the live entry).
type sidecarRecord struct {
	Path         string    `json:"path"`
	ReminderID   string    `json:"reminder_id,omitempty"`
	MintedAt     time.Time `json:"minted_at,omitzero"`
	TombstonedAt time.Time `json:"tombstoned_at,omitzero"`
}

// NewStaleness constructs a Staleness rooted at the supplied sidecar
// path. Loads existing entries from the sidecar synchronously so
// post-restart Mint calls for the same path are idempotent. resolver
// is injected — the daemon-startup wiring composes a closure that
// queries the agent registry for coordinator-role agents in the repo
// (the same closure pattern the watcher uses).
func NewStaleness(store reminders.Store, resolver ChainResolver, mapPath string, pendingAfter time.Duration) *Staleness {
	s := &Staleness{
		store:        store,
		resolver:     resolver,
		mapPath:      mapPath,
		pendingAfter: pendingAfter,
		logger:       slog.Default(),
		pathToID:     map[string]string{},
	}
	s.loadSidecar()
	return s
}

// SetLogger swaps the logger — used by tests to capture audit-log
// assertions without going through slog.SetDefault.
func (s *Staleness) SetLogger(l *slog.Logger) {
	if l != nil {
		s.logger = l
	}
}

// MintProposalReminder ensures a reminder exists for the supplied
// proposal path. Idempotent: a second call with the same path returns
// the existing reminder ID without re-minting.
//
// Resolves the coordinator chain via the injected ChainResolver. An
// empty chain logs a warning and returns ("", nil) — no recipient
// means no useful reminder, and surfacing an error would block the
// propose path on an empty-team repo. A resolver error propagates.
func (s *Staleness) MintProposalReminder(ctx context.Context, proposalPath string) (string, error) {
	s.mu.Lock()
	if existing, ok := s.pathToID[proposalPath]; ok {
		s.mu.Unlock()
		return existing, nil
	}
	s.mu.Unlock()

	agents, err := s.resolver(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve coordinator chain: %w", err)
	}
	if len(agents) == 0 {
		s.logger.Warn("skills staleness: empty coordinator chain; skipping mint", "path", proposalPath)
		return "", nil
	}

	author, name := proposedAuthorAndName(proposalPath)
	body := fmt.Sprintf("Skill proposal pending review: %s/%s at %s", author, name, proposalPath)

	now := time.Now().UTC()
	triggerAt := now.Add(s.pendingAfter)
	// MintID requires an "agent" component for the verbal-dictation
	// format; for daemon-source reminders we synthesize one from the
	// skill name so collisions across simultaneous proposals are
	// minimized.
	reminderID := reminders.MintID("skill-" + name)
	rem := &reminders.Reminder{
		ID:          reminderID,
		Source:      reminders.SourceDaemon,
		TriggerKind: reminders.TriggerTime,
		TriggerAt:   &triggerAt,
		TargetChain: agents,
		Body:        body,
		RaisedAt:    now,
		State:       reminders.StateOpen,
	}
	if mintErr := s.store.Mint(ctx, rem); mintErr != nil {
		return "", fmt.Errorf("mint reminder: %w", mintErr)
	}

	s.mu.Lock()
	s.pathToID[proposalPath] = reminderID
	s.mu.Unlock()

	if appendErr := s.appendSidecar(sidecarRecord{
		Path: proposalPath, ReminderID: reminderID, MintedAt: now,
	}); appendErr != nil {
		s.logger.Warn("skills staleness: sidecar append failed", "path", proposalPath, "err", appendErr)
	}
	s.logger.Info("skill staleness reminder minted", "path", proposalPath, "reminder_id", reminderID, "trigger_at", triggerAt)
	return reminderID, nil
}

// CancelProposalReminder retracts the reminder for the supplied path.
// Best-effort: an absent map entry logs a warning and returns nil
// (idempotent — promote and watcher-remove may race; the second caller
// must not fail). On success, appends a tombstone record to the
// sidecar so a startup compact-pass can drop the live entry.
func (s *Staleness) CancelProposalReminder(ctx context.Context, proposalPath string) error {
	s.mu.Lock()
	reminderID, ok := s.pathToID[proposalPath]
	s.mu.Unlock()
	if !ok {
		s.logger.Warn("skills staleness: cancel without mint", "path", proposalPath)
		return nil
	}

	if err := s.store.Cancel(ctx, reminderID, "skill-promote-or-delete"); err != nil {
		return fmt.Errorf("cancel reminder %s: %w", reminderID, err)
	}

	s.mu.Lock()
	delete(s.pathToID, proposalPath)
	s.mu.Unlock()

	if appendErr := s.appendSidecar(sidecarRecord{
		Path: proposalPath, ReminderID: reminderID, TombstonedAt: time.Now().UTC(),
	}); appendErr != nil {
		s.logger.Warn("skills staleness: sidecar tombstone append failed", "path", proposalPath, "err", appendErr)
	}
	s.logger.Info("skill staleness reminder cancelled", "path", proposalPath, "reminder_id", reminderID)
	return nil
}

// ReconcileProposals walks every .thrum/agents/*/proposed-skills/*/
// SKILL.md under libraryRoot's parent dir and mints any missing
// reminders. Used at daemon boot post-A-B4-init per spec §13.1's
// "reconcile pass at start". Also compacts the sidecar: rewrites the
// file from the live pathToID map, dropping tombstones and merged
// records.
func (s *Staleness) ReconcileProposals(ctx context.Context, libraryRoot string) error {
	agentsDir := filepath.Join(libraryRoot, ".thrum", "agents")
	authorDirs, err := filepath.Glob(filepath.Join(agentsDir, "*"))
	if err != nil {
		return fmt.Errorf("glob agents: %w", err)
	}
	for _, authorDir := range authorDirs {
		info, statErr := os.Stat(authorDir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		proposedDir := filepath.Join(authorDir, "proposed-skills")
		entries, readErr := os.ReadDir(proposedDir)
		if readErr != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			skillMd := filepath.Join(proposedDir, e.Name(), "SKILL.md")
			if _, statErr := os.Stat(skillMd); statErr != nil {
				continue
			}
			if _, mintErr := s.MintProposalReminder(ctx, skillMd); mintErr != nil {
				s.logger.Warn("skills staleness reconcile: mint failed", "path", skillMd, "err", mintErr)
			}
		}
	}
	return s.compactSidecar()
}

// loadSidecar reads the sidecar file at startup and rebuilds pathToID
// from the merged record stream (later records win; a tombstone deletes
// the entry). Missing-file is fine; a malformed record is logged and
// skipped (subsequent valid records can still load).
func (s *Staleness) loadSidecar() {
	if s.mapPath == "" {
		return
	}
	f, err := os.Open(s.mapPath) //nolint:gosec // mapPath is daemon-supplied (.thrum/state/skill-proposal-reminders.jsonl)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("skills staleness: sidecar open failed", "path", s.mapPath, "err", err)
		}
		return
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec sidecarRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			s.logger.Warn("skills staleness: sidecar parse failed", "err", err)
			continue
		}
		if !rec.TombstonedAt.IsZero() {
			delete(s.pathToID, rec.Path)
			continue
		}
		if rec.ReminderID != "" {
			s.pathToID[rec.Path] = rec.ReminderID
		}
	}
}

// appendSidecar atomically appends one record. Uses O_APPEND so
// concurrent writes interleave at the line level without corruption
// (POSIX append guarantee).
func (s *Staleness) appendSidecar(rec sidecarRecord) error {
	if s.mapPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.mapPath), 0o750); err != nil {
		return fmt.Errorf("mkdir sidecar dir: %w", err)
	}
	f, err := os.OpenFile(s.mapPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open sidecar: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, marshalErr := json.Marshal(rec)
	if marshalErr != nil {
		return fmt.Errorf("marshal sidecar record: %w", marshalErr)
	}
	if _, writeErr := f.Write(append(data, '\n')); writeErr != nil {
		return fmt.Errorf("write sidecar: %w", writeErr)
	}
	return nil
}

// compactSidecar rewrites the sidecar file with one record per live
// pathToID entry, dropping tombstones and merged history. Atomic via
// temp-file + rename.
func (s *Staleness) compactSidecar() error {
	if s.mapPath == "" {
		return nil
	}
	s.mu.Lock()
	snapshot := make([]sidecarRecord, 0, len(s.pathToID))
	for p, id := range s.pathToID {
		snapshot = append(snapshot, sidecarRecord{Path: p, ReminderID: id, MintedAt: time.Now().UTC()})
	}
	s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.mapPath), 0o750); err != nil {
		return fmt.Errorf("mkdir sidecar dir: %w", err)
	}
	tmp := s.mapPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // G304: tmp is mapPath+".tmp" — same trust boundary as the caller-supplied sidecar path
	if err != nil {
		return fmt.Errorf("open compact tmp: %w", err)
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	for _, rec := range snapshot {
		data, marshalErr := json.Marshal(rec)
		if marshalErr != nil {
			return fmt.Errorf("marshal compact record: %w", marshalErr)
		}
		if _, writeErr := w.Write(append(data, '\n')); writeErr != nil {
			return fmt.Errorf("write compact: %w", writeErr)
		}
	}
	if flushErr := w.Flush(); flushErr != nil {
		return fmt.Errorf("flush compact: %w", flushErr)
	}
	if syncErr := f.Sync(); syncErr != nil {
		return fmt.Errorf("sync compact: %w", syncErr)
	}
	if renameErr := os.Rename(tmp, s.mapPath); renameErr != nil {
		return fmt.Errorf("rename compact: %w", renameErr)
	}
	return nil
}

// Compile-time guard: *Staleness satisfies the ProposalReminderer
// interface that the watcher and skill.* handlers consume.
var _ ProposalReminderer = (*Staleness)(nil)
