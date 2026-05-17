package skills

import (
	"errors"
	"time"
)

// CheckSkillStubVersion is the placeholder version stamped into
// thrum.review.check_skill_version during the C-B2 stub window
// (canonical §8.3). When C-B2 lands and the real check-the-skill
// meta-skill is invoked, callers stamp the meta-skill's own version
// string instead.
const CheckSkillStubVersion = "0.0.0-stub"

// Stamper applies the thrum.review.* fields to a Skill at promote
// time. Tests inject a deterministic clock; production callers use
// NewStamper(time.Now).
type Stamper struct {
	clock func() time.Time
}

// NewStamper constructs a Stamper with the given clock. Pass
// time.Now for production. Pass a fixed-value closure for tests so
// timestamps are deterministic.
func NewStamper(clock func() time.Time) *Stamper {
	if clock == nil {
		clock = time.Now
	}
	return &Stamper{clock: clock}
}

// StampCreate fills the thrum.* fields appropriate to a fresh
// promote (mode=create per spec §13.2). Mutates s in place; the
// caller persists by re-encoding frontmatter via EncodeFrontmatter
// (E11.1).
//
// Per AC: fills promoted_by, created_at (now), review.reviewed_by,
// review.reviewed_at (now), review.check_skill_version. Initializes
// revisions to a non-nil empty slice so the marshaled YAML emits
// `revisions: []` rather than absent — explicit empty signals
// "ready to accept revisions".
func (s *Stamper) StampCreate(skill *Skill, reviewer, checkVersion string) error {
	if skill == nil {
		return errors.New("skills: StampCreate(nil)")
	}
	if reviewer == "" {
		return errors.New("skills: StampCreate: reviewer required")
	}
	now := s.clock().UTC()
	skill.Frontmatter.Thrum.PromotedBy = reviewer
	skill.Frontmatter.Thrum.CreatedAt = now
	skill.Frontmatter.Thrum.Review.ReviewedBy = reviewer
	skill.Frontmatter.Thrum.Review.ReviewedAt = now
	skill.Frontmatter.Thrum.Review.CheckSkillVersion = checkVersion
	if skill.Frontmatter.Thrum.Review.Revisions == nil {
		skill.Frontmatter.Thrum.Review.Revisions = []RevisionEntry{}
	}
	return nil
}

// StampEdit applies an edit-promote (mode=edit per Q8 symmetric
// flow). Preserves the original created_at, refreshes reviewed_at,
// and appends the supplied RevisionEntry to revisions[]. The
// reviewer + check version are NOT re-stamped here — the caller
// passes them via the existing skill state (StampCreate already set
// them on the previous promote).
func (s *Stamper) StampEdit(skill *Skill, existingCreatedAt time.Time, newRevisionEntry RevisionEntry) error {
	if skill == nil {
		return errors.New("skills: StampEdit(nil)")
	}
	if existingCreatedAt.IsZero() {
		return errors.New("skills: StampEdit: existingCreatedAt required (preserves original promote timestamp)")
	}
	skill.Frontmatter.Thrum.CreatedAt = existingCreatedAt
	skill.Frontmatter.Thrum.Review.ReviewedAt = s.clock().UTC()
	skill.Frontmatter.Thrum.Review.Revisions = append(skill.Frontmatter.Thrum.Review.Revisions, newRevisionEntry)
	return nil
}

// RecordSecretScanOverride appends a secret-scan override entry to
// the skill's review block. Called by the promote handler when the
// coordinator passes --allow-secret-pattern + a reason.
//
// The override is recorded with the reviewer ID and the wall-clock
// timestamp at recording time — both are persistent audit trail
// per spec §14 (secret-scan).
func (s *Stamper) RecordSecretScanOverride(skill *Skill, pattern, reason, reviewer string) error {
	if skill == nil {
		return errors.New("skills: RecordSecretScanOverride(nil)")
	}
	if pattern == "" {
		return errors.New("skills: RecordSecretScanOverride: pattern required")
	}
	if reason == "" {
		return errors.New("skills: RecordSecretScanOverride: reason required (audit trail)")
	}
	if reviewer == "" {
		return errors.New("skills: RecordSecretScanOverride: reviewer required")
	}
	skill.Frontmatter.Thrum.Review.SecretScanOverrides = append(
		skill.Frontmatter.Thrum.Review.SecretScanOverrides,
		SecretScanOverride{
			Pattern:    pattern,
			Reason:     reason,
			ReviewedBy: reviewer,
			ReviewedAt: s.clock().UTC(),
		},
	)
	return nil
}
