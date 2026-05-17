package skills

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// skillNameRegex enforces the on-disk skill-name shape per design-spec
// §9.1: lowercase letter prefix, then lowercase letters / digits /
// hyphens, total length 1..64. Applied to both the frontmatter `name:`
// field and the parent directory name.
var skillNameRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// Finding is one diagnostic produced by Validator. Kind drives caller
// switch statements (promote rejects on any Kind besides minor info);
// Path is the frontmatter dot-path that violated the rule (or empty
// for whole-document issues like duplicate_field); Detail is a short
// human-readable extension.
//
// Stable Kind values: frontmatter_invalid, duplicate_field,
// missing_required, name_mismatch, regex_violation.
type Finding struct {
	Kind   string
	Path   string
	Detail string
}

// Validator runs schema-conformance checks against Skill /
// ProposedSkill values (design-spec §9.2 + §15). The strict pass
// (ValidatePromoted) is the promote-time gate; the loose pass
// (ValidateProposed) is the propose-time gate (review block absent is
// acceptable).
//
// Validator carries no state; the constructor exists for symmetry with
// Library and to allow future option fields (e.g. an allow-list of
// reserved skill names) without breaking the call site.
type Validator struct{}

// NewValidator returns a default Validator. No state today — the
// constructor exists so downstream code can construct via the
// (potentially future) NewValidator(opts...) shape without churning
// signatures.
func NewValidator() *Validator { return &Validator{} }

// Validate is the plan-AC entrypoint (plan §E8.4): runs the strict
// promote-time pass over a *Skill. Empty slice means valid.
//
// Callers that hold a *ProposedSkill use ValidateProposed; callers
// that have only raw frontmatter bytes (the merge-conflict defense
// for E10.8) use ValidateRawFrontmatter.
func (v *Validator) Validate(s *Skill) []Finding {
	return v.ValidatePromoted(s)
}

// ValidatePromoted enforces the strict frontmatter contract used at
// promote time (design-spec §9.2 + §13.2 — Q4 / Q8). Findings cover
// regex, dir-name match, and every promote-required `thrum.*` field.
func (v *Validator) ValidatePromoted(s *Skill) []Finding {
	if s == nil {
		return []Finding{{Kind: "frontmatter_invalid", Detail: "nil skill"}}
	}
	var findings []Finding

	findings = append(findings, v.checkName(s)...)

	// Required at promote-time. Order chosen so the most-actionable
	// fields surface first; downstream code that short-circuits on the
	// first finding still gets a useful error. The top-level `name`
	// field is required (checkName skips its regex check when name is
	// empty so callers don't double-report).
	required := []struct {
		path string
		val  string
	}{
		{"name", s.Frontmatter.Name},
		{"thrum.proposed_by", s.Frontmatter.Thrum.ProposedBy},
		{"thrum.promoted_by", s.Frontmatter.Thrum.PromotedBy},
		{"thrum.trigger_reason", s.Frontmatter.Thrum.TriggerReason},
		{"thrum.review.reviewed_by", s.Frontmatter.Thrum.Review.ReviewedBy},
		{"thrum.review.check_skill_version", s.Frontmatter.Thrum.Review.CheckSkillVersion},
	}
	for _, r := range required {
		if r.val == "" {
			findings = append(findings, Finding{
				Kind: "missing_required",
				Path: r.path,
			})
		}
	}
	if s.Frontmatter.Thrum.CreatedAt.IsZero() {
		findings = append(findings, Finding{Kind: "missing_required", Path: "thrum.created_at"})
	}
	if s.Frontmatter.Thrum.Review.ReviewedAt.IsZero() {
		findings = append(findings, Finding{Kind: "missing_required", Path: "thrum.review.reviewed_at"})
	}

	return findings
}

// ValidateProposed enforces the loose propose-time contract. Required
// fields shrink to name + description + thrum.proposed_by +
// thrum.trigger_reason; review block absent is acceptable per spec
// §13.1.
func (v *Validator) ValidateProposed(p *ProposedSkill) []Finding {
	if p == nil {
		return []Finding{{Kind: "frontmatter_invalid", Detail: "nil proposed skill"}}
	}
	var findings []Finding

	findings = append(findings, v.checkName(&p.Skill)...)

	if p.Frontmatter.Name == "" {
		findings = append(findings, Finding{Kind: "missing_required", Path: "name"})
	}
	if p.Frontmatter.Description == "" {
		findings = append(findings, Finding{Kind: "missing_required", Path: "description"})
	}
	if p.Frontmatter.Thrum.ProposedBy == "" {
		findings = append(findings, Finding{Kind: "missing_required", Path: "thrum.proposed_by"})
	}
	if p.Frontmatter.Thrum.TriggerReason == "" {
		findings = append(findings, Finding{Kind: "missing_required", Path: "thrum.trigger_reason"})
	}

	return findings
}

// checkName enforces the name regex + dir-name match. Both rules
// apply at propose-time and promote-time, so they live in a shared
// helper. The empty-name case is intentionally left to the caller's
// missing_required check — otherwise a missing name would surface as
// two findings (regex_violation + missing_required), and callers
// that switch on Kind would see redundant entries.
func (v *Validator) checkName(s *Skill) []Finding {
	var findings []Finding

	if s.Frontmatter.Name != "" && !skillNameRegex.MatchString(s.Frontmatter.Name) {
		findings = append(findings, Finding{
			Kind:   "regex_violation",
			Path:   "name",
			Detail: fmt.Sprintf("%q does not match ^[a-z][a-z0-9-]{0,63}$", s.Frontmatter.Name),
		})
	}

	if s.Path != "" {
		dir := filepath.Base(filepath.Dir(s.Path))
		if dir != "" && dir != "." && s.Frontmatter.Name != "" && dir != s.Frontmatter.Name {
			findings = append(findings, Finding{
				Kind:   "name_mismatch",
				Path:   "name",
				Detail: fmt.Sprintf("directory %q does not match frontmatter name %q", dir, s.Frontmatter.Name),
			})
		}
	}
	return findings
}

// ValidateRawFrontmatter detects merge-conflict patterns in the raw
// frontmatter YAML — primarily, duplicated top-level keys like
// `thrum:` that yaml.v3 silently collapses on Unmarshal. Walks the
// parsed MappingNode directly so the check is robust against SKILL.md
// body content that happens to contain `thrum:` as text.
//
// Returns Findings for any duplicated top-level key (kind:
// duplicate_field, path: <key>). Other malformed YAML surfaces as a
// single frontmatter_invalid finding so callers can short-circuit.
func (v *Validator) ValidateRawFrontmatter(raw []byte) []Finding {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return []Finding{{Kind: "frontmatter_invalid", Detail: err.Error()}}
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil
	}

	seen := map[string]int{}
	for i := 0; i < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if key.Kind != yaml.ScalarNode {
			continue
		}
		seen[key.Value]++
	}
	// Sort keys so the findings slice is deterministic across runs.
	// Map iteration in Go is intentionally randomized; without this
	// sort the order would vary, breaking any caller that asserts a
	// specific finding position (e.g. table-driven tests).
	dupKeys := make([]string, 0, len(seen))
	for key, count := range seen {
		if count > 1 {
			dupKeys = append(dupKeys, key)
		}
	}
	sort.Strings(dupKeys)
	findings := make([]Finding, 0, len(dupKeys))
	for _, key := range dupKeys {
		findings = append(findings, Finding{
			Kind:   "duplicate_field",
			Path:   key,
			Detail: fmt.Sprintf("appears %d times in frontmatter", seen[key]),
		})
	}
	return findings
}
