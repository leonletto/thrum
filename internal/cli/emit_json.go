package cli

import (
	"encoding/json"
	"fmt"
)

// EmitJSON marshals body as indented JSON and writes it to stdout with a
// trailing newline, grafting any slog-pushed hints under a top-level "hints"
// key. Callers pass structs, maps, or arrays; the function preserves the
// shape except when pushed hints exist AND body is non-object, in which case
// it wraps as {"result": body, "hints": [...]} to preserve the JSON contract.
//
// Use EmitJSONWithHints when the command also has pull-based hints (from
// cli.Collect) that should merge with the slog-pushed set.
func EmitJSON(body any) error {
	return EmitJSONWithHints(body, nil)
}

// EmitJSONWithHints is EmitJSON + explicit hints from the caller's own
// cli.Collect path. Slog-pushed hints and collected hints are merged,
// with collected hints placed first (preserves pilot-catalog ordering).
func EmitJSONWithHints(body any, collected []Hint) error {
	pushed := DrainPushedHints()
	// RenderJSONForEmit gates on THRUM_NO_HINTS; when suppressed the hints
	// disappear for both stderr and JSON paths, preserving the existing
	// suppression contract.
	allHints := make([]Hint, 0, len(collected)+len(pushed))
	allHints = append(allHints, collected...)
	allHints = append(allHints, pushed...)
	hintBlock := RenderJSONForEmit(allHints)

	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	// Object bodies: unmarshal back to map, graft hints key, re-marshal.
	// Non-object bodies (arrays, primitives): when there ARE hints, wrap.
	// When no hints, emit the body verbatim — preserves output for callers
	// whose downstream tooling doesn't expect a "hints" key.
	if len(raw) > 0 && raw[0] == '{' {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return fmt.Errorf("parse body for hint graft: %w", err)
		}
		if obj == nil {
			obj = map[string]any{}
		}
		if hintBlock != nil {
			obj["hints"] = hintBlock
		}
		out, err := json.MarshalIndent(obj, "", "  ")
		if err != nil {
			return fmt.Errorf("render body: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}
	if hintBlock != nil {
		wrap := map[string]any{"result": body, "hints": hintBlock}
		out, err := json.MarshalIndent(wrap, "", "  ")
		if err != nil {
			return fmt.Errorf("render wrapped body: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}
	out, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return fmt.Errorf("render raw body: %w", err)
	}
	fmt.Println(string(out))
	return nil
}
