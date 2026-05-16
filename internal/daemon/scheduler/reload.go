package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ReloadEscalation is emitted via Scheduler.OnReloadError when a reload's
// JSON-parse or validator step fails. Carries the failing config path,
// every validator finding (whole-config — not just the first), and the
// timestamp the failure was detected. The daemon's escalation routing
// (B-B1 / D-B1 channels) consumes and surfaces this to the operator.
type ReloadEscalation struct {
	ConfigPath string
	Errors     []error
	Timestamp  time.Time
}

// ConfigFile is the on-disk JSON shape the scheduler cares about. Other
// top-level keys (daemon, sync, etc.) are decoded elsewhere by their
// respective owners; the scheduler only touches `jobs`.
type ConfigFile struct {
	Jobs map[string]JobSpec `json:"jobs"`
}

// ReloadConfig loads configPath from disk, validates with
// ValidateWholeConfig (Task 30), and either swaps the user-jobs portion
// of the spec map (success) or preserves last-good config (validator
// failure). On failure it logs every diagnostic AND invokes
// OnReloadError so the daemon's escalation channel sees the event.
//
// Spec §8.6.3: validator error MUST NOT crash the daemon nor corrupt
// previously-valid config. internal.* jobs are preserved across reload
// — they live in the daemon-registered registry, not in the user config.
func (s *Scheduler) ReloadConfig(_ context.Context, configPath string) error {
	data, err := os.ReadFile(configPath) //nolint:gosec // configPath is daemon-owned config file path
	if err != nil {
		return fmt.Errorf("ReloadConfig: read %s: %w", configPath, err)
	}

	var parsed ConfigFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&parsed); err != nil {
		s.escalateReloadError(configPath, []error{err})
		log.Printf("scheduler.ReloadConfig: parse error: %v", err)
		return fmt.Errorf("ReloadConfig: json: %w", err)
	}

	// Whole-config validator (Task 30) — reports ALL errors in one pass.
	if errs := s.ValidateWholeConfig(parsed.Jobs); len(errs) > 0 {
		s.escalateReloadError(configPath, errs)
		for _, e := range errs {
			log.Printf("scheduler.ReloadConfig: validator: %v", e)
		}
		return fmt.Errorf("ReloadConfig: %d validator error(s); config NOT swapped, daemon keeps last-good", len(errs))
	}

	// Validation passed — swap in user jobs under the config mutex.
	// Internal jobs (internal.* prefix) are preserved; only user jobs
	// rotate.
	s.mu.Lock()
	for id := range s.specs {
		if !strings.HasPrefix(id, InternalPrefix) {
			delete(s.specs, id)
		}
	}
	for id, spec := range parsed.Jobs {
		spec.ID = id // ensure the spec's own ID matches the map key
		s.specs[id] = spec
	}
	s.mu.Unlock()
	s.wakeReactor()
	return nil
}

// escalateReloadError invokes OnReloadError (if set) with the per-config
// escalation payload. Centralised so both the json-parse path and the
// validator-failure path emit the same shape.
func (s *Scheduler) escalateReloadError(configPath string, errs []error) {
	if s.OnReloadError == nil {
		return
	}
	s.OnReloadError(ReloadEscalation{
		ConfigPath: configPath,
		Errors:     errs,
		Timestamp:  time.Now(),
	})
}

// WatchConfig starts a config-file watcher and registers a SIGHUP fallback.
// Calls onReload once per detected change. Stops when ctx is cancelled.
//
// Two paths feed onReload:
//  1. fsnotify on the file's PARENT directory — fsnotify on Darwin
//     reports RENAME events against the parent inode (not the renamed
//     file), so watching the file directly would miss the
//     write-tmp-and-rename pattern operators typically use to edit
//     configs atomically. Filter events to the configured filename.
//  2. SIGHUP — process-wide signal that lets operators force a reload
//     when fsnotify misses the change (canonical macOS rename-gap per
//     brainstorm Q8.3 + MINOR-18).
//
// Returns an error if the watcher could not be created or the directory
// could not be added. Otherwise spawns a goroutine that runs until ctx
// cancels.
func (s *Scheduler) WatchConfig(ctx context.Context, configPath string, onReload func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	dir := filepath.Dir(configPath)
	if err := watcher.Add(dir); err != nil {
		_ = watcher.Close()
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)

	absConfig, _ := filepath.Abs(configPath)

	go func() {
		defer func() {
			_ = watcher.Close()
			signal.Stop(sigCh)
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				evAbs, _ := filepath.Abs(ev.Name)
				if evAbs != absConfig {
					continue
				}
				// WRITE catches in-place modifies; CREATE catches the
				// rename-and-replace second leg; RENAME catches the
				// first leg of write-tmp-and-rename. Any of the three
				// is a reload trigger.
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
					onReload()
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				// Ignore closed-watcher errors during shutdown; log others.
				if !errors.Is(err, fsnotify.ErrEventOverflow) {
					log.Printf("scheduler: fsnotify error on %s: %v", configPath, err)
				}
			case <-sigCh:
				onReload()
			}
		}
	}()
	return nil
}
