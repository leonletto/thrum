package skills

import (
	"encoding/json"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestSkill_TypeRoundTrip(t *testing.T) {
	t.Parallel()

	original := Frontmatter{
		Name:         "fewer-permission-prompts",
		Description:  "Scan transcripts; emit allowlist.",
		AllowedTools: []string{"Bash", "Read"},
		Version:      "1.0.0",
		Author:       "@coordinator_main",
		License:      "Apache-2.0",
		Thrum: ThrumProvenance{
			ProposedBy:    "@researcher_skills",
			PromotedBy:    "@coordinator_main",
			CreatedAt:     time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC),
			TriggerReason: "Repeated permission prompts during E2E runs",
			SourcePattern: SourcePattern{Type: "bd-issue", Ref: "thrum-6qmf.2"},
			Review: ReviewBlock{
				ReviewedBy:        "@coordinator_main",
				ReviewedAt:        time.Date(2026, 5, 15, 17, 5, 0, 0, time.UTC),
				CheckSkillVersion: "0.0.0-stub",
				Revisions: []RevisionEntry{
					{
						MsgThreadID: "msg_01KSXTEST",
						ProposedBy:  "@researcher_skills",
						At:          time.Date(2026, 5, 15, 16, 30, 0, 0, time.UTC),
					},
				},
				SecretScanOverrides: []SecretScanOverride{
					{
						Pattern:    "AKIA[0-9A-Z]{16}",
						Reason:     "test fixture, not a live key",
						ReviewedBy: "@coordinator_main",
						ReviewedAt: time.Date(2026, 5, 15, 17, 4, 0, 0, time.UTC),
					},
				},
			},
		},
	}

	// JSON round-trip
	jsonBytes, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var decodedJSON Frontmatter
	if err := json.Unmarshal(jsonBytes, &decodedJSON); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if !decodedJSON.Thrum.CreatedAt.Equal(original.Thrum.CreatedAt) {
		t.Fatalf("CreatedAt drift: got %v want %v", decodedJSON.Thrum.CreatedAt, original.Thrum.CreatedAt)
	}
	if decodedJSON.Name != original.Name || decodedJSON.Description != original.Description {
		t.Fatalf("name/description drift after JSON round-trip")
	}
	if len(decodedJSON.AllowedTools) != 2 || decodedJSON.AllowedTools[0] != "Bash" {
		t.Fatalf("allowed-tools drift after JSON round-trip: %v", decodedJSON.AllowedTools)
	}
	if decodedJSON.Thrum.SourcePattern.Type != "bd-issue" || decodedJSON.Thrum.SourcePattern.Ref != "thrum-6qmf.2" {
		t.Fatalf("source_pattern drift after JSON round-trip: %+v", decodedJSON.Thrum.SourcePattern)
	}
	if len(decodedJSON.Thrum.Review.Revisions) != 1 || decodedJSON.Thrum.Review.Revisions[0].MsgThreadID != "msg_01KSXTEST" {
		t.Fatalf("revisions drift after JSON round-trip: %+v", decodedJSON.Thrum.Review.Revisions)
	}

	// YAML round-trip (canonical on-disk form is YAML frontmatter)
	yamlBytes, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}
	var decodedYAML Frontmatter
	if err := yaml.Unmarshal(yamlBytes, &decodedYAML); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if decodedYAML.Name != original.Name {
		t.Fatalf("YAML name drift: got %q want %q", decodedYAML.Name, original.Name)
	}
	if decodedYAML.Thrum.PromotedBy != original.Thrum.PromotedBy {
		t.Fatalf("YAML promoted_by drift: got %q want %q", decodedYAML.Thrum.PromotedBy, original.Thrum.PromotedBy)
	}
	if len(decodedYAML.AllowedTools) != 2 {
		t.Fatalf("YAML allowed-tools drift: %v", decodedYAML.AllowedTools)
	}
}

func TestProposedSkill_PartialFrontmatter(t *testing.T) {
	t.Parallel()

	// A proposed skill before promote — the `thrum:` block is absent or zero.
	proposed := ProposedSkill{
		Skill: Skill{
			Name: "draft-thing",
			Path: ".thrum/agents/researcher_x/proposed-skills/draft-thing/SKILL.md",
			Frontmatter: Frontmatter{
				Name:        "draft-thing",
				Description: "in-progress draft",
			},
			Body: []byte("# draft-thing\n\nbody text\n"),
		},
		Author:           "@researcher_x",
		ProposedAt:       time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
		FrontmatterValid: false,
	}

	// JSON should not panic; ThrumProvenance must marshal as zero values.
	jsonBytes, err := json.Marshal(proposed)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var decoded ProposedSkill
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if decoded.Author != "@researcher_x" {
		t.Fatalf("author drift: %q", decoded.Author)
	}
	if decoded.Frontmatter.Thrum.PromotedBy != "" {
		t.Fatalf("PromotedBy should be zero-value, got %q", decoded.Frontmatter.Thrum.PromotedBy)
	}
	if !decoded.Frontmatter.Thrum.CreatedAt.IsZero() {
		t.Fatalf("CreatedAt should be zero, got %v", decoded.Frontmatter.Thrum.CreatedAt)
	}
	if decoded.FrontmatterValid {
		t.Fatalf("FrontmatterValid should round-trip false")
	}

	// YAML round-trip must equally tolerate the missing thrum block.
	yamlBytes, err := yaml.Marshal(proposed.Frontmatter)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}
	var decodedYAML Frontmatter
	if err := yaml.Unmarshal(yamlBytes, &decodedYAML); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if decodedYAML.Thrum.PromotedBy != "" {
		t.Fatalf("YAML PromotedBy non-zero after round-trip: %q", decodedYAML.Thrum.PromotedBy)
	}
}

