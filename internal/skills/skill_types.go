// Package skills owns the per-project skill substrate (canonical §8.3 / §8.5
// / §8.6). It walks the on-disk skill library at .thrum/skills/ and the
// per-agent proposed-skills tree, parses YAML frontmatter (§9.2), mirrors
// promoted skills into per-worktree runtime targets, and minds the
// staleness reminder substrate for un-promoted proposals via A-B4.
//
// This file declares shared types only — no business logic. Downstream
// files in this package (library.go, frontmatter.go, validator.go,
// watcher.go, staleness.go) and the mirror sub-package
// (internal/skills/mirror/) consume them.
//
// Dependency direction: internal/skills/mirror/ MAY import this package.
// internal/skills MUST NOT import internal/skills/mirror/. Adding any
// internal/skills/mirror import to a file in this package is a
// regression — re-run `go build ./internal/skills/` after any change here
// to verify. (Spec §6: MirrorEvent / MirrorEventKind / MirrorTrigger live
// in the parent package so both watcher.go and mirror/worker.go reference
// the same type without an import cycle.)
package skills

import "time"

// Skill is a fully-promoted skill rooted at
// .thrum/skills/<name>/SKILL.md.
type Skill struct {
	Name        string
	Path        string
	Frontmatter Frontmatter
	Body        []byte
}

// ProposedSkill is an un-promoted draft rooted at
// .thrum/agents/<author>/proposed-skills/<name>/SKILL.md.
//
// At propose time the embedded Frontmatter.Thrum block is typically a
// zero-value ThrumProvenance — the daemon stamps it on promote.
type ProposedSkill struct {
	Skill
	Author           string
	ProposedAt       time.Time
	FrontmatterValid bool
}

// Frontmatter mirrors the SKILL.md YAML frontmatter schema in
// design-spec §9.2. Tags carry both JSON and YAML names so a single
// struct round-trips through the on-disk canonical (YAML) and the
// RPC wire (JSON) without an intermediate map.
type Frontmatter struct {
	Name         string          `json:"name" yaml:"name"`
	Description  string          `json:"description" yaml:"description"`
	AllowedTools []string        `json:"allowed-tools,omitempty" yaml:"allowed-tools,omitempty"`
	Version      string          `json:"version,omitempty" yaml:"version,omitempty"`
	Author       string          `json:"author,omitempty" yaml:"author,omitempty"`
	License      string          `json:"license,omitempty" yaml:"license,omitempty"`
	Thrum        ThrumProvenance `json:"thrum" yaml:"thrum"`
}

// ThrumProvenance is the daemon-stamped `thrum:` block under a skill's
// frontmatter. Pre-promote drafts may carry a zero-value provenance;
// promote stamps the canonical fields atomically.
type ThrumProvenance struct {
	ProposedBy    string        `json:"proposed_by,omitempty" yaml:"proposed_by,omitempty"`
	PromotedBy    string        `json:"promoted_by,omitempty" yaml:"promoted_by,omitempty"`
	CreatedAt     time.Time     `json:"created_at,omitzero" yaml:"created_at,omitempty"`
	TriggerReason string        `json:"trigger_reason,omitempty" yaml:"trigger_reason,omitempty"`
	SourcePattern SourcePattern `json:"source_pattern,omitzero" yaml:"source_pattern,omitempty"`
	Review        ReviewBlock   `json:"review,omitzero" yaml:"review,omitempty"`
}

// SourcePattern links a skill to the artifact that motivated it
// (design-spec §9.2). `Type` is one of "bd-issue", "commit-range",
// "message-thread", or "other"; `Ref` is the id / SHA range / message ID.
type SourcePattern struct {
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
	Ref  string `json:"ref,omitempty" yaml:"ref,omitempty"`
}

