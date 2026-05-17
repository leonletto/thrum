package skills

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

const nestedCanonical = `---
name: nested-fixture
description: Nested canonical form
version: 1.0.0
thrum:
  proposed_by: "@researcher_x"
  promoted_by: "@coordinator_main"
  created_at: 2026-05-15T17:00:00Z
  trigger_reason: testing
  source_pattern:
    type: bd-issue
    ref: thrum-6qmf.2.1
  review:
    reviewed_by: "@coordinator_main"
    reviewed_at: 2026-05-15T17:05:00Z
    check_skill_version: 0.0.0-stub
    revisions:
      - msg_thread_id: msg_01TEST
        proposed_by: "@researcher_x"
        at: 2026-05-15T16:30:00Z
---

# nested-fixture

body
`

const flatFallback = `---
name: flat-fixture
description: Flat-key fallback form
version: 1.0.0
thrum_proposed_by: "@researcher_x"
thrum_promoted_by: "@coordinator_main"
thrum_created_at: 2026-05-15T17:00:00Z
thrum_trigger_reason: testing
thrum_source_pattern_type: bd-issue
thrum_source_pattern_ref: thrum-6qmf.2.1
thrum_review_reviewed_by: "@coordinator_main"
thrum_review_reviewed_at: 2026-05-15T17:05:00Z
thrum_review_check_skill_version: 0.0.0-stub
---

# flat-fixture

body
`

func TestFrontmatter_ParseNestedThrumBlock(t *testing.T) {
	t.Parallel()

	fm, body, err := ParseFrontmatter([]byte(nestedCanonical))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if fm.Name != "nested-fixture" {
		t.Errorf("Name = %q", fm.Name)
	}
	if fm.Thrum.ProposedBy != "@researcher_x" {
		t.Errorf("ProposedBy = %q", fm.Thrum.ProposedBy)
	}
	if fm.Thrum.SourcePattern.Type != "bd-issue" {
		t.Errorf("SourcePattern.Type = %q", fm.Thrum.SourcePattern.Type)
	}
	if fm.Thrum.SourcePattern.Ref != "thrum-6qmf.2.1" {
		t.Errorf("SourcePattern.Ref = %q", fm.Thrum.SourcePattern.Ref)
	}
	if fm.Thrum.Review.CheckSkillVersion != "0.0.0-stub" {
		t.Errorf("CheckSkillVersion = %q", fm.Thrum.Review.CheckSkillVersion)
	}
	if len(fm.Thrum.Review.Revisions) != 1 {
		t.Fatalf("Revisions len = %d", len(fm.Thrum.Review.Revisions))
	}
	if fm.Thrum.Review.Revisions[0].MsgThreadID != "msg_01TEST" {
		t.Errorf("Revisions[0].MsgThreadID = %q", fm.Thrum.Review.Revisions[0].MsgThreadID)
	}
	if !strings.Contains(string(body), "# nested-fixture") {
		t.Errorf("body missing fixture marker: %q", body)
	}
}

func TestFrontmatter_ParseFlatKeyFallback(t *testing.T) {
	t.Parallel()

	fm, _, err := ParseFrontmatter([]byte(flatFallback))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}

	// The flat-form parser must produce the same ThrumProvenance
	// shape as the nested-form parser. Compare every field.
	nestedFm, _, err := ParseFrontmatter([]byte(nestedCanonical))
	if err != nil {
		t.Fatalf("ParseFrontmatter nested baseline: %v", err)
	}

	// Some top-level fields differ between fixtures (Name); only
	// compare the Thrum block.
	if fm.Thrum.ProposedBy != nestedFm.Thrum.ProposedBy {
		t.Errorf("ProposedBy: flat=%q nested=%q", fm.Thrum.ProposedBy, nestedFm.Thrum.ProposedBy)
	}
	if !fm.Thrum.CreatedAt.Equal(nestedFm.Thrum.CreatedAt) {
		t.Errorf("CreatedAt: flat=%v nested=%v", fm.Thrum.CreatedAt, nestedFm.Thrum.CreatedAt)
	}
	if fm.Thrum.SourcePattern != nestedFm.Thrum.SourcePattern {
		t.Errorf("SourcePattern: flat=%+v nested=%+v", fm.Thrum.SourcePattern, nestedFm.Thrum.SourcePattern)
	}
	if fm.Thrum.Review.CheckSkillVersion != nestedFm.Thrum.Review.CheckSkillVersion {
		t.Errorf("CheckSkillVersion: flat=%q nested=%q",
			fm.Thrum.Review.CheckSkillVersion, nestedFm.Thrum.Review.CheckSkillVersion)
	}
}