func TestRevisionEntry_RoundTrip(t *testing.T) {
	t.Parallel()

	entry := RevisionEntry{
		MsgThreadID: "msg_01KSXTHREAD",
		ProposedBy:  "@researcher_x",
		At:          time.Date(2026, 5, 15, 14, 30, 0, 0, time.UTC),
	}

	jsonBytes, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	want := `{"msg_thread_id":"msg_01KSXTHREAD","proposed_by":"@researcher_x","at":"2026-05-15T14:30:00Z"}`
	if string(jsonBytes) != want {
		t.Fatalf("JSON shape drift:\n  got:  %s\n  want: %s", jsonBytes, want)
	}

	var decoded RevisionEntry
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if decoded != entry {
		t.Fatalf("decoded mismatch: got %+v want %+v", decoded, entry)
	}

	// YAML form uses the same snake_case keys per design-spec §9.2.
	yamlBytes, err := yaml.Marshal(entry)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}
	var decodedYAML RevisionEntry
	if err := yaml.Unmarshal(yamlBytes, &decodedYAML); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if decodedYAML != entry {
		t.Fatalf("YAML round-trip mismatch: got %+v want %+v", decodedYAML, entry)
	}
}

func TestThrumProvenance_AbsentReview(t *testing.T) {
	t.Parallel()

	// At propose time the review block is absent; provenance must marshal
	// cleanly with a zero-value ReviewBlock and unmarshal back without panic.
	prov := ThrumProvenance{
		ProposedBy:    "@researcher_x",
		TriggerReason: "drafting",
	}

	jsonBytes, err := json.Marshal(prov)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var decoded ThrumProvenance
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if decoded.ProposedBy != "@researcher_x" {
		t.Fatalf("ProposedBy drift: %q", decoded.ProposedBy)
	}
	if decoded.Review.ReviewedBy != "" {
		t.Fatalf("ReviewedBy should be zero-value, got %q", decoded.Review.ReviewedBy)
	}
	if !decoded.Review.ReviewedAt.IsZero() {
		t.Fatalf("ReviewedAt should be zero, got %v", decoded.Review.ReviewedAt)
	}
	if len(decoded.Review.Revisions) != 0 {
		t.Fatalf("Revisions should be empty, got %d entries", len(decoded.Review.Revisions))
	}
}

func TestMirrorEventKind_Stringer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind MirrorEventKind
		want string
	}{
		{MirrorEventKindCreate, "create"},
		{MirrorEventKindUpdate, "update"},
		{MirrorEventKindDelete, "delete"},
		{MirrorEventKindReconcile, "reconcile"},
	}
	for _, c := range cases {
		if got := string(c.kind); got != c.want {
			t.Errorf("MirrorEventKind(%v) stringified to %q, want %q", c.kind, got, c.want)
		}
	}

	// JSON round-trip pins the string representation as the wire form.
	ev := MirrorEvent{
		Kind:      MirrorEventKindCreate,
		SkillName: "demo",
		Trigger:   TriggerFileChange,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"kind":"create","skill_name":"demo","trigger":"file_change"}`; string(data) != want {
		t.Fatalf("MirrorEvent JSON shape drift:\n  got:  %s\n  want: %s", data, want)
	}
	var decoded MirrorEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded != ev {
		t.Fatalf("MirrorEvent round-trip mismatch: got %+v want %+v", decoded, ev)
	}
}

func TestMirrorTrigger_Stringer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		trig MirrorTrigger
		want string
	}{
		{TriggerFileChange, "file_change"},
		{TriggerWorktreeCreate, "worktree_create"},
		{TriggerRestartReconcile, "restart_reconcile"},
		{TriggerManualSync, "manual_sync"},
	}
	for _, c := range cases {
		if got := string(c.trig); got != c.want {
			t.Errorf("MirrorTrigger(%v) stringified to %q, want %q", c.trig, got, c.want)
		}
	}
}
