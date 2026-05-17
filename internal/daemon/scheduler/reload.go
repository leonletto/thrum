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
	"regexp"
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
		wrapped := normalizeJSONDecodeError(err)
		s.escalateReloadError(configPath, []error{wrapped})
		log.Printf("scheduler.ReloadConfig: parse error: %v", wrapped)
		return fmt.Errorf("ReloadConfig: json: %w", wrapped)
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

// unknownFieldRE extracts the unknown field name from Go's encoding/json
// DisallowUnknownFields error message: `json: unknown field "<name>"`.
// Used to reformat into the spec §4.1 rule-8 operator-facing shape.
var unknownFieldRE = regexp.MustCompile(`unknown field "([^"]+)"`)

// normalizeJSONDecodeError reshapes Go's raw json.Decoder errors into the
// spec §4.1 operator-facing format `jobs.<id>.<unknown_key>: not a
// recognized field`. For DisallowUnknownFields rejections this means
// emitting the structured `<key>: not a recognized field` form; other
// JSON errors (syntax, type mismatch) pass through unchanged because
// the spec only mandates a structured shape for rule 8.
//
// We don't always have the jobs.<id> path context here (Go's
// DisallowUnknownFields error doesn't carry the offending path), so the
// wrapped error names the unknown field and lets the operator locate the
// jobs entry. ReloadConfig log + OnReloadError escalation both see this
// shape.
func normalizeJSONDecodeError(err error) error {
	if err == nil {
		return nil
	}
	if match := unknownFieldRE.FindStringSubmatch(err.Error()); match != nil {
		return fmt.Errorf("jobs.<id>.%s: not a recognized field", match[1])
	}
	return err
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

// AtomicWriteConfig writes `data` to `path` via the .tmp → fsync → rename
// pattern per spec §8.6.4. Failures leave the prior file at `path`
// intact; the .tmp file is cleaned up on any error and on the
// pre-rename close failure path.
//
// Used by any code that mutates config from inside the daemon (e.g.
// future job.create / job.update RPCs that persist back to disk) AND by
// operators who can write configs from outside the daemon. The fsync
// makes the new content durable before the rename, so a power loss
// between rename and OS buffer flush still finds either the old file
// (rename hadn't committed) or the new file with valid content
// (rename committed; data was already on disk).
func AtomicWriteConfig(path string, data []byte) error {
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // tmpPath is daemon-owned config path
	if err != nil {
		return fmt.Errorf("atomic write %s: open tmp: %w", path, err)
	}
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("atomic write %s: write tmp: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("atomic write %s: sync tmp: %w", path, err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("atomic write %s: close tmp: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("atomic write %s: rename: %w", path, err)
	}
	return nil
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
