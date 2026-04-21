package cli

import (
	"context"
	"log/slog"
	"strings"
	"sync"
)

var (
	pushedHintsMu sync.Mutex
	pushedHints   []Hint
)

// SlogHintHandler is a slog.Handler that converts records at Warn or above
// into Hints appended to an in-process accumulator. In --json mode this
// accumulator is drained and grafted into the JSON body by EmitJSON; in
// human mode it's rendered to stderr by EmitStderr via the existing
// Shape B path. Lower-level records (Info, Debug) are dropped because
// they are operator-facing noise the CLI should not surface.
//
// The bridge surfaces only record.Message — structured attrs (slog.With,
// slog.Group) are intentionally NOT propagated to the resulting Hint.
// Hint.Message is human-readable text that ends up in users' JSON
// bodies/stderr; smuggling structured key/values into it would mangle the
// presentation and risk leaking unredacted attribute values into outputs
// that callers may pipe to logs or other tools. Library code that wants
// structured context in a hint should construct the Hint at the call site
// (with the cli.Hint API) rather than through slog.
type SlogHintHandler struct{}

// NewSlogHintHandler returns a handler ready to install via
// slog.SetDefault(slog.New(NewSlogHintHandler())).
func NewSlogHintHandler() *SlogHintHandler { return &SlogHintHandler{} }

func (h *SlogHintHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

func (h *SlogHintHandler) Handle(_ context.Context, r slog.Record) error {
	// Enforce the same gate Enabled() promises, so callers that bypass
	// Enabled() (tests, MultiHandler compositions) don't accumulate
	// Info/Debug records. slog.Default() normally consults Enabled first,
	// but this keeps the handler self-consistent.
	if r.Level < slog.LevelWarn {
		return nil
	}
	code := deriveHintCode(r.Message)
	hint := Hint{
		Code:     code,
		Severity: SeverityWarn,
		Message:  r.Message,
	}
	pushedHintsMu.Lock()
	pushedHints = append(pushedHints, hint)
	pushedHintsMu.Unlock()
	return nil
}

// WithAttrs and WithGroup return the handler unchanged — see the type
// docstring for why structured attrs are intentionally not surfaced in
// hints. Returning the receiver means slog.With(...).Warn(msg) emits the
// same hint that .Warn(msg) alone would, with no attribute leakage.
func (h *SlogHintHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

func (h *SlogHintHandler) WithGroup(_ string) slog.Handler { return h }

// deriveHintCode extracts a code token from a slog record message. The
// convention most of the codebase already follows is "package.Symbol: reason"
// or "subsystem: reason" — we pull the first whitespace-delimited token,
// strip surrounding brackets (for daemon-style "[telegram.msgmap] ..."
// records that may reach the bridge in unusual flows), and trim a trailing
// colon. If the result still doesn't contain a "." we fall back to
// "runtime.warn" so EmitJSON always has a stable code.
func deriveHintCode(msg string) string {
	first := strings.SplitN(msg, " ", 2)[0]
	first = strings.Trim(first, "[]")
	first = strings.TrimRight(first, ":")
	first = strings.ToLower(first)
	if !strings.Contains(first, ".") {
		return "runtime.warn"
	}
	return first
}

// DrainPushedHints returns all accumulated hints and clears the buffer.
// Safe to call multiple times; returns nil when empty.
func DrainPushedHints() []Hint {
	pushedHintsMu.Lock()
	defer pushedHintsMu.Unlock()
	if len(pushedHints) == 0 {
		return nil
	}
	out := pushedHints
	pushedHints = nil
	return out
}

// ResetPushedHintsForTest clears the buffer without returning it. Tests
// should call this in setup so state from prior tests doesn't leak.
func ResetPushedHintsForTest() {
	pushedHintsMu.Lock()
	pushedHints = nil
	pushedHintsMu.Unlock()
}
