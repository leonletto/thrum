package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// RenderText writes Shape B (labeled stderr trailer) to w. Empty input produces
// no output. Hints are ordered warn-before-info; registration order is stable
// within each severity tier.
//
// Format:
//
//	(leading blank line)
//	  <sev> [<code>]: <message>
//	    <label>:  <cmd>    (<note>)
//	    ...
//	(blank line between hints)
func RenderText(hints []Hint, w io.Writer) {
	if len(hints) == 0 {
		return
	}
	ordered := make([]Hint, len(hints))
	copy(ordered, hints)
	sort.SliceStable(ordered, func(i, j int) bool {
		return severityRank(ordered[i].Severity) < severityRank(ordered[j].Severity)
	})
	// Writes to w ignore errors — we're rendering hints to a terminal/pipe;
	// if the writer is broken the command itself is in bigger trouble and
	// there's nothing the hint system can do about it.
	_, _ = fmt.Fprintln(w) // leading blank line separates from command stdout
	for i, h := range ordered {
		if i > 0 {
			_, _ = fmt.Fprintln(w)
		}
		_, _ = fmt.Fprintf(w, "  %s [%s]: %s\n", h.Severity, h.Code, h.Message)
		// Compute label-width so colons line up across option rows.
		// NOTE: uses len() (byte count) rather than utf8.RuneCountInString
		// because all pilot-catalog labels are ASCII ("attach", "replace",
		// "rename", etc.). Multi-byte labels would misalign. Revisit if
		// a future hint introduces non-ASCII labels.
		width := 0
		for _, o := range h.Options {
			if len(o.Label) > width {
				width = len(o.Label)
			}
		}
		for _, o := range h.Options {
			note := ""
			if o.Note != "" {
				note = "    (" + o.Note + ")"
			}
			// "%-*s" pads right to width+1 so the trailing colon aligns.
			_, _ = fmt.Fprintf(w, "    %-*s %s%s\n", width+1, o.Label+":", o.Cmd, note)
		}
	}
}

// RenderJSON converts hints to a Shape C []map suitable for embedding under
// a top-level "hints" key in a command's JSON body. Returns nil for empty
// input so callers can omit the key entirely (avoids "hints": null noise).
//
// Options with empty Note omit the "note" field per the JSON schema in spec §2.2.
func RenderJSON(hints []Hint) []map[string]any {
	if len(hints) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(hints))
	for _, h := range hints {
		opts := make([]map[string]any, 0, len(h.Options))
		for _, o := range h.Options {
			m := map[string]any{"label": o.Label, "cmd": o.Cmd}
			if o.Note != "" {
				m["note"] = o.Note
			}
			opts = append(opts, m)
		}
		out = append(out, map[string]any{
			"code":     h.Code,
			"severity": string(h.Severity),
			"message":  h.Message,
			"options":  opts,
		})
	}
	return out
}

// RenderJSONForEmit is RenderJSON with suppression applied. Command sites that
// build the top-level JSON body use this to decide whether to include the
// "hints" key at all. Returns nil when THRUM_NO_HINTS suppresses.
func RenderJSONForEmit(hints []Hint) []map[string]any {
	if hintsSuppressedByEnv() {
		return nil
	}
	return RenderJSON(hints)
}

// Emit renders Shape B to w unless suppressed. Suppression rules:
//   - quiet=true → suppress
//   - jsonMode=true → suppress (caller emits via RenderJSONForEmit)
//   - THRUM_NO_HINTS env truthy → suppress
func Emit(hints []Hint, quiet, jsonMode bool, w io.Writer) {
	if quiet || jsonMode || hintsSuppressedByEnv() {
		return
	}
	RenderText(hints, w)
}

// EmitStderr is a convenience for the common case of writing to os.Stderr.
func EmitStderr(hints []Hint, quiet, jsonMode bool) {
	Emit(hints, quiet, jsonMode, os.Stderr)
}

// severityRank orders warn (0) before info (1). Used by RenderText's stable sort.
func severityRank(s Severity) int {
	if s == SeverityWarn {
		return 0
	}
	return 1
}

// hintsSuppressedByEnv checks THRUM_NO_HINTS. Truthy values suppress:
// "1", "true", "yes" (anything non-empty that isn't "0" or "false").
func hintsSuppressedByEnv() bool {
	v := os.Getenv("THRUM_NO_HINTS")
	if v == "" {
		return false
	}
	if v == "0" || strings.EqualFold(v, "false") {
		return false
	}
	return true
}
