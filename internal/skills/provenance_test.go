package skills

import (
	"testing"
	"time"
)

// fixedClock returns a clock func() that always returns the supplied
// time — used so stamper tests can assert on exact field values.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func freshSkill() *Skill {
	return &Skill{
		Name: "demo",
		Path: ".thrum/skills/demo/SKILL.md",
		Frontmatter: Frontmatter{
			Name:        "demo",
			Description: "fixture",
		},
	}
}

func TestStamper_StampCreateFillsAllFields(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC)
	s := NewStamper(fixedClock(now))
	skill := freshSkill()

	if err := s.StampCreate(skill, "@coordinator_main", "1.0.0"); err != nil {
		t.Fatalf("StampCreate: %v", err)
	}
	prov := skill.Frontmatter.Thrum
	if prov.PromotedBy != "@coordinator_main" {
		t.Errorf("PromotedBy = %q", prov.PromotedBy)
	}
	if !prov.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v want %v", prov.CreatedAt, now)
	}
	if prov.Review.ReviewedBy != "@coordinator_main" {
		t.Errorf("Review.ReviewedBy = %q", prov.Review.ReviewedBy)
	}
	if !prov.Review.ReviewedAt.Equal(now) {
		t.Errorf("Review.ReviewedAt = %v", prov.Review.ReviewedAt)
	}
	if prov.Review.CheckSkillVersion != "1.0.0" {
		t.Errorf("CheckSkillVersion = %q", prov.Review.CheckSkillVersion)
	}
	if prov.Review.Revisions == nil {
		t.Errorf("Revisions should be non-nil empty slice, got nil")
	}
	if len(prov.Review.Revisions) != 0 {
		t.Errorf("Revisions should be empty, got %d entries", len(prov.Review.Revisions))
	}
}

func TestStamper_StampCreateUsesStubVersion(t *testing.T) {
	t.Parallel()

	s := NewStamper(fixedClock(time.Now().UTC()))
	skill := freshSkill()

	if err := s.StampCreate(skill, "@coordinator_main", CheckSkillStubVersion); err != nil {
		t.Fatalf("StampCreate: %v", err)
	}
	if skill.Frontmatter.Thrum.Review.CheckSkillVersion != "0.0.0-stub" {
		t.Errorf("CheckSkillVersion = %q, want %q",
			skill.Frontmatter.Thrum.Review.CheckSkillVersion, "0.0.0-stub")
	}
}

func TestStamper_StampEditPreservesCreatedAt(t *testing.T) {
	t.Parallel()

	originalCreate := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	editTime := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)

	skill := freshSkill()
	skill.Frontmatter.Thrum.CreatedAt = originalCreate
	skill.Frontmatter.Thrum.Review.ReviewedAt = originalCreate

	s := NewStamper(fixedClock(editTime))
	rev := RevisionEntry{MsgThreadID: "msg_01EDIT", ProposedBy: "@researcher_x", At: editTime}
	if err := s.StampEdit(skill, originalCreate, rev); err != nil {
		t.Fatalf("StampEdit: %v", err)
	}
	if !skill.Frontmatter.Thrum.CreatedAt.Equal(originalCreate) {
		t.Errorf("CreatedAt should be preserved: got %v want %v",
			skill.Frontmatter.Thrum.CreatedAt, originalCreate)
	}
	if !skill.Frontmatter.Thrum.Review.ReviewedAt.Equal(editTime) {
		t.Errorf("ReviewedAt should refresh to editTime: got %v want %v",
			skill.Frontmatter.Thrum.Review.ReviewedAt, editTime)
	}
}

func TestStamper_StampEditAppendsToRevisions(t *testing.T) {
	t.Parallel()

	originalCreate := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	editTime := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)

	skill := freshSkill()
	skill.Frontmatter.Thrum.CreatedAt = originalCreate
	// Pre-seed an existing revision so we can assert append, not replace.
	skill.Frontmatter.Thrum.Review.Revisions = []RevisionEntry{
		{MsgThreadID: "msg_01OLDREV", ProposedBy: "@researcher_x", At: originalCreate},
	}

	s := NewStamper(fixedClock(editTime))
	newRev := RevisionEntry{MsgThreadID: "msg_01NEWREV", ProposedBy: "@researcher_y", At: editTime}
	if err := s.StampEdit(skill, originalCreate, newRev); err != nil {
		t.Fatalf("StampEdit: %v", err)
	}
	revs := skill.Frontmatter.Thrum.Review.Revisions
	if len(revs) != 2 {
		t.Fatalf("Revisions len = %d, want 2", len(revs))
	}
	if revs[0].MsgThreadID != "msg_01OLDREV" {
		t.Errorf("revisions[0] preserved? got %q", revs[0].MsgThreadID)
	}
	if revs[1].MsgThreadID != "msg_01NEWREV" {
		t.Errorf("revisions[1] appended? got %q", revs[1].MsgThreadID)
	}
}