func TestFrontmatter_RoundTripLossless(t *testing.T) {
	t.Parallel()

	original := &Frontmatter{
		Name:         "rt-fixture",
		Description:  "round-trip test",
		AllowedTools: []string{"Bash", "Read"},
		Version:      "1.0.0",
		Thrum: ThrumProvenance{
			ProposedBy:    "@researcher_x",
			PromotedBy:    "@coordinator_main",
			CreatedAt:     time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC),
			TriggerReason: "round-trip",
			SourcePattern: SourcePattern{Type: "bd-issue", Ref: "thrum-6qmf.2.1"},
			Review: ReviewBlock{
				ReviewedBy:        "@coordinator_main",
				ReviewedAt:        time.Date(2026, 5, 15, 17, 5, 0, 0, time.UTC),
				CheckSkillVersion: "0.0.0-stub",
				Revisions: []RevisionEntry{
					{
						MsgThreadID: "msg_01ROUNDTRIP",
						ProposedBy:  "@researcher_x",
						At:          time.Date(2026, 5, 15, 16, 30, 0, 0, time.UTC),
					},
				},
			},
		},
	}

	encoded, err := EncodeFrontmatter(original)
	if err != nil {
		t.Fatalf("EncodeFrontmatter: %v", err)
	}
	// Append a fake body so the parser has a delimiter to split on.
	withBody := append(encoded, []byte("body\n")...)

	decoded, _, err := ParseFrontmatter(withBody)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if decoded.Name != original.Name {
		t.Errorf("Name drift: %q", decoded.Name)
	}
	if !decoded.Thrum.CreatedAt.Equal(original.Thrum.CreatedAt) {
		t.Errorf("CreatedAt drift: %v", decoded.Thrum.CreatedAt)
	}
	if decoded.Thrum.SourcePattern != original.Thrum.SourcePattern {
		t.Errorf("SourcePattern drift: %+v", decoded.Thrum.SourcePattern)
	}
	if len(decoded.Thrum.Review.Revisions) != 1 ||
		decoded.Thrum.Review.Revisions[0].MsgThreadID != "msg_01ROUNDTRIP" {
		t.Errorf("Revisions drift: %+v", decoded.Thrum.Review.Revisions)
	}
}

func TestFrontmatter_MissingDelimiter(t *testing.T) {
	t.Parallel()

	raw := []byte("# just a body\nno frontmatter here\n")
	fm, body, err := ParseFrontmatter(raw)
	if !errors.Is(err, ErrNoFrontmatter) {
		t.Fatalf("expected ErrNoFrontmatter, got %v", err)
	}
	if fm != nil {
		t.Errorf("expected nil Frontmatter on no-delimiter, got %+v", fm)
	}
	if !bytes.Equal(body, raw) {
		t.Errorf("body should be raw: %q", body)
	}
}

func TestFrontmatter_MalformedYaml(t *testing.T) {
	t.Parallel()

	raw := []byte(`---
name: bad
description: [malformed
thrum: : :
---

body
`)
	_, _, err := ParseFrontmatter(raw)
	if !errors.Is(err, ErrFrontmatterInvalid) {
		t.Fatalf("expected ErrFrontmatterInvalid, got %v", err)
	}
}

func TestFrontmatter_RevisionsArrayOfObjects(t *testing.T) {
	t.Parallel()

	raw := []byte(`---
name: rev-fixture
description: revisions shape
thrum:
  proposed_by: "@x"
  review:
    revisions:
      - msg_thread_id: msg_a
        proposed_by: "@y"
        at: 2026-05-15T10:00:00Z
      - msg_thread_id: msg_b
        proposed_by: "@z"
        at: 2026-05-15T11:00:00Z
---

body
`)
	fm, _, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if len(fm.Thrum.Review.Revisions) != 2 {
		t.Fatalf("expected 2 revisions, got %d", len(fm.Thrum.Review.Revisions))
	}
	if fm.Thrum.Review.Revisions[0].MsgThreadID != "msg_a" {
		t.Errorf("Revisions[0].MsgThreadID = %q", fm.Thrum.Review.Revisions[0].MsgThreadID)
	}
	if fm.Thrum.Review.Revisions[1].ProposedBy != "@z" {
		t.Errorf("Revisions[1].ProposedBy = %q", fm.Thrum.Review.Revisions[1].ProposedBy)
	}
}

