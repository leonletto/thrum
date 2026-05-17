//go:build integration

// Package skills runtime_loaders verifies frontmatter-compatibility
// across the runtime plugin loaders C-B1 will mirror skills into.
// Per spec §9.3 the audit deliverable is: load a representative
// SKILL.md through each runtime plugin's skill-discovery path; if
// nested-form frontmatter is rejected, the flat-form fallback must
// load instead.
//
// In-repo we don't have direct access to the runtime plugins'
// loaders — claude-plugin/, opencode-plugin/, codex-plugin/ ship
// their own discovery code that runs in the runtime's own process.
// What we DO have is internal/skills.ParseFrontmatter (E11.1),
// which targets the same YAML shape every runtime loader sees.
//
// This test exercises ParseFrontmatter against a representative
// sample SKILL.md in BOTH forms as a proxy for runtime tolerance.
// Real cross-runtime loader audit is an operational task: install
// each runtime plugin in a fresh repo with the sample SKILL.md
// mirrored under that runtime's path, restart, and confirm the
// loader picks it up. See dev-docs/operator/skill-runtime-audit.md
// for the operational playbook (lands at E10.10).
package skills

import (
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/skills"
)

// canonicalNested is the full nested-form fixture per spec §9.2.
// Every thrum.* field populated so loaders that strict-validate
// against the schema have a complete sample.
const canonicalNested = `---
name: runtime-audit-fixture
description: Compatibility audit fixture for runtime loaders.
allowed-tools:
  - Bash
  - Read
version: 1.0.0
author: "@coordinator_main"
license: Apache-2.0

thrum:
  proposed_by: "@researcher_skills"
  promoted_by: "@coordinator_main"
  created_at: 2026-05-15T17:00:00Z
  trigger_reason: cross-runtime compat audit
  source_pattern:
    type: bd-issue
    ref: thrum-6qmf.2.12
  review:
    reviewed_by: "@coordinator_main"
    reviewed_at: 2026-05-15T17:05:00Z
    check_skill_version: 0.0.0-stub
    revisions: []
    secret_scan_overrides: []
---

# runtime-audit-fixture

Body content. Real runtime loaders read this as the skill body.
`

// flatFallback mirrors canonicalNested in the flat-key form per
// spec §9.3. Same fields, different shape.
const flatFallback = `---
name: runtime-audit-fixture
description: Compatibility audit fixture for runtime loaders.
allowed-tools:
  - Bash
  - Read
version: 1.0.0
author: "@coordinator_main"
license: Apache-2.0

thrum_proposed_by: "@researcher_skills"
thrum_promoted_by: "@coordinator_main"
thrum_created_at: 2026-05-15T17:00:00Z
thrum_trigger_reason: cross-runtime compat audit
thrum_source_pattern_type: bd-issue
thrum_source_pattern_ref: thrum-6qmf.2.12
thrum_review_reviewed_by: "@coordinator_main"
thrum_review_reviewed_at: 2026-05-15T17:05:00Z
thrum_review_check_skill_version: 0.0.0-stub
---

# runtime-audit-fixture

Body content. Real runtime loaders read this as the skill body.
`

func assertFrontmatterShape(t *testing.T, label string, fm *skills.Frontmatter) {
	t.Helper()
	if fm == nil {
		t.Fatalf("[%s] nil Frontmatter", label)
	}
	if fm.Name != "runtime-audit-fixture" {
		t.Errorf("[%s] Name = %q", label, fm.Name)
	}
	if fm.Description == "" {
		t.Errorf("[%s] Description empty", label)
	}
	if len(fm.AllowedTools) != 2 {
		t.Errorf("[%s] AllowedTools len = %d, want 2", label, len(fm.AllowedTools))
	}
	if fm.Thrum.ProposedBy != "@researcher_skills" {
		t.Errorf("[%s] Thrum.ProposedBy = %q", label, fm.Thrum.ProposedBy)
	}
	if fm.Thrum.SourcePattern.Type != "bd-issue" {
		t.Errorf("[%s] SourcePattern.Type = %q", label, fm.Thrum.SourcePattern.Type)
	}
	if fm.Thrum.SourcePattern.Ref != "thrum-6qmf.2.12" {
		t.Errorf("[%s] SourcePattern.Ref = %q", label, fm.Thrum.SourcePattern.Ref)
	}
	if fm.Thrum.Review.CheckSkillVersion != "0.0.0-stub" {
		t.Errorf("[%s] CheckSkillVersion = %q", label, fm.Thrum.Review.CheckSkillVersion)
	}
}