func TestStamper_RecordSecretScanOverride(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC)
	s := NewStamper(fixedClock(now))
	skill := freshSkill()

	if err := s.RecordSecretScanOverride(skill, "AKIA[0-9A-Z]{16}", "test fixture", "@coordinator_main"); err != nil {
		t.Fatalf("RecordSecretScanOverride: %v", err)
	}
	overrides := skill.Frontmatter.Thrum.Review.SecretScanOverrides
	if len(overrides) != 1 {
		t.Fatalf("overrides len = %d, want 1", len(overrides))
	}
	if overrides[0].Pattern != "AKIA[0-9A-Z]{16}" {
		t.Errorf("Pattern = %q", overrides[0].Pattern)
	}
	if overrides[0].Reason != "test fixture" {
		t.Errorf("Reason = %q", overrides[0].Reason)
	}
	if overrides[0].ReviewedBy != "@coordinator_main" {
		t.Errorf("ReviewedBy = %q", overrides[0].ReviewedBy)
	}
	if !overrides[0].ReviewedAt.Equal(now) {
		t.Errorf("ReviewedAt = %v", overrides[0].ReviewedAt)
	}
}

func TestStamper_MultipleOverridesAccumulate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC)
	s := NewStamper(fixedClock(now))
	skill := freshSkill()

	if err := s.RecordSecretScanOverride(skill, "pat1", "reason1", "@coordinator_main"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.RecordSecretScanOverride(skill, "pat2", "reason2", "@coordinator_main"); err != nil {
		t.Fatalf("second: %v", err)
	}
	overrides := skill.Frontmatter.Thrum.Review.SecretScanOverrides
	if len(overrides) != 2 {
		t.Fatalf("overrides len = %d, want 2", len(overrides))
	}
	if overrides[0].Pattern != "pat1" || overrides[1].Pattern != "pat2" {
		t.Errorf("ordering drift: %+v", overrides)
	}
}

// TestStamper_StampCreatePreservesProposedBy pins the design intent
// per spec §9.2: thrum.proposed_by is filled by the proposer (the
// researcher who drafts the SKILL.md), NOT by the coordinator at
// stamp time. The stamper must not overwrite an existing
// proposed_by — doing so would lose attribution.
func TestStamper_StampCreatePreservesProposedBy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC)
	s := NewStamper(fixedClock(now))
	skill := freshSkill()
	skill.Frontmatter.Thrum.ProposedBy = "@researcher_x"

	if err := s.StampCreate(skill, "@coordinator_main", "1.0.0"); err != nil {
		t.Fatalf("StampCreate: %v", err)
	}
	if skill.Frontmatter.Thrum.ProposedBy != "@researcher_x" {
		t.Errorf("ProposedBy overwritten by stamper: got %q, want %q (preserved from proposer)",
			skill.Frontmatter.Thrum.ProposedBy, "@researcher_x")
	}
}

// Stamper{nil,...} guards. New() defaults to time.Now so nil-clock
// is not an issue at construction; missing required args at the
// method layer should produce a clear error.
func TestStamper_RequiredArgsErrors(t *testing.T) {
	t.Parallel()

	s := NewStamper(fixedClock(time.Now().UTC()))
	if err := s.StampCreate(freshSkill(), "", "1.0.0"); err == nil {
		t.Errorf("StampCreate(reviewer=\"\") should error")
	}
	if err := s.StampEdit(freshSkill(), time.Time{}, RevisionEntry{}); err == nil {
		t.Errorf("StampEdit(zero existingCreatedAt) should error")
	}
	if err := s.RecordSecretScanOverride(freshSkill(), "", "r", "@c"); err == nil {
		t.Errorf("RecordSecretScanOverride(pattern=\"\") should error")
	}
	if err := s.RecordSecretScanOverride(freshSkill(), "p", "", "@c"); err == nil {
		t.Errorf("RecordSecretScanOverride(reason=\"\") should error")
	}
	if err := s.RecordSecretScanOverride(freshSkill(), "p", "r", ""); err == nil {
		t.Errorf("RecordSecretScanOverride(reviewer=\"\") should error")
	}
}