func TestFrontmatter_SecretScanOverridesArray(t *testing.T) {
	t.Parallel()

	raw := []byte(`---
name: scan-fixture
description: secret_scan_overrides shape
thrum:
  proposed_by: "@x"
  review:
    secret_scan_overrides:
      - pattern: "AKIA[0-9A-Z]{16}"
        reason: test fixture
        reviewed_by: "@coordinator_main"
        reviewed_at: 2026-05-15T17:00:00Z
---

body
`)
	fm, _, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if len(fm.Thrum.Review.SecretScanOverrides) != 1 {
		t.Fatalf("expected 1 override, got %d", len(fm.Thrum.Review.SecretScanOverrides))
	}
	if fm.Thrum.Review.SecretScanOverrides[0].Pattern != "AKIA[0-9A-Z]{16}" {
		t.Errorf("Pattern = %q", fm.Thrum.Review.SecretScanOverrides[0].Pattern)
	}
	if fm.Thrum.Review.SecretScanOverrides[0].Reason != "test fixture" {
		t.Errorf("Reason = %q", fm.Thrum.Review.SecretScanOverrides[0].Reason)
	}
}

func TestFrontmatter_AbsentThrumBlockProduceZeroValue(t *testing.T) {
	t.Parallel()

	raw := []byte(`---
name: draft-fixture
description: pre-promote draft, no thrum block
---

body
`)
	fm, _, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if fm.Name != "draft-fixture" {
		t.Errorf("Name = %q", fm.Name)
	}
	if fm.Thrum.ProposedBy != "" {
		t.Errorf("ProposedBy should be zero, got %q", fm.Thrum.ProposedBy)
	}
	if !fm.Thrum.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be zero, got %v", fm.Thrum.CreatedAt)
	}
	if fm.Thrum.SourcePattern != (SourcePattern{}) {
		t.Errorf("SourcePattern should be zero, got %+v", fm.Thrum.SourcePattern)
	}
}

// TestFrontmatter_EncodeFlatRoundTripsViaParse pins the flat-form
// encode for runtime-loader compat: encoding a Frontmatter via
// EncodeFlatFrontmatter then parsing it back must yield the same
// ThrumProvenance. This is the mirror-time conversion path; the
// parser already handles both forms.
func TestFrontmatter_EncodeFlatRoundTripsViaParse(t *testing.T) {
	t.Parallel()

	original := &Frontmatter{
		Name:        "flat-rt",
		Description: "flat round-trip",
		Thrum: ThrumProvenance{
			ProposedBy:    "@researcher_x",
			PromotedBy:    "@coordinator_main",
			CreatedAt:     time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC),
			TriggerReason: "flat encoding",
			SourcePattern: SourcePattern{Type: "bd-issue", Ref: "thrum-test"},
			Review: ReviewBlock{
				ReviewedBy:        "@coordinator_main",
				ReviewedAt:        time.Date(2026, 5, 15, 17, 5, 0, 0, time.UTC),
				CheckSkillVersion: "0.0.0-stub",
			},
		},
	}

	encoded, err := EncodeFlatFrontmatter(original)
	if err != nil {
		t.Fatalf("EncodeFlatFrontmatter: %v", err)
	}
	withBody := append(encoded, []byte("body\n")...)

	decoded, _, err := ParseFrontmatter(withBody)
	if err != nil {
		t.Fatalf("ParseFrontmatter on flat-encoded: %v", err)
	}
	if decoded.Thrum.ProposedBy != original.Thrum.ProposedBy {
		t.Errorf("ProposedBy: got %q want %q", decoded.Thrum.ProposedBy, original.Thrum.ProposedBy)
	}
	if !decoded.Thrum.CreatedAt.Equal(original.Thrum.CreatedAt) {
		t.Errorf("CreatedAt: got %v want %v", decoded.Thrum.CreatedAt, original.Thrum.CreatedAt)
	}
	if decoded.Thrum.SourcePattern != original.Thrum.SourcePattern {
		t.Errorf("SourcePattern drift: %+v vs %+v", decoded.Thrum.SourcePattern, original.Thrum.SourcePattern)
	}
}
