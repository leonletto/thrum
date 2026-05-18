package skills

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter parser errors. Callers compare via errors.Is.
var (
	// ErrNoFrontmatter signals a SKILL.md file with no leading
	// `---` delimiter — the body is the entire file content.
	ErrNoFrontmatter = errors.New("skills: no frontmatter delimiter")

	// ErrFrontmatterInvalid signals a YAML parse failure on the
	// frontmatter region. The underlying yaml.v3 error is wrapped
	// for caller-side diagnostics.
	ErrFrontmatterInvalid = errors.New("skills: frontmatter invalid")
)

// ParseFrontmatter splits a SKILL.md document into its YAML
// frontmatter (decoded into Frontmatter) and the body bytes. The
// parser tolerates both forms documented in design-spec §9.2:
//
//   - **Nested form** (canonical on-disk): top-level `thrum:` block
//     containing the provenance / review fields.
//   - **Flat form** (compat fallback per spec §9.3): top-level keys
//     prefixed `thrum_*` (e.g. `thrum_proposed_by`, `thrum_promoted_by`,
//     `thrum_review_reviewed_by`). Used by runtime loaders that
//     reject nested YAML at discovery time. The flat form is parse-
//     only; Encode always emits the nested form.
//
// Returns ErrNoFrontmatter when the file has no leading `---`
// delimiter, ErrFrontmatterInvalid when the YAML region fails to
// parse, or any other read error directly.
func ParseFrontmatter(raw []byte) (*Frontmatter, []byte, error) {
	if !bytes.HasPrefix(raw, []byte("---")) {
		return nil, raw, ErrNoFrontmatter
	}
	rest := raw[3:]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	}
	yamlBytes, body, found := bytes.Cut(rest, []byte("\n---"))
	if !found {
		return nil, raw, fmt.Errorf("%w: missing closing delimiter", ErrFrontmatterInvalid)
	}
	// Strip the trailing-delimiter line break from the body.
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	}
	body = bytes.TrimLeft(body, "\n")

	var root yaml.Node
	if err := yaml.Unmarshal(yamlBytes, &root); err != nil {
		return nil, body, fmt.Errorf("%w: %w", ErrFrontmatterInvalid, err)
	}

	var fm Frontmatter
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 && root.Content[0].Kind == yaml.MappingNode {
		mapping := root.Content[0]
		hasNested := mapNodeHasKey(mapping, "thrum")
		hasFlat := mapNodeHasFlatThrum(mapping)

		switch {
		case hasNested:
			if err := mapping.Decode(&fm); err != nil {
				return nil, body, fmt.Errorf("%w: decode nested: %w", ErrFrontmatterInvalid, err)
			}
		case hasFlat:
			// Decode the non-thrum_* fields normally via a Frontmatter
			// scratch struct, then promote the flat keys into Thrum.
			if err := mapping.Decode(&fm); err != nil {
				return nil, body, fmt.Errorf("%w: decode flat: %w", ErrFrontmatterInvalid, err)
			}
			if err := promoteFlatThrumKeys(mapping, &fm.Thrum); err != nil {
				return nil, body, fmt.Errorf("%w: %w", ErrFrontmatterInvalid, err)
			}
		default:
			// No thrum block at all (e.g. propose-time draft before
			// the provenance is stamped). Decode the top-level
			// fields; Thrum stays at its zero value.
			if err := mapping.Decode(&fm); err != nil {
				return nil, body, fmt.Errorf("%w: decode: %w", ErrFrontmatterInvalid, err)
			}
		}
	}
	// Empty frontmatter (just `---\n---\n`) falls through with the
	// zero-value Frontmatter, no error — that's the legal
	// "delimiters present but no fields" shape.

	return &fm, body, nil
}

// EncodeFrontmatter serializes Frontmatter to canonical (nested-form)
// SKILL.md frontmatter bytes — including the leading and trailing
// `---` delimiters and a trailing newline. Callers concatenate the
// returned bytes with the body to form the full SKILL.md.
func EncodeFrontmatter(fm *Frontmatter) ([]byte, error) {
	if fm == nil {
		return nil, errors.New("skills: EncodeFrontmatter(nil)")
	}
	body, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("skills: marshal frontmatter: %w", err)
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(body)
	if !bytes.HasSuffix(body, []byte("\n")) {
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	return b.Bytes(), nil
}

// mapNodeHasKey reports whether a YAML mapping node contains a
// scalar key with the given name at the top level.
func mapNodeHasKey(mapping *yaml.Node, key string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return true
		}
	}
	return false
}

// mapNodeHasFlatThrum reports whether a YAML mapping node has any
// `thrum_*` flat-form key at the top level. The presence of even one
// is enough to switch the parser into flat-form-merge mode.
func mapNodeHasFlatThrum(mapping *yaml.Node) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Kind == yaml.ScalarNode && strings.HasPrefix(k.Value, "thrum_") {
			return true
		}
	}
	return false
}

