package skills

import (
	"path/filepath"
	"testing"
	"time"
)

// promotedFixture returns a fully-populated Skill that should validate
// cleanly under ValidatePromoted. Helper centralizes the field shape so
// individual tests can mutate one field and re-assert.
func promotedFixture() *Skill {
	return &Skill{
		Name: "demo-skill",
		Path: filepath.FromSlash(".thrum/skills/demo-skill/SKILL.md"),
		Frontmatter: Frontmatter{
			Name:        "demo-skill",
			Description: "Demo for validator tests.",
			Thrum: ThrumProvenance{
				ProposedBy:    "@researcher_x",
				PromotedBy:    "@coordinator_main",
				CreatedAt:     time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC),
				TriggerReason: "demo trigger",
				Review: ReviewBlock{
					ReviewedBy:        "@coordinator_main",
					ReviewedAt:        time.Date(2026, 5, 15, 17, 5, 0, 0, time.UTC),
					CheckSkillVersion: "0.0.0-stub",
				},
			},
		},
	}
}

func proposedFixture() *ProposedSkill {
	return &ProposedSkill{
		Skill: Skill{
			Name: "draft-skill",
			Path: filepath.FromSlash(".thrum/agents/researcher_x/proposed-skills/draft-skill/SKILL.md"),
			Frontmatter: Frontmatter{
				Name:        "draft-skill",
				Description: "Draft for propose-time validator tests.",
				Thrum: ThrumProvenance{
					ProposedBy:    "@researcher_x",
					TriggerReason: "drafting",
				},
			},
		},
		Author:           "researcher_x",
		ProposedAt:       time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
		FrontmatterValid: true,
	}
}

func findingsHaveKind(findings []Finding, kind string) bool {
	for _, f := range findings {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

func TestValidator_NameRegexEnforced(t *testing.T) {
	t.Parallel()

	v := NewValidator()
	skill := promotedFixture()
	skill.Name = "Bad Name"
	skill.Frontmatter.Name = "Bad Name"
	skill.Path = filepath.FromSlash(".thrum/skills/Bad Name/SKILL.md")

	findings := v.ValidatePromoted(skill)
	if !findingsHaveKind(findings, "regex_violation") {
		t.Errorf("expected regex_violation, got %+v", findings)
	}
}

func TestValidator_NameDirMismatch(t *testing.T) {
	t.Parallel()

	v := NewValidator()
	skill := promotedFixture()
	skill.Path = filepath.FromSlash(".thrum/skills/other-name/SKILL.md")

	findings := v.ValidatePromoted(skill)
	if !findingsHaveKind(findings, "name_mismatch") {
		t.Errorf("expected name_mismatch, got %+v", findings)
	}
}

func TestValidator_RequiredFieldsPromote(t *testing.T) {
	t.Parallel()

	v := NewValidator()
	skill := promotedFixture()
	skill.Frontmatter.Thrum.Review.ReviewedBy = ""

	findings := v.ValidatePromoted(skill)
	if !findingsHaveKind(findings, "missing_required") {
		t.Errorf("expected missing_required, got %+v", findings)
	}
}

func TestValidator_RequiredFieldsPropose(t *testing.T) {
	t.Parallel()

	v := NewValidator()
	proposed := proposedFixture()
	// Review block absent — propose-time should not flag this.
	findings := v.ValidateProposed(proposed)
	for _, f := range findings {
		if f.Kind == "missing_required" && f.Path == "thrum.review.reviewed_by" {
			t.Errorf("propose-time wrongly flagged review.reviewed_by missing: %+v", findings)
		}
	}
	// Loose pass should be clean.
	if len(findings) != 0 {
		t.Errorf("propose-time fixture should produce no findings, got %+v", findings)
	}
}

func TestValidator_DuplicateThrumBlock(t *testing.T) {
	t.Parallel()

	// Raw YAML with two thrum: blocks at the top level — the merge-conflict
	// pattern E10.8 defends against. yaml.v3 silently keeps the last
	// occurrence on Unmarshal, so the detection has to walk the parsed
	// Node tree directly.
	raw := []byte(`name: dupe-skill
description: Has two thrum blocks
thrum:
  proposed_by: "@a"
  promoted_by: "@b"
  created_at: 2026-05-15T17:00:00Z
  trigger_reason: x
  review:
    reviewed_by: "@b"
    reviewed_at: 2026-05-15T17:05:00Z
    check_skill_version: 0.0.0-stub
thrum:
  proposed_by: "@conflict"
  promoted_by: "@c"
  created_at: 2026-05-15T18:00:00Z
  trigger_reason: y
  review:
    reviewed_by: "@c"
    reviewed_at: 2026-05-15T18:05:00Z
    check_skill_version: 0.0.0-stub
`)
	v := NewValidator()
	findings := v.ValidateRawFrontmatter(raw)
	if !findingsHaveKind(findings, "duplicate_field") {
		t.Errorf("expected duplicate_field, got %+v", findings)
	}
}

func TestValidator_ValidPromotedSkill(t *testing.T) {
	t.Parallel()

	v := NewValidator()
	skill := promotedFixture()
	findings := v.ValidatePromoted(skill)
	if len(findings) != 0 {
		t.Errorf("clean promoted fixture produced findings: %+v", findings)
	}
}

func TestValidator_ValidProposedSkill(t *testing.T) {
	t.Parallel()

	v := NewValidator()
	proposed := proposedFixture()
	findings := v.ValidateProposed(proposed)
	if len(findings) != 0 {
		t.Errorf("clean proposed fixture produced findings: %+v", findings)
	}
}

func TestValidator_TriggerReasonRequired(t *testing.T) {
	t.Parallel()

	v := NewValidator()
	proposed := proposedFixture()
	proposed.Frontmatter.Thrum.TriggerReason = ""

	findings := v.ValidateProposed(proposed)
	var found bool
	for _, f := range findings {
		if f.Kind == "missing_required" && f.Path == "thrum.trigger_reason" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected missing_required at thrum.trigger_reason, got %+v", findings)
	}
}

// Validate is the plan-AC entrypoint that runs the strict promote-time
// pass. Pins the signature so downstream consumers (E10.8 post-merge
// defense) can rely on the plan-named API.
func TestValidator_PlanAPI(t *testing.T) {
	t.Parallel()

	v := NewValidator()
	if findings := v.Validate(promotedFixture()); len(findings) != 0 {
		t.Errorf("Validate(*Skill) clean fixture findings: %+v", findings)
	}
}
