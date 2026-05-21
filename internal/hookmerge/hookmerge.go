// Package hookmerge provides JSON-merge primitives for managing hook entries
// in Claude Code's .claude/settings.json format. The package preserves
// third-party (bd, user, other tools) entries while letting thrum push
// hook updates idempotently — the alternative skip-on-exists policy prevents
// thrum init from refreshing hook strings once a user owns the file.
//
// Hook entries follow Claude Code's shape:
//
//	{
//	  "hooks": {
//	    "SessionStart": [
//	      {"hooks": [{"type": "command", "command": "..."}]}
//	    ]
//	  }
//	}
//
// All match/dedup logic uses exact-string comparison on the command field,
// matching bd setup claude's semantics so the two tools coexist cleanly.
package hookmerge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Settings is Claude Code's settings.json document parsed as a generic map.
// Using map[string]any rather than typed structs preserves third-party
// keys (enabledPlugins, model, custom fields) on round-trip.
type Settings = map[string]any

// Load reads and parses a settings.json file. Returns an empty map (not nil)
// when the file does not exist, so callers can unconditionally Add/Remove
// and Save.
func Load(path string) (Settings, error) {
	data, err := os.ReadFile(path) //#nosec G304 -- path is supplied by caller in a trusted context (runtime-init or worktree setup)
	if err != nil {
		if os.IsNotExist(err) {
			return Settings{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return Settings{}, nil
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if settings == nil {
		return Settings{}, nil
	}
	return settings, nil
}

// Save writes settings as 2-space-indented JSON, matching bd setup claude's
// json.MarshalIndent("", "  ") format so the two writers produce byte-stable
// output on the same file. Parent directories are created with 0o750.
func Save(path string, settings Settings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	// Trailing newline matches editors' default and bd's output.
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //#nosec G306 -- settings.json is user-editable, group-readable matches existing template mode
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// AddHookCommand ensures a hook with the given exact command exists under
// settings.hooks[event]. Returns true if a new entry was appended, false if
// the command was already present in any entry's hooks array.
//
// The new entry shape is `{"hooks": [{"type": "command", "command": <cmd>}]}`.
// Existing entries in the same event array (e.g. bd's hook, the user's
// custom hooks) are not inspected beyond the command-equality check and
// pass through untouched.
func AddHookCommand(settings Settings, event, command string) bool {
	hooksMap := getOrCreateHooksMap(settings)
	eventArr, _ := hooksMap[event].([]any)

	if commandPresent(eventArr, command) {
		return false
	}

	newEntry := map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": command,
			},
		},
	}
	eventArr = append(eventArr, newEntry)
	hooksMap[event] = eventArr
	return true
}

// RemoveHookCommand removes any hook whose command exactly matches the given
// string from settings.hooks[event]. If an entry's hooks array becomes empty
// after removal, the entry is dropped. If the event array becomes empty, the
// event key is deleted entirely (avoids JSON null and preserves byte-stable
// re-runs). Returns true if any hook was removed.
func RemoveHookCommand(settings Settings, event, command string) bool {
	hooksMap, ok := settings["hooks"].(map[string]any)
	if !ok {
		return false
	}
	eventArr, ok := hooksMap[event].([]any)
	if !ok {
		return false
	}

	removed := false
	filteredEntries := make([]any, 0, len(eventArr))
	for _, raw := range eventArr {
		entry, ok := raw.(map[string]any)
		if !ok {
			filteredEntries = append(filteredEntries, raw)
			continue
		}
		hooksArr, ok := entry["hooks"].([]any)
		if !ok {
			filteredEntries = append(filteredEntries, raw)
			continue
		}
		keptHooks := make([]any, 0, len(hooksArr))
		for _, h := range hooksArr {
			hMap, ok := h.(map[string]any)
			if !ok {
				keptHooks = append(keptHooks, h)
				continue
			}
			if cmd, _ := hMap["command"].(string); cmd == command {
				removed = true
				continue
			}
			keptHooks = append(keptHooks, h)
		}
		if len(keptHooks) == 0 {
			// Entry's only command(s) matched — drop the whole entry.
			continue
		}
		entry["hooks"] = keptHooks
		filteredEntries = append(filteredEntries, entry)
	}

	if !removed {
		return false
	}

	if len(filteredEntries) == 0 {
		delete(hooksMap, event)
	} else {
		hooksMap[event] = filteredEntries
	}

	// Clean up an emptied hooks parent so the file doesn't accumulate {} -> {"hooks":{}}.
	if len(hooksMap) == 0 {
		delete(settings, "hooks")
	}

	return true
}

// HasBeadsPlugin returns true if settings.enabledPlugins contains any key
// whose lowercased form contains "beads" with a truthy value. Used as the
// gate for skipping thrum's bd hook install — when the beads marketplace
// plugin is enabled, the plugin already installs the SessionStart hook and
// thrum must not double-fire.
func HasBeadsPlugin(settings Settings) bool {
	enabled, ok := settings["enabledPlugins"].(map[string]any)
	if !ok {
		return false
	}
	for key, val := range enabled {
		if !strings.Contains(strings.ToLower(key), "beads") {
			continue
		}
		switch v := val.(type) {
		case bool:
			if v {
				return true
			}
		case string:
			// Some plugin shapes record the marker as a string. Only
			// recognize the well-known falsy values; everything else
			// (including "0" and "no") is treated as enabled. This is
			// conservative — false-positive skip is preferable to
			// false-negative install since double-installing hooks is
			// the failure mode this guard exists to prevent.
			if v != "" && v != "false" {
				return true
			}
		}
	}
	return false
}

// ExtractCommands walks settings.hooks and returns every (event, command)
// pair found, in stable iteration order per-event. Intended for merging
// rendered-template commands into an existing file.
func ExtractCommands(settings Settings) []EventCommand {
	hooksMap, ok := settings["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	// Stable event ordering keeps merge output deterministic across runs.
	events := make([]string, 0, len(hooksMap))
	for e := range hooksMap {
		events = append(events, e)
	}
	slices.Sort(events)

	var out []EventCommand
	for _, event := range events {
		arr, ok := hooksMap[event].([]any)
		if !ok {
			continue
		}
		for _, raw := range arr {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			hooks, ok := entry["hooks"].([]any)
			if !ok {
				continue
			}
			for _, h := range hooks {
				hMap, ok := h.(map[string]any)
				if !ok {
					continue
				}
				if cmd, _ := hMap["command"].(string); cmd != "" {
					out = append(out, EventCommand{Event: event, Command: cmd})
				}
			}
		}
	}
	return out
}

// EventCommand pairs a hook event name (SessionStart, PreCompact, …) with the
// exact command string that should be installed under it. ExtractCommands
// emits these from a rendered template; Apply consumes them to merge into
// an existing file.
type EventCommand struct {
	Event   string
	Command string
}

// commandPresent reports whether eventArr already contains any hook entry
// whose command exactly matches the given string.
func commandPresent(eventArr []any, command string) bool {
	for _, raw := range eventArr {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		hooksArr, ok := entry["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range hooksArr {
			hMap, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if cmd, _ := hMap["command"].(string); cmd == command {
				return true
			}
		}
	}
	return false
}

// getOrCreateHooksMap returns settings.hooks, creating it if absent. The
// returned map shares storage with settings, so mutations are visible.
func getOrCreateHooksMap(settings Settings) map[string]any {
	hooksMap, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooksMap = map[string]any{}
		settings["hooks"] = hooksMap
	}
	return hooksMap
}