// promoteFlatThrumKeys collects every `thrum_*` top-level key from
// the mapping node and decodes it into the corresponding ThrumProvenance
// field. The flat schema is the dot-separated nested path with `.`
// replaced by `_`:
//
//	thrum_proposed_by                          → ProposedBy
//	thrum_promoted_by                          → PromotedBy
//	thrum_created_at                           → CreatedAt
//	thrum_trigger_reason                       → TriggerReason
//	thrum_source_pattern_type                  → SourcePattern.Type
//	thrum_source_pattern_ref                   → SourcePattern.Ref
//	thrum_review_reviewed_by                   → Review.ReviewedBy
//	thrum_review_reviewed_at                   → Review.ReviewedAt
//	thrum_review_check_skill_version           → Review.CheckSkillVersion
//	thrum_review_revisions                     → Review.Revisions (array)
//	thrum_review_secret_scan_overrides         → Review.SecretScanOverrides (array)
//
// Unknown flat keys are ignored (forward-compat with C-B2's
// additional frontmatter fields).
func promoteFlatThrumKeys(mapping *yaml.Node, prov *ThrumProvenance) error {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		val := mapping.Content[i+1]
		if key.Kind != yaml.ScalarNode || !strings.HasPrefix(key.Value, "thrum_") {
			continue
		}
		switch key.Value {
		case "thrum_proposed_by":
			if err := val.Decode(&prov.ProposedBy); err != nil {
				return fmt.Errorf("decode thrum_proposed_by: %w", err)
			}
		case "thrum_promoted_by":
			if err := val.Decode(&prov.PromotedBy); err != nil {
				return fmt.Errorf("decode thrum_promoted_by: %w", err)
			}
		case "thrum_created_at":
			if err := val.Decode(&prov.CreatedAt); err != nil {
				return fmt.Errorf("decode thrum_created_at: %w", err)
			}
		case "thrum_trigger_reason":
			if err := val.Decode(&prov.TriggerReason); err != nil {
				return fmt.Errorf("decode thrum_trigger_reason: %w", err)
			}
		case "thrum_source_pattern_type":
			if err := val.Decode(&prov.SourcePattern.Type); err != nil {
				return fmt.Errorf("decode thrum_source_pattern_type: %w", err)
			}
		case "thrum_source_pattern_ref":
			if err := val.Decode(&prov.SourcePattern.Ref); err != nil {
				return fmt.Errorf("decode thrum_source_pattern_ref: %w", err)
			}
		case "thrum_review_reviewed_by":
			if err := val.Decode(&prov.Review.ReviewedBy); err != nil {
				return fmt.Errorf("decode thrum_review_reviewed_by: %w", err)
			}
		case "thrum_review_reviewed_at":
			if err := val.Decode(&prov.Review.ReviewedAt); err != nil {
				return fmt.Errorf("decode thrum_review_reviewed_at: %w", err)
			}
		case "thrum_review_check_skill_version":
			if err := val.Decode(&prov.Review.CheckSkillVersion); err != nil {
				return fmt.Errorf("decode thrum_review_check_skill_version: %w", err)
			}
		case "thrum_review_revisions":
			if err := val.Decode(&prov.Review.Revisions); err != nil {
				return fmt.Errorf("decode thrum_review_revisions: %w", err)
			}
		case "thrum_review_secret_scan_overrides":
			if err := val.Decode(&prov.Review.SecretScanOverrides); err != nil {
				return fmt.Errorf("decode thrum_review_secret_scan_overrides: %w", err)
			}
		}
	}
	return nil
}

// EncodeFlatFrontmatter is the read-only opposite of the flat-form
// parser: serializes a Frontmatter as flat-key SKILL.md. Used ONLY
// by mirror-time conversion for runtime loaders that don't tolerate
// nested YAML (per spec §9.3). Production code paths default to
// EncodeFrontmatter (nested form).
//
// Exported as a package-level entry point so the mirror sub-package
// can call it during apply when an adapter entry flags a flat-only
// loader (post-v0.11 — no adapter currently sets that bit).
func EncodeFlatFrontmatter(fm *Frontmatter) ([]byte, error) {
	if fm == nil {
		return nil, errors.New("skills: EncodeFlatFrontmatter(nil)")
	}
	// Build a generic map so we can intermix top-level fields with
	// the flattened thrum_* keys.
	out := map[string]any{}
	if fm.Name != "" {
		out["name"] = fm.Name
	}
	if fm.Description != "" {
		out["description"] = fm.Description
	}
	if len(fm.AllowedTools) > 0 {
		out["allowed-tools"] = fm.AllowedTools
	}
	if fm.Version != "" {
		out["version"] = fm.Version
	}
	if fm.Author != "" {
		out["author"] = fm.Author
	}
	if fm.License != "" {
		out["license"] = fm.License
	}
	addIfNonZero := func(k string, v any) {
		if !reflect.ValueOf(v).IsZero() {
			out[k] = v
		}
	}
	addIfNonZero("thrum_proposed_by", fm.Thrum.ProposedBy)
	addIfNonZero("thrum_promoted_by", fm.Thrum.PromotedBy)
	addIfNonZero("thrum_created_at", fm.Thrum.CreatedAt)
	addIfNonZero("thrum_trigger_reason", fm.Thrum.TriggerReason)
	addIfNonZero("thrum_source_pattern_type", fm.Thrum.SourcePattern.Type)
	addIfNonZero("thrum_source_pattern_ref", fm.Thrum.SourcePattern.Ref)
	addIfNonZero("thrum_review_reviewed_by", fm.Thrum.Review.ReviewedBy)
	addIfNonZero("thrum_review_reviewed_at", fm.Thrum.Review.ReviewedAt)
	addIfNonZero("thrum_review_check_skill_version", fm.Thrum.Review.CheckSkillVersion)
	if len(fm.Thrum.Review.Revisions) > 0 {
		out["thrum_review_revisions"] = fm.Thrum.Review.Revisions
	}
	if len(fm.Thrum.Review.SecretScanOverrides) > 0 {
		out["thrum_review_secret_scan_overrides"] = fm.Thrum.Review.SecretScanOverrides
	}

	body, err := yaml.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("skills: marshal flat frontmatter: %w", err)
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(body)
	if !bytes.HasSuffix(body, []byte("\n")) {
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	return b.Bytes(), nil
}