// TestRuntimeLoader_NestedFormLoads is the parser-level proxy for
// every runtime loader audited per spec §9.3. If this test fails,
// the canonical schema in skill_types.go has drifted from what
// ParseFrontmatter expects.
func TestRuntimeLoader_NestedFormLoads(t *testing.T) {
	t.Parallel()

	fm, body, err := skills.ParseFrontmatter([]byte(canonicalNested))
	if err != nil {
		t.Fatalf("ParseFrontmatter (nested): %v", err)
	}
	assertFrontmatterShape(t, "nested", fm)
	if !strings.Contains(string(body), "# runtime-audit-fixture") {
		t.Errorf("nested body missing fixture marker")
	}
}

// TestRuntimeLoader_FlatFormFallback covers the runtime-loader case
// where nested YAML is rejected by the loader (spec §9.3 fallback).
// The parser must produce the same Frontmatter shape from flat
// keys as from the nested form.
func TestRuntimeLoader_FlatFormFallback(t *testing.T) {
	t.Parallel()

	fm, body, err := skills.ParseFrontmatter([]byte(flatFallback))
	if err != nil {
		t.Fatalf("ParseFrontmatter (flat): %v", err)
	}
	assertFrontmatterShape(t, "flat", fm)
	if !strings.Contains(string(body), "# runtime-audit-fixture") {
		t.Errorf("flat body missing fixture marker")
	}
}

// TestRuntimeLoader_NestedAndFlatProduceEqualShape pins the
// equivalence invariant: a sample SKILL.md in either form decodes
// to the same logical Frontmatter. This is the contract every
// runtime loader's per-runtime parser must preserve.
func TestRuntimeLoader_NestedAndFlatProduceEqualShape(t *testing.T) {
	t.Parallel()

	nested, _, err := skills.ParseFrontmatter([]byte(canonicalNested))
	if err != nil {
		t.Fatalf("nested parse: %v", err)
	}
	flat, _, err := skills.ParseFrontmatter([]byte(flatFallback))
	if err != nil {
		t.Fatalf("flat parse: %v", err)
	}
	// Compare per-field — the fixtures use the same string values
	// across the board.
	if nested.Name != flat.Name {
		t.Errorf("Name: nested=%q flat=%q", nested.Name, flat.Name)
	}
	if nested.Thrum.ProposedBy != flat.Thrum.ProposedBy {
		t.Errorf("Thrum.ProposedBy: nested=%q flat=%q", nested.Thrum.ProposedBy, flat.Thrum.ProposedBy)
	}
	if !nested.Thrum.CreatedAt.Equal(flat.Thrum.CreatedAt) {
		t.Errorf("Thrum.CreatedAt: nested=%v flat=%v", nested.Thrum.CreatedAt, flat.Thrum.CreatedAt)
	}
	if nested.Thrum.SourcePattern != flat.Thrum.SourcePattern {
		t.Errorf("SourcePattern: nested=%+v flat=%+v", nested.Thrum.SourcePattern, flat.Thrum.SourcePattern)
	}
	if nested.Thrum.Review.CheckSkillVersion != flat.Thrum.Review.CheckSkillVersion {
		t.Errorf("Review.CheckSkillVersion: nested=%q flat=%q",
			nested.Thrum.Review.CheckSkillVersion, flat.Thrum.Review.CheckSkillVersion)
	}
}
