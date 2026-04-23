package cli

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	hintRegistryMu sync.RWMutex
	hintRegistry   = map[string]HintSource{}
)

// RegisterHintSource wires a HintSource for a command name. Typically called
// from the init() function of each hint_sources_*.go file. Re-registration of
// the same command overwrites the previous source (tests rely on this to swap
// in fixtures).
func RegisterHintSource(command string, src HintSource) {
	hintRegistryMu.Lock()
	defer hintRegistryMu.Unlock()
	hintRegistry[command] = src
}

// Collect runs the HintSource registered for ctx.Command and returns its hints.
// Returns nil when no source is registered for the command (unmigrated commands
// fall back to the legacy flat-map via LegacyHint).
func Collect(ctx HintCtx) []Hint {
	hintRegistryMu.RLock()
	src, ok := hintRegistry[ctx.Command]
	hintRegistryMu.RUnlock()
	if !ok {
		return nil
	}
	return src(ctx)
}

// HintAbortError is returned by HandlePreAction when one or more warn hints
// block execution. Command sites return this from their RunE; the CLI wrapper
// renders the blocking hints to stderr and exits non-zero (see EmitAbort).
type HintAbortError struct {
	Hints []Hint
}

func (e *HintAbortError) Error() string {
	codes := make([]string, 0, len(e.Hints))
	for _, h := range e.Hints {
		codes = append(codes, h.Code)
	}
	return "command aborted by hint(s): " + strings.Join(codes, ", ")
}

// HandlePreAction returns a *HintAbortError when any warn hint blocks the
// command. Rules:
//   - warn with AllowForce=false → always blocks (hard refusal)
//   - warn with AllowForce=true  → blocks unless force=true
//   - info → never blocks
//
// The returned error carries the blocking hints so the caller can render them.
// Non-blocking hints (info, or warn cleared by force) are not in the return;
// the caller still has the full hint list from Collect() to render.
func HandlePreAction(hints []Hint, force bool) error {
	var blockers []Hint
	for _, h := range hints {
		if h.Severity != SeverityWarn {
			continue
		}
		if h.AllowForce && force {
			continue
		}
		blockers = append(blockers, h)
	}
	if len(blockers) == 0 {
		return nil
	}
	return &HintAbortError{Hints: blockers}
}

// EmitAbort is a convenience for command sites: if err is a *HintAbortError,
// render the blocking hints to stderr (respecting suppression) and return a
// terse shell-facing error. Non-HintAbortError errors are returned unchanged.
//
// The shell-facing message varies by whether any blocker is recoverable via
// --force; we only suggest --force when at least one blocker opts in.
func EmitAbort(err error, quiet, jsonMode bool) error {
	var he *HintAbortError
	if !errors.As(err, &he) {
		return err
	}
	EmitStderr(he.Hints, quiet, jsonMode)
	anyRecoverable := false
	for _, h := range he.Hints {
		if h.AllowForce {
			anyRecoverable = true
			break
		}
	}
	if anyRecoverable {
		return fmt.Errorf("aborted (pass --force to override recoverable conditions)")
	}
	return fmt.Errorf("aborted — see hint above for next steps")
}
