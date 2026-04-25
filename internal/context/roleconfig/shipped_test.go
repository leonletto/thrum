package roleconfig

import (
	"testing"
)

func TestShippedTemplateInfo_ValidTemplate(t *testing.T) {
	schema, hash, err := ShippedTemplateInfo("coordinator", "autonomous")
	if err != nil {
		t.Fatalf("ShippedTemplateInfo: %v", err)
	}
	if schema != 1 {
		t.Errorf("schema: got %d, want 1", schema)
	}
	if len(hash) != 64 {
		t.Errorf("hash length: got %d, want 64; hash=%q", len(hash), hash)
	}
}

func TestShippedTemplateInfo_UnknownRole(t *testing.T) {
	_, _, err := ShippedTemplateInfo("nonexistent", "strict")
	if err == nil {
		t.Fatal("expected error for unknown role")
	}
}

func TestShippedTemplateInfo_HashStableAcrossWhitespaceFrontmatterEdits(t *testing.T) {
	body := "# Body\n\nSome content.\n"
	a := []byte("---\nschema_version: 1\n---\n" + body)
	b := []byte("---\nschema_version:    1\n---\n" + body)
	_, hashA, err := parseShippedTemplate(a)
	if err != nil {
		t.Fatalf("parse a: %v", err)
	}
	_, hashB, err := parseShippedTemplate(b)
	if err != nil {
		t.Fatalf("parse b: %v", err)
	}
	if hashA != hashB {
		t.Errorf("body hash should be stable across frontmatter whitespace edits; got %q vs %q", hashA, hashB)
	}
}

func TestParseShippedTemplate_BodyChangeChangesHash(t *testing.T) {
	a := []byte("---\nschema_version: 1\n---\n# A\n")
	b := []byte("---\nschema_version: 1\n---\n# B\n")
	_, hashA, _ := parseShippedTemplate(a)
	_, hashB, _ := parseShippedTemplate(b)
	if hashA == hashB {
		t.Error("body change should change hash")
	}
}

func TestListShippedTemplates_Returns19Files(t *testing.T) {
	list, err := ListShippedTemplates()
	if err != nil {
		t.Fatalf("ListShippedTemplates: %v", err)
	}
	if len(list) != 19 {
		t.Errorf("expected 19 templates, got %d (%v)", len(list), list)
	}
}

// TestShippedTemplateInfo_OrchestratorFallback verifies the single-variant
// fallback path: orchestrator.md has no -strict / -autonomous suffix, so
// ShippedTemplateInfo("orchestrator", *) must return the same hash regardless
// of autonomy.
//
// Regression spec: thrum-z2et.20.1 § "Single-variant role caveat".
func TestShippedTemplateInfo_OrchestratorFallback(t *testing.T) {
	schema, hash, err := ShippedTemplateInfo("orchestrator", "autonomous")
	if err != nil {
		t.Fatalf("orchestrator (autonomous) fallback failed: %v", err)
	}
	if schema != 1 {
		t.Errorf("schema: got %d, want 1", schema)
	}
	if len(hash) != 64 {
		t.Errorf("hash length: got %d, want 64", len(hash))
	}

	_, hash2, err := ShippedTemplateInfo("orchestrator", "strict")
	if err != nil {
		t.Fatalf("orchestrator (strict) fallback failed: %v", err)
	}
	if hash != hash2 {
		t.Errorf("orchestrator hash should be variant-independent; autonomous=%q strict=%q", hash, hash2)
	}
}