// ReviewBlock records the coordinator-side review trail for a promoted
// skill: who reviewed it, against which version of the check-the-skill
// meta-skill, plus per-revision msg-thread links and any secret-scan
// pattern overrides recorded at promote time.
type ReviewBlock struct {
	ReviewedBy          string               `json:"reviewed_by,omitempty" yaml:"reviewed_by,omitempty"`
	ReviewedAt          time.Time            `json:"reviewed_at,omitzero" yaml:"reviewed_at,omitempty"`
	CheckSkillVersion   string               `json:"check_skill_version,omitempty" yaml:"check_skill_version,omitempty"`
	Revisions           []RevisionEntry      `json:"revisions,omitempty" yaml:"revisions,omitempty"`
	SecretScanOverrides []SecretScanOverride `json:"secret_scan_overrides,omitempty" yaml:"secret_scan_overrides,omitempty"`
}

// RevisionEntry is one entry in the review.revisions array. The shape is
// {msg_thread_id, proposed_by, at} per design-spec §9.2 (MINOR #13 fix
// at spec lock).
type RevisionEntry struct {
	MsgThreadID string    `json:"msg_thread_id" yaml:"msg_thread_id"`
	ProposedBy  string    `json:"proposed_by" yaml:"proposed_by"`
	At          time.Time `json:"at" yaml:"at"`
}

// SecretScanOverride records a per-promote coordinator decision to allow
// a pattern that the regex pass would otherwise reject. The reason and
// reviewer ID are persisted alongside the matched pattern for audit.
type SecretScanOverride struct {
	Pattern    string    `json:"pattern" yaml:"pattern"`
	Reason     string    `json:"reason" yaml:"reason"`
	ReviewedBy string    `json:"reviewed_by" yaml:"reviewed_by"`
	ReviewedAt time.Time `json:"reviewed_at" yaml:"reviewed_at"`
}

// MirrorEventKind discriminates the lifecycle phase of a mirror event.
// String-typed so the wire form and the Go identifier line up; the
// Stringer test pins the chosen representation.
type MirrorEventKind string

const (
	MirrorEventKindCreate    MirrorEventKind = "create"
	MirrorEventKindUpdate    MirrorEventKind = "update"
	MirrorEventKindDelete    MirrorEventKind = "delete"
	MirrorEventKindReconcile MirrorEventKind = "reconcile"
)

// MirrorTrigger identifies why a mirror event fired (file-change vs.
// worktree-create vs. restart reconcile vs. manual sync). Used by
// internal/skills/mirror/worker.go to decide debounce/coalesce policy.
type MirrorTrigger string

const (
	TriggerFileChange       MirrorTrigger = "file_change"
	TriggerWorktreeCreate   MirrorTrigger = "worktree_create"
	TriggerRestartReconcile MirrorTrigger = "restart_reconcile"
	TriggerManualSync       MirrorTrigger = "manual_sync"
)

// MirrorEvent is the typed message that flows from watcher.go (parent
// package) into the mirror worker channel (internal/skills/mirror/).
// Per spec §6 the type lives in the parent package so both producers
// reference a single declaration.
type MirrorEvent struct {
	Kind      MirrorEventKind `json:"kind"`
	SkillName string          `json:"skill_name"`
	Trigger   MirrorTrigger   `json:"trigger"`
}

// CheckResult is the return shape of the check-the-skill meta-skill
// (canonical §8.3 stub-and-ship-broken in v0.11; live in C-B2). Fields
// match the skill.check_status response shape in design-spec §7.4.
//
// At the v0.11 stub layer, CheckID and Error carry the stub error
// (`check_the_skill_not_available`) and Findings is the zero value.
type CheckResult struct {
	CheckID     string        `json:"check_id"`
	Status      string        `json:"status"`
	Findings    CheckFindings `json:"findings,omitzero"`
	CompletedAt time.Time     `json:"completed_at,omitzero"`
	Error       string        `json:"error,omitempty"`
}

// CheckFindings is the structured payload returned when a check
// completes (live, not stub). Fields match design-spec §7.4.
type CheckFindings struct {
	Uniqueness              string   `json:"uniqueness,omitempty"`
	Fit                     string   `json:"fit,omitempty"`
	Overlaps                []string `json:"overlaps,omitempty"`
	RevisionRecommendations []string `json:"revision_recommendations,omitempty"`
}
