package mirror

// AdapterEntry is one row in the runtime adapter table: where to copy
// promoted skills for the named runtime, and (optionally) what command
// to send after copy so the runtime reloads its skill registry without
// a restart. ReloadCommand=="" means the runtime has no two-tier
// reload surface (loader picks up files on next invocation).
//
// See design-spec §11 for the canonical matrix and spec §20 item 9
// for the in-flight runtime audit list. v0.11 first-ship populates
// claude only; codex/opencode/kiro/cursor land via additive PRs once
// their loader behavior is audited.
type AdapterEntry struct {
	MirrorPath    string
	ReloadCommand string
}

// adapterTable maps runtime name → mirror entry. nil entries are
// runtimes that exist in the project (so a typo there is wrong) but
// have no v0.11 mirror surface — Lookup returns (nil, nil) on these so
// the worker treats them as success-skip per AC.
//
// PR-only additions per spec §20: edit this table to register a new
// runtime; never mutate at runtime. The read-only access pattern
// relies on Go's map-no-writers concurrent-read guarantee
// (TestAdapter_LookupConcurrent locks it in).
var adapterTable = map[string]*AdapterEntry{
	"claude": {
		MirrorPath:    ".claude/skills",
		ReloadCommand: "/reload-plugins",
	},
	"codex":    nil,
	"opencode": nil,
	"kiro":     nil,
	"cursor":   nil,
}

// Lookup returns the adapter entry for a runtime name. The return
// shape is three-valued so the worker can distinguish:
//
//   - (entry, nil): runtime is registered and has a mirror surface
//   - (nil,   nil): runtime is registered but null-entry (success-skip)
//   - (nil,   ErrUnknownRuntime): runtime is not registered (typo / new)
//
// Goroutine-safe by construction: adapterTable is package-level and
// never mutated after init, so concurrent reads are safe per Go's
// map-no-writers guarantee. No mutex.
//
// Adapter.Lookup is the plan-AC method form (plan §E9.1); the bare
// Lookup function below delegates to it for callers that don't want
// to instantiate an Adapter value.
type Adapter struct{}

// DefaultAdapter is the singleton callers reach for when they don't
// need to override the table. Future test-injected variants could
// embed a custom table, but v0.11 has one canonical adapter set.
var DefaultAdapter Adapter

// Lookup is the method form referenced in plan §E9.1.
func (Adapter) Lookup(runtime string) (*AdapterEntry, error) {
	return Lookup(runtime)
}

// Lookup is the package-level function form. Internal callers
// (Worker, EnsureMirrored) use it directly; external callers may
// prefer the method form via DefaultAdapter.Lookup for clarity.
func Lookup(runtime string) (*AdapterEntry, error) {
	entry, ok := adapterTable[runtime]
	if !ok {
		return nil, ErrUnknownRuntime
	}
	return entry, nil
}

// KnownRuntimes returns a snapshot of every registered runtime name
// (including null-entry ones). Used by the diagnostics layer / config
// validator to confirm a user-set `runtime.primary` is recognized
// before the daemon boot tries to mirror against it. Order is not
// guaranteed; callers that need a stable order sort the slice.
func KnownRuntimes() []string {
	out := make([]string, 0, len(adapterTable))
	for name := range adapterTable {
		out = append(out, name)
	}
	return out
}
