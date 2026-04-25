package roleconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ShippedTemplateInfo returns the schema_version and body_hash for a shipped
// role template. body_hash is sha256-hex of the file content excluding YAML
// frontmatter so whitespace-only frontmatter edits do not change the hash.
//
// Lookup order:
//  1. templates/roles/<role>-<autonomy>.md (multi-variant roles)
//  2. templates/roles/<role>.md            (single-variant fallback, e.g. orchestrator)
func ShippedTemplateInfo(role, autonomy string) (schemaVersion int, bodyHash string, err error) {
	raw, err := readShippedTemplateRaw(role, autonomy)
	if err != nil {
		return 0, "", err
	}
	return parseShippedTemplate(raw)
}

// ReadShippedTemplate returns the full file contents (with frontmatter) for the
// given shipped template variant. Falls back to single-variant <role>.md if
// no variant-specific file exists. Used by `thrum roles templates print` so
// the configure-roles skill can read embedded content via CLI.
func ReadShippedTemplate(role, autonomy string) ([]byte, error) {
	return readShippedTemplateRaw(role, autonomy)
}

// ListShippedTemplates returns the basenames (without extension) of every
// embedded role template, e.g. "coordinator-strict", "implementer-autonomous",
// "orchestrator".
func ListShippedTemplates() ([]string, error) {
	entries, err := fs.ReadDir(embeddedRoleTemplates, "templates/roles")
	if err != nil {
		return nil, fmt.Errorf("read embedded templates dir: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".md"))
	}
	return out, nil
}

// readShippedTemplateRaw is the shared lookup used by ShippedTemplateInfo and
// ReadShippedTemplate. Tries the variant-specific file first, then falls back
// to the single-variant <role>.md (e.g. orchestrator).
func readShippedTemplateRaw(role, autonomy string) ([]byte, error) {
	variant := "templates/roles/" + role + "-" + autonomy + ".md"
	raw, err := fs.ReadFile(embeddedRoleTemplates, variant)
	if err == nil {
		return raw, nil
	}
	fallback := "templates/roles/" + role + ".md"
	raw2, err2 := fs.ReadFile(embeddedRoleTemplates, fallback)
	if err2 == nil {
		return raw2, nil
	}
	return nil, fmt.Errorf("read shipped template (%s or %s): %w", variant, fallback, err)
}

var frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n?`)

type frontmatter struct {
	SchemaVersion int `yaml:"schema_version"`
}

// parseShippedTemplate extracts the schema_version from the YAML frontmatter
// and computes a sha256-hex body hash over the post-frontmatter content.
// Leading newlines after the closing "---" are trimmed before hashing so
// whitespace edits to the frontmatter block do not perturb body_hash.
func parseShippedTemplate(raw []byte) (schemaVersion int, bodyHash string, err error) {
	m := frontmatterRe.FindSubmatchIndex(raw)
	if m == nil {
		return 0, "", fmt.Errorf("no YAML frontmatter found")
	}
	fmRaw := raw[m[2]:m[3]]
	var fm frontmatter
	if err := yaml.Unmarshal(fmRaw, &fm); err != nil {
		return 0, "", fmt.Errorf("parse frontmatter: %w", err)
	}
	if fm.SchemaVersion == 0 {
		return 0, "", fmt.Errorf("schema_version missing or zero")
	}
	body := bytes.TrimLeft(raw[m[1]:], "\n")
	sum := sha256.Sum256(body)
	return fm.SchemaVersion, hex.EncodeToString(sum[:]), nil
}
