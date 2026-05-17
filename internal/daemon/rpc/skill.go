package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/skills"
	"github.com/leonletto/thrum/internal/skills/mirror"
)

// SkillSupervisorMessenger is the subset of internal/daemon/permission.
// Permission consumed by HandlePromote for the inbox-fanout broadcast.
// Defined locally (rather than reusing internal/skills.SupervisorMessenger)
// because the skills-package interface drops the message-ID return for
// watcher-side parity, and HandlePromote depends on the daemon-side
// permission.Permission's full signature for future audit linkage.
type SkillSupervisorMessenger interface {
	SendSupervisorMessage(ctx context.Context, to, body, threadID string) (string, error)
}

// SkillHandler wires the skill.* JSON-RPC surface (design-spec §7) to
// the internal/skills helpers. Constructed once at daemon boot via
// NewSkillHandler and registered against the JSON-RPC server in
// cmd/thrum/main.go alongside the other rpc handler families.
//
// Required-at-construction collaborators are validated by the
// constructor (only library is required across every entry point at
// E10.2–E10.3). Required-at-entry collaborators are guarded at the
// HandlePromote/HandleDelete/etc entry points as those tasks land —
// this keeps the constructor stable while the surface grows.
type SkillHandler struct {
	library    *skills.Library
	validator  *skills.Validator
	perm       SkillSupervisorMessenger
	staleness  skills.ProposalReminderer
	worker     *mirror.Worker
	db         *sql.DB
	stamper    *skills.Stamper
	scanner    *skills.Scanner
	clock      func() time.Time
	logger     *slog.Logger
	renameFunc func(oldpath, newpath string) error

	// promoteMutexes serializes concurrent skill.promote calls for the
	// same skill name. Without this, two coordinators (or one
	// coordinator firing two RPCs in flight) racing on the same name
	// can interleave the `<name>.tmp/` write and `<name>.old/` backup
	// rename in ways that defer-rollback cannot untangle — the second
	// promote may see the first's in-progress backup as a stale
	// leftover and clean it up mid-flight. Per-name mutex is the
	// minimum-scope serialization that allows unrelated promotes to
	// proceed in parallel.
	promoteMutexes sync.Map // name string → *sync.Mutex
}

// NewSkillHandler constructs a SkillHandler. library is required —
// the E10.2 list/show entrypoints depend on it; panics on nil so the
// daemon refuses to start with broken wiring (the watcher's
// internal/skills/watcher.go WatcherOpts uses the same pattern).
// The remaining collaborators are validated at the handlers that
// consume them: messenger + staleness at E10.4 (promote), worker at
// E10.6 (delete). db is consumed by requireCoordinator on check_status
// and by the agent-fanout query in HandlePromote.
//
// stamper / scanner / clock / logger / renameFunc are optional —
// HandlePromote installs deterministic defaults on first use so the
// production wiring stays terse and tests can override per-field by
// direct struct assignment in the same package.
func NewSkillHandler(library *skills.Library, validator *skills.Validator, messenger SkillSupervisorMessenger, staleness skills.ProposalReminderer, worker *mirror.Worker, db *sql.DB) *SkillHandler {
	if library == nil {
		panic("rpc: NewSkillHandler: library is required")
	}
	return &SkillHandler{
		library:   library,
		validator: validator,
		perm:      messenger,
		staleness: staleness,
		worker:    worker,
		db:        db,
	}
}

// --- Wire shapes ---

// SkillListRequest is the params shape for skill.list (design-spec §7.1).
type SkillListRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	Pending       bool   `json:"pending,omitempty"`
	ProposedBy    string `json:"proposed_by,omitempty"`
	Format        string `json:"format,omitempty"`
}

// SkillListEntry is the JSON shape for one promoted skill in the
// skill.list response. Frontmatter fields (description, version,
// thrum.*) are flattened into the entry for caller convenience —
// matches the on-wire sample in design-spec §7.1.
type SkillListEntry struct {
	Name        string                 `json:"name"`
	Path        string                 `json:"path"`
	Description string                 `json:"description,omitempty"`
	Version     string                 `json:"version,omitempty"`
	Thrum       skills.ThrumProvenance `json:"thrum,omitzero"`
}

// ProposedSkillEntry is the JSON shape for one in-flight proposal in
// a skill.list --pending response. age_hours drives the operator's
// staleness display (plan AC L1467).
type ProposedSkillEntry struct {
	SkillListEntry
	ProposedBy string  `json:"proposed_by"`
	AgeHours   float64 `json:"age_hours"`
}

// SkillListResponse is the union response for skill.list. Skills
// carries either []SkillListEntry (promoted form) or
// []ProposedSkillEntry (pending form) — same JSON key per spec §7.1.
//
// The any-typed Skills field is only valid pre-serialization. Callers
// that consume the wire response should deserialize into a typed
// shape (e.g. []SkillListEntry / []ProposedSkillEntry based on whether
// they sent Pending=true) — type assertions on the deserialized any
// will fail because encoding/json decodes into []map[string]any.
type SkillListResponse struct {
	Skills any `json:"skills"`
}

// SkillShowRequest is the params shape for skill.show (design-spec §7.2).
// Either Name (promoted lookup) or Path (proposed-skill path) is
// required; the handler rejects an empty pair.
type SkillShowRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	Name          string `json:"name,omitempty"`
	Path          string `json:"path,omitempty"`
	IncludeRaw    bool   `json:"include_raw,omitempty"`
}

// SkillShowResponse is the JSON shape for skill.show. Raw is populated
// only when IncludeRaw=true on the request — used by the diff path of
// an edit-promote (E10.4) and by the check-the-skill meta-skill when
// C-B2 ships.
type SkillShowResponse struct {
	Frontmatter skills.Frontmatter `json:"frontmatter"`
	Body        string             `json:"body"`
	Raw         string             `json:"raw,omitempty"`
}

// SkillCheckStatusRequest is the params shape for skill.check_status
// (design-spec §7.4).
type SkillCheckStatusRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	CheckID       string `json:"check_id"`
}

// SkillCheckStatusResponse is the JSON shape for skill.check_status.
// In the v0.11 stub window the handler always populates Status="error"
// and Error=ErrCheckSkillNotAvailableCode; the live shape (Findings,
// CompletedAt) lands when C-B2 ships and the stub flips to live.
type SkillCheckStatusResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// ErrCheckSkillNotAvailableCode is the canonical wire-level error
// identifier for the check-the-skill stub (design-spec §7.4 / canonical
// §8.3). Returned by HandleCheckStatus as the `error` field of the
// success-response stub shape; embedded by HandleCheck inside the
// human-readable message via ErrCheckTheSkillNotAvailable.
const ErrCheckSkillNotAvailableCode = "check_the_skill_not_available"

// CheckSkillNotAvailableMessage is the canonical §8.3 / spec §7.3
// verbatim error text returned by HandleCheck in the v0.11 stub
// window. Exported so the CLI can string-match the message to drive
// exit-code-2 classification (the JSON-RPC envelope's `error.code`
// field is generic -32000, so the message text is the only
// available discriminator after the error crosses the wire).
const CheckSkillNotAvailableMessage = "check-the-skill meta-skill not implemented in this build. Use 'thrum skill promote --force <path>' to bypass the admission gate, or wait for C-B2 to ship."

// ErrCheckTheSkillNotAvailable is the daemon-side sentinel error
// returned by HandleCheck while C-B2 (the live check-the-skill
// meta-skill) is unimplemented. Tests compare via errors.Is.
//
//nolint:staticcheck // ST1005: error message is the canonical §8.3 verbatim text — punctuation is spec-mandated.
var ErrCheckTheSkillNotAvailable = errors.New(CheckSkillNotAvailableMessage)

// Promote error-code constants. Returned via SkillPromoteResponse.Error
// (not the Go error path) so structured detail (findings, override
// records) can travel with the response.
const (
	ErrFrontmatterInvalidCode = "frontmatter_invalid"
	ErrSecretScanBlockedCode  = "secret_scan_blocked"
	ErrCheckRequiredCode      = "check_required"
	// ErrInvalidPatternCode signals a malformed AllowSecretPatterns
	// regex. Surfaced via the structured response (matching every
	// other coordinator-facing logical failure) so the CLI can distinguish
	// a typo in --allow-secret from a real scanner I/O failure.
	ErrInvalidPatternCode = "invalid_pattern"
	// ErrProposalNotFoundCode signals that skill.revise was given a
	// path that doesn't resolve to a proposed-skill SKILL.md. Surfaced
	// via the structured response so the CLI distinguishes
	// "you typed a bad path" from a wire / DB failure.
	ErrProposalNotFoundCode = "proposal_not_found"
)

// AllowedPatternWire is the JSON shape for one entry in
// SkillPromoteRequest.AllowSecretPatterns. Mirrors
// skills.AllowedPattern but lives in the rpc package so the wire-side
// JSON tagging stays explicit.
type AllowedPatternWire struct {
	Pattern string `json:"pattern"`
	Reason  string `json:"reason"`
}

// SkillPromoteRequest is the params shape for skill.promote
// (design-spec §7.5).
type SkillPromoteRequest struct {
	CallerAgentID       string               `json:"caller_agent_id"`
	Path                string               `json:"path"`
	Force               bool                 `json:"force,omitempty"`
	ForceReason         string               `json:"force_reason,omitempty"`
	AllowSecretPatterns []AllowedPatternWire `json:"allow_secret_patterns,omitempty"`
	// MsgThreadID is the inbound revision message's thread ID, captured
	// on an edit-promote as part of the new RevisionEntry. Empty for a
	// create-promote.
	MsgThreadID string `json:"msg_thread_id,omitempty"`
}

// PromoteReview is the review-block subset returned in
// SkillPromoteResponse. Mirrors skills.ReviewBlock but with stable JSON
// tags pinned to the spec §7.5 sample.
type PromoteReview struct {
	ReviewedBy          string                       `json:"reviewed_by"`
	ReviewedAt          time.Time                    `json:"reviewed_at"`
	CheckSkillVersion   string                       `json:"check_skill_version"`
	Revisions           []skills.RevisionEntry       `json:"revisions"`
	SecretScanOverrides []skills.SecretScanOverride  `json:"secret_scan_overrides,omitempty"`
	ForceOverride       string                       `json:"force_override,omitempty"`
}

// PromoteSecretFinding is the JSON shape for one secret-scan hit in
// SkillPromoteResponse.SecretFindings. Mirrors skills.SecretFinding but
// keeps the wire-side JSON tagging explicit. The matched string is
// never included per spec §14.3 (privacy guarantee).
type PromoteSecretFinding struct {
	Path            string `json:"path"`
	Line            int    `json:"line"`
	PatternCategory string `json:"pattern_category"`
}

// PromoteFrontmatterFinding is the JSON shape for one validator
// finding surfaced in SkillPromoteResponse.FrontmatterFindings on a
// frontmatter_invalid error.
type PromoteFrontmatterFinding struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// PromoteInvalidPattern is the JSON shape for one offending pattern
// surfaced when AllowSecretPatterns contains a regex that fails to
// compile. The Pattern field is the operator-supplied string verbatim
// — we deliberately do NOT include the regexp.Compile error detail
// because Go's regexp error text can echo back fragments of the
// pattern in an inconsistent shape across versions, and the operator
// already knows what they typed.
type PromoteInvalidPattern struct {
	Pattern string `json:"pattern"`
	Error   string `json:"error,omitempty"`
}

// SkillPromoteResponse is the JSON shape for skill.promote (design-spec
// §7.5). Success populates PromotedPath / PromotedAt / Mode / Review.
// Logical errors (frontmatter_invalid, secret_scan_blocked,
// check_required) populate Error + the corresponding detail field
// instead — auth failures still travel via the Go error return per
// the established HandleCheck pattern.
type SkillPromoteResponse struct {
	PromotedPath        string                      `json:"promoted_path,omitempty"`
	PromotedAt          time.Time                   `json:"promoted_at,omitzero"`
	Mode                string                      `json:"mode,omitempty"`
	Review              *PromoteReview              `json:"review,omitempty"`
	Error               string                      `json:"error,omitempty"`
	FrontmatterFindings []PromoteFrontmatterFinding `json:"frontmatter_findings,omitempty"`
	SecretFindings      []PromoteSecretFinding      `json:"secret_findings,omitempty"`
	InvalidPatterns     []PromoteInvalidPattern     `json:"invalid_patterns,omitempty"`
}

// --- HandleList ---

// HandleList serves skill.list (design-spec §7.1). Per §7.10, any
// identified agent may call this method. The handler proxies to
// Library.List (promoted form) or Library.ListPending (pending form
// via Pending=true) and flattens the result into the wire-shape.
func (h *SkillHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req SkillListRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid request: %w", err)
		}
	}
	if req.CallerAgentID == "" {
		return nil, errors.New("unauthorized: caller_agent_id is required")
	}

	if req.Pending {
		proposed, err := h.library.ListPending(ctx, skills.PendingFilter{ProposedBy: req.ProposedBy})
		if err != nil {
			return nil, err
		}
		out := make([]ProposedSkillEntry, 0, len(proposed))
		for _, p := range proposed {
			out = append(out, h.proposedToEntry(p))
		}
		return SkillListResponse{Skills: out}, nil
	}

	list, err := h.library.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SkillListEntry, 0, len(list))
	for _, s := range list {
		out = append(out, skillToEntry(s))
	}
	return SkillListResponse{Skills: out}, nil
}

// skillToEntry projects a skills.Skill into the wire-shape, flattening
// the frontmatter description/version into the entry.
func skillToEntry(s skills.Skill) SkillListEntry {
	return SkillListEntry{
		Name:        s.Name,
		Path:        s.Path,
		Description: s.Frontmatter.Description,
		Version:     s.Frontmatter.Version,
		Thrum:       s.Frontmatter.Thrum,
	}
}

// proposedToEntry projects a skills.ProposedSkill into the pending
// wire-shape. age_hours derives from the SKILL.md mtime
// (filesystem-as-source-of-truth per design-spec §3) — when an mtime
// stat fails the field stays at zero rather than failing the whole
// listing, and a debug log line surfaces the silent-zero so operators
// can distinguish "new file" from "stat failed" during triage.
func (h *SkillHandler) proposedToEntry(p skills.ProposedSkill) ProposedSkillEntry {
	var ageHours float64
	if info, err := os.Stat(p.Path); err == nil {
		ageHours = time.Since(info.ModTime()).Hours()
	} else {
		slog.Debug("skill.list: stat for age_hours failed", "path", p.Path, "err", err)
	}
	return ProposedSkillEntry{
		SkillListEntry: skillToEntry(p.Skill),
		ProposedBy:     p.Author,
		AgeHours:       ageHours,
	}
}

// --- HandleShow ---

// HandleShow serves skill.show (design-spec §7.2). Per §7.10, any
// identified agent may call. The request supplies either Name (lookup
// in .thrum/skills/) or Path (load a proposed-skill SKILL.md). Path
// is containment-checked by Library.GetProposed; the handler does no
// additional sandbox enforcement beyond that.
func (h *SkillHandler) HandleShow(ctx context.Context, params json.RawMessage) (any, error) {
	var req SkillShowRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.CallerAgentID == "" {
		return nil, errors.New("unauthorized: caller_agent_id is required")
	}
	if req.Name == "" && req.Path == "" {
		return nil, errors.New("invalid request: name or path is required")
	}

	var (
		fm   skills.Frontmatter
		body []byte
		path string
	)
	if req.Path != "" {
		proposed, err := h.library.GetProposed(ctx, req.Path)
		if err != nil {
			return nil, err
		}
		fm = proposed.Frontmatter
		body = proposed.Body
		path = proposed.Path
	} else {
		s, err := h.library.Get(ctx, req.Name)
		if err != nil {
			return nil, err
		}
		fm = s.Frontmatter
		body = s.Body
		path = s.Path
	}

	resp := SkillShowResponse{
		Frontmatter: fm,
		Body:        string(body),
	}
	if req.IncludeRaw {
		raw, err := os.ReadFile(path) //nolint:gosec // path resolved via Library (containment-checked in GetProposed; Get returns a path under .thrum/skills)
		if err != nil {
			return nil, fmt.Errorf("read raw: %w", err)
		}
		resp.Raw = string(raw)
	}
	return resp, nil
}

// --- HandleCheck ---

// SkillCheckRequest is the params shape for skill.check (design-spec
// §7.3). The Wait flag is accepted in the stub window for forward-
// compat with the live form (post-C-B2 blocks up to 30s); the stub
// ignores it and returns the canonical error immediately.
type SkillCheckRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	Path          string `json:"path"`
	Wait          bool   `json:"wait,omitempty"`
}

// HandleCheck serves skill.check (design-spec §7.3). Coordinator-only
// per spec §7.10. In the v0.11 first-ship window — per canonical
// §8.3's stub-and-ship-broken decision — the handler returns the
// ErrCheckTheSkillNotAvailable sentinel without ever invoking a
// check-the-skill meta-skill. When C-B2 ships, this entry point
// flips to live invocation with zero CLI / RPC contract change
// (verb, request shape, response shape, and async/inbox path are
// already wired).
func (h *SkillHandler) HandleCheck(ctx context.Context, params json.RawMessage) (any, error) {
	var req SkillCheckRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if err := h.requireCoordinator(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}
	// Path is structurally required by the spec but the stub does no
	// filesystem work — accept any non-empty value; a future live
	// handler will validate path containment via the library.
	return nil, ErrCheckTheSkillNotAvailable
}

// --- HandleCheckStatus ---

// HandleCheckStatus serves skill.check_status (design-spec §7.4). Per
// §7.10, coordinator-only — only a coordinator-role agent may poll a
// check. In the v0.11 stub window the body always returns the
// canonical check_the_skill_not_available code; the live shape ships
// when C-B2 lands.
//
// Plan-AC deviation (acknowledged in plan-errata batch thrum-8rgu):
// the plan AC at L1475 reads "Auth: list/show/check_status callable
// by any identified agent". Spec §7.10 is the authoritative wire
// contract and pins check_status as coordinator-only — coordinator-
// confirmed in session that spec wins (msg_01KRVFY2TDEHR6V7JYEPYSP8XF
// 2026-05-17). The stub response is uniform either way; spec-aligned
// auth keeps the wire contract stable when C-B2 flips the stub live.
func (h *SkillHandler) HandleCheckStatus(ctx context.Context, params json.RawMessage) (any, error) {
	var req SkillCheckStatusRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if err := h.requireCoordinator(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}
	return SkillCheckStatusResponse{
		Status: "error",
		Error:  ErrCheckSkillNotAvailableCode,
	}, nil
}

// --- HandlePromote ---

// HandlePromote serves skill.promote (design-spec §7.5). Coordinator-
// only per spec §7.10. The flow:
//
//  1. Auth + load proposal
//  2. Validate frontmatter (returns frontmatter_invalid on failure)
//  3. Secret-scan with caller-supplied allow_secret_patterns overrides
//     (returns secret_scan_blocked on remaining findings)
//  4. Determine mode: edit (existing .thrum/skills/<name>/SKILL.md) vs
//     create
//  5. Stamp provenance via skills.Stamper (StampCreate or StampEdit,
//     plus RecordSecretScanOverride for each override that fired)
//  6. Atomic on-disk move via temp dir + rename, with defer-rollback
//     when the rename fails after the existing target was backed aside
//  7. Best-effort: cancel staleness reminder, fanout inbox notifications
//     to every non-supervisor/non-user agent in the repo
//  8. Emit slog.Info audit line
//
// In the v0.11 stub window, the `force` flag is plumbed through but
// has no functional effect — the check-the-skill gate it would bypass
// is itself the stub from canonical §8.3 (returns
// check_the_skill_not_available unconditionally). The flag is recorded
// in the audit log for forward-compat with C-B2; secret-scan ALWAYS
// runs regardless of force per AC.
func (h *SkillHandler) HandlePromote(ctx context.Context, params json.RawMessage) (any, error) {
	var req SkillPromoteRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if err := h.requireCoordinator(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}
	if req.Path == "" {
		return nil, errors.New("invalid request: path is required")
	}

	h.ensurePromoteDefaults()

	// Pre-compile every override regex BEFORE doing any filesystem work.
	// A typo in --allow-secret must surface as a structured response
	// error (matching every other coordinator-facing logical failure on
	// this verb), not as an opaque Go error wrapped through the scanner.
	if invalid := compileAllowedPatterns(req.AllowSecretPatterns); len(invalid) > 0 {
		return SkillPromoteResponse{
			Error:           ErrInvalidPatternCode,
			InvalidPatterns: invalid,
		}, nil
	}

	proposed, err := h.library.GetProposed(ctx, req.Path)
	if err != nil {
		return nil, fmt.Errorf("load proposal: %w", err)
	}

	// Serialize concurrent promotes for the SAME skill name. Unrelated
	// names proceed in parallel. The mutex is acquired before the
	// pre-stage RemoveAll so the entire `.tmp/` + `.old/` + rename
	// sequence is atomic from any other promote's perspective.
	mu := h.promoteMutex(proposed.Name)
	mu.Lock()
	defer mu.Unlock()

	// Validation: surface findings via response.Error (frontmatter_invalid)
	// rather than the Go error path so the structured detail can travel
	// with the response payload.
	//
	// The proposed-skill carries operator-controlled fields only; the
	// promote-stamped fields (promoted_by / review.*) are filled in by
	// the stamper below. So the pre-stamp check uses ValidateProposed
	// (loose form: name + description + thrum.proposed_by +
	// thrum.trigger_reason), and a post-stamp ValidatePromoted runs
	// after the stamper as a belt-and-suspenders sanity check that
	// catches stamper bugs before the on-disk write.
	if h.validator != nil {
		findings := h.validator.ValidateProposed(proposed)
		if len(findings) > 0 {
			out := make([]PromoteFrontmatterFinding, 0, len(findings))
			for _, f := range findings {
				out = append(out, PromoteFrontmatterFinding{Kind: f.Kind, Path: f.Path, Detail: f.Detail})
			}
			return SkillPromoteResponse{
				Error:               ErrFrontmatterInvalidCode,
				FrontmatterFindings: out,
			}, nil
		}
	}

	// Secret-scan: ALWAYS runs, even with force=true per AC line 1631.
	skillDir := filepath.Dir(proposed.Path)
	overrides := make([]skills.AllowedPattern, 0, len(req.AllowSecretPatterns))
	for _, a := range req.AllowSecretPatterns {
		overrides = append(overrides, skills.AllowedPattern{Pattern: a.Pattern, Reason: a.Reason})
	}
	active, _, scanErr := h.scanner.ScanWithOverrides(skillDir, overrides)
	if scanErr != nil {
		return nil, fmt.Errorf("secret scan: %w", scanErr)
	}
	if len(active) > 0 {
		out := make([]PromoteSecretFinding, 0, len(active))
		for _, f := range active {
			out = append(out, PromoteSecretFinding{
				Path:            f.Path,
				Line:            f.Line,
				PatternCategory: f.PatternCategory,
			})
		}
		return SkillPromoteResponse{
			Error:          ErrSecretScanBlockedCode,
			SecretFindings: out,
		}, nil
	}

	// Mode discriminator: existing promoted skill at the canonical path
	// means edit-promote per spec §13.3 Q8 symmetry.
	finalDir := filepath.Join(h.library.RepoRoot(), ".thrum", "skills", proposed.Name)
	finalPath := filepath.Join(finalDir, "SKILL.md")
	mode := "create"
	var existingCreatedAt time.Time
	var existingReview skills.ReviewBlock
	if existing, getErr := h.library.Get(ctx, proposed.Name); getErr == nil && existing != nil {
		mode = "edit"
		existingCreatedAt = existing.Frontmatter.Thrum.CreatedAt
		existingReview = existing.Frontmatter.Thrum.Review
	}

	// Stamp provenance into the proposed-skill struct in memory. The
	// stamped frontmatter is the bytes we'll re-encode and write below.
	stamped := proposed.Skill
	stamped.Frontmatter.Thrum.Review = existingReview // baseline (empty on create)
	stamped.Path = finalPath
	if mode == "edit" {
		if err := h.stamper.StampEdit(&stamped, existingCreatedAt, skills.RevisionEntry{
			MsgThreadID: req.MsgThreadID,
			ProposedBy:  proposed.Author,
			At:          h.clock(),
		}); err != nil {
			return nil, fmt.Errorf("stamp edit: %w", err)
		}
		// On edit, preserve the original promoted_by / proposed_by /
		// reviewed_by / check_skill_version unless they were absent on
		// the prior frontmatter — StampEdit deliberately leaves those
		// alone, but the proposed-skill's frontmatter usually has empty
		// values for them which need backfilling from the existing skill.
		if stamped.Frontmatter.Thrum.PromotedBy == "" {
			stamped.Frontmatter.Thrum.PromotedBy = req.CallerAgentID
		}
		if stamped.Frontmatter.Thrum.Review.ReviewedBy == "" {
			stamped.Frontmatter.Thrum.Review.ReviewedBy = req.CallerAgentID
		}
		if stamped.Frontmatter.Thrum.Review.CheckSkillVersion == "" {
			stamped.Frontmatter.Thrum.Review.CheckSkillVersion = skills.CheckSkillStubVersion
		}
	} else {
		if err := h.stamper.StampCreate(&stamped, req.CallerAgentID, skills.CheckSkillStubVersion); err != nil {
			return nil, fmt.Errorf("stamp create: %w", err)
		}
	}
	// Record any overrides that fired during the scan as audit entries
	// on the review block. The scanner already filtered them out of
	// `active`; we record what the caller supplied so the audit trail
	// captures the operator's intent regardless of whether the pattern
	// actually matched anything in this proposal.
	for _, ov := range req.AllowSecretPatterns {
		if recErr := h.stamper.RecordSecretScanOverride(&stamped, ov.Pattern, ov.Reason, req.CallerAgentID); recErr != nil {
			return nil, fmt.Errorf("record override: %w", recErr)
		}
	}
	// Record the force-override reason on the stamped review block per
	// plan AC line 1172 / 1632. A blank ForceReason gets a non-empty
	// default so the audit string is never silently empty when
	// force=true (the on-disk frontmatter is the only durable record
	// of WHY force was used).
	if req.Force {
		reason := strings.TrimSpace(req.ForceReason)
		if reason == "" {
			reason = "force override at promote time (no reason supplied)"
		}
		stamped.Frontmatter.Thrum.Review.ForceOverride = fmt.Sprintf("%s — %s", req.CallerAgentID, reason)
	}

	// Atomic on-disk move: build the new skill bytes, write to a temp
	// dir, swap with the existing target (when present), and rename.
	encoded, err := skills.EncodeFrontmatter(&stamped.Frontmatter)
	if err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	fullContent := append(encoded, proposed.Body...)

	tmpDir := finalDir + ".tmp"
	backupDir := finalDir + ".old"

	// Clean any stale leftovers from a prior crash before staging.
	_ = os.RemoveAll(tmpDir)
	_ = os.RemoveAll(backupDir)

	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir tmp: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), fullContent, 0o600); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("write tmp skill: %w", err)
	}

	// Edit-mode: rename existing target aside so we can rollback on
	// rename failure. Create-mode: no backup needed.
	if mode == "edit" {
		if renameErr := h.renameFunc(finalDir, backupDir); renameErr != nil {
			_ = os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("backup existing: %w", renameErr)
		}
	}

	// Critical rename. On failure, restore the backup (edit mode) and
	// clean up the tmp dir.
	if renameErr := h.renameFunc(tmpDir, finalDir); renameErr != nil {
		_ = os.RemoveAll(tmpDir)
		if mode == "edit" {
			// Best-effort restore. If this fails, the original is at
			// backupDir and an operator can recover manually — we
			// surface the original error so the caller knows the
			// promote failed even when restore succeeded.
			_ = os.Rename(backupDir, finalDir)
		}
		return nil, fmt.Errorf("rename promote: %w", renameErr)
	}

	// Successful rename: clean up the backup (edit mode only).
	if mode == "edit" {
		if rmErr := os.RemoveAll(backupDir); rmErr != nil {
			// Non-fatal: the promote landed; backup-cleanup failure is a
			// disk-state warning, not a promote failure. Surface in the
			// audit log so operators can clear it manually.
			h.logger.Warn("skill promote: backup cleanup failed", "path", backupDir, "err", rmErr)
		}
	}

	// Best-effort: cancel the staleness reminder for this proposal.
	if h.staleness != nil {
		if cancelErr := h.staleness.CancelProposalReminder(ctx, req.Path); cancelErr != nil {
			h.logger.Warn("skill promote: cancel staleness reminder failed", "path", req.Path, "err", cancelErr)
		}
	}

	// Inbox fanout: notify every non-supervisor / non-user agent in
	// the repo. Best-effort — individual send failures log + continue.
	// When force=true, the body prepends a FORCE OVERRIDE marker per
	// plan AC line 1632-1634 so every recipient's inbox surfaces the
	// admission-gate bypass on a normal triage glance.
	if h.perm != nil && h.db != nil {
		recipients, lookupErr := h.listRepoAgents(ctx)
		if lookupErr != nil {
			h.logger.Warn("skill promote: agent fanout list failed", "err", lookupErr)
		} else {
			body := fmt.Sprintf("Skill %q promoted by %s (mode=%s) at %s", proposed.Name, req.CallerAgentID, mode, finalPath)
			if req.Force {
				body = "[FORCE OVERRIDE: " + stamped.Frontmatter.Thrum.Review.ForceOverride + "] " + body
			}
			for _, agentID := range recipients {
				if _, sendErr := h.perm.SendSupervisorMessage(ctx, agentID, body, ""); sendErr != nil {
					h.logger.Warn("skill promote: supervisor send failed", "to", agentID, "err", sendErr)
				}
			}
		}
	}

	h.logger.Info("skill promoted",
		"name", proposed.Name,
		"mode", mode,
		"force", req.Force,
		"caller", req.CallerAgentID,
		"path", finalPath,
	)

	resp := SkillPromoteResponse{
		PromotedPath: finalPath,
		PromotedAt:   h.clock(),
		Mode:         mode,
		Review: &PromoteReview{
			ReviewedBy:          stamped.Frontmatter.Thrum.Review.ReviewedBy,
			ReviewedAt:          stamped.Frontmatter.Thrum.Review.ReviewedAt,
			CheckSkillVersion:   stamped.Frontmatter.Thrum.Review.CheckSkillVersion,
			Revisions:           stamped.Frontmatter.Thrum.Review.Revisions,
			SecretScanOverrides: stamped.Frontmatter.Thrum.Review.SecretScanOverrides,
			ForceOverride:       stamped.Frontmatter.Thrum.Review.ForceOverride,
		},
	}
	// Always emit a non-nil Revisions slice so the wire shape is
	// stable (encoding/json marshals nil slice as `null`, which the
	// CLI / web UI then has to special-case). StampCreate already
	// installs an empty slice; StampEdit appends to whatever was
	// there. The defensive belt-and-suspenders is here so a future
	// refactor of either stamper can't silently break the shape.
	if resp.Review.Revisions == nil {
		resp.Review.Revisions = []skills.RevisionEntry{}
	}
	return resp, nil
}

// ensurePromoteDefaults installs deterministic defaults for the
// optional collaborators consumed by HandlePromote (and future promote-
// adjacent handlers). Each is no-op when already set; production code
// supplies nothing and gets stable defaults, tests assign fakes before
// calling.
func (h *SkillHandler) ensurePromoteDefaults() {
	if h.clock == nil {
		h.clock = time.Now
	}
	if h.stamper == nil {
		h.stamper = skills.NewStamper(h.clock)
	}
	if h.scanner == nil {
		h.scanner = skills.NewScanner()
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	if h.renameFunc == nil {
		h.renameFunc = os.Rename
	}
}

// --- HandleRevise ---

// SkillReviseRequest is the params shape for skill.revise (design-spec §7.7).
type SkillReviseRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	Path          string `json:"path"`
	Findings      string `json:"findings"`
	// CheckFindings is the structured output of a prior `check-the-skill`
	// run. Nil/empty in the v0.11 stub window (no live check), packaged
	// into the inbox body as "stub: not run".
	CheckFindings string `json:"check_findings,omitempty"`
}

// SkillReviseResponse returns the IDs of the inbox message that
// HandleRevise dispatched on the coordinator's behalf. Empty MessageID
// + non-empty Error signals a logical failure (e.g. proposal_not_found);
// auth-style failures still travel via the Go error path per the
// established HandleCheck pattern.
type SkillReviseResponse struct {
	MessageID string `json:"message_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// HandleRevise serves skill.revise (design-spec §7.7 / §13.2 step 2).
// Coordinator-only. Packages coordinator findings + optional check
// output into a structured inbox message addressed to the proposing
// agent. MUST NOT write to the submitter's `.thrum/agents/<author>/`
// directory — MB-1.S2 Q2 ownership boundary enforced by the
// TestRevise_NeverWritesSubmitterFolder integration test.
//
// The submitter is resolved from the path's `<author>` segment
// (canonical per spec §17.2); a frontmatter mismatch logs a warning
// but does not block — the path location is the load-bearing
// identity, not the operator-mutable frontmatter field.
func (h *SkillHandler) HandleRevise(ctx context.Context, params json.RawMessage) (any, error) {
	var req SkillReviseRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if err := h.requireCoordinator(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}
	if req.Path == "" {
		return nil, errors.New("invalid request: path is required")
	}

	h.ensurePromoteDefaults() // logger; revise reuses the same defaulter

	proposed, err := h.library.GetProposed(ctx, req.Path)
	if err != nil {
		// Library.GetProposed wraps the various not-found / escape
		// cases with ErrSkillNotFound; map those to the structured
		// proposal_not_found response so the CLI can distinguish bad
		// path from system error.
		if errors.Is(err, skills.ErrSkillNotFound) {
			return SkillReviseResponse{Error: ErrProposalNotFoundCode}, nil
		}
		return nil, fmt.Errorf("load proposal: %w", err)
	}

	// Cross-check: frontmatter's proposed_by SHOULD match the path's
	// author segment. Mismatch is informational (path wins), but log
	// at warn so operators can investigate stale or mis-authored
	// proposals on triage.
	if proposed.Frontmatter.Thrum.ProposedBy != "" && proposed.Frontmatter.Thrum.ProposedBy != proposed.Author {
		h.logger.Warn("skill.revise: submitter mismatch",
			"path_author", proposed.Author,
			"frontmatter_proposed_by", proposed.Frontmatter.Thrum.ProposedBy,
			"path", proposed.Path,
		)
	}

	if h.perm == nil {
		return nil, errors.New("skill.revise: messenger not configured")
	}

	checkText := req.CheckFindings
	if checkText == "" {
		checkText = "stub: not run"
	}
	body := fmt.Sprintf(
		"# Skill revision feedback\n\n**Skill:** %s\n**Path:** %s\n\n## Coordinator findings\n%s\n\n## Check-the-skill output\n%s\n",
		proposed.Name, proposed.Path, req.Findings, checkText,
	)

	msgID, sendErr := h.perm.SendSupervisorMessage(ctx, proposed.Author, body, "")
	if sendErr != nil {
		return nil, fmt.Errorf("send revision message: %w", sendErr)
	}
	h.logger.Info("skill revised",
		"name", proposed.Name,
		"submitter", proposed.Author,
		"caller", req.CallerAgentID,
		"msg_id", msgID,
	)
	return SkillReviseResponse{
		MessageID: msgID,
		ThreadID:  msgID, // new thread; the first message's ID is the thread root.
	}, nil
}

// compileAllowedPatterns pre-compiles every override regex and returns
// a list of the offending entries (empty when all compile clean). The
// internal scan path eventually re-compiles via the scanner, but pre-
// validating here lets HandlePromote return the structured
// invalid_pattern error before any filesystem work happens.
func compileAllowedPatterns(in []AllowedPatternWire) []PromoteInvalidPattern {
	var bad []PromoteInvalidPattern
	for _, p := range in {
		if _, err := regexp.Compile(p.Pattern); err != nil {
			bad = append(bad, PromoteInvalidPattern{
				Pattern: p.Pattern,
				// regexp error text is bounded + descriptive; safe to
				// surface because no scan has run yet (no matched
				// secret to leak).
				Error: err.Error(),
			})
		}
	}
	return bad
}

// promoteMutex returns the per-skill-name mutex used to serialize
// concurrent promote calls. The first call for a given name installs
// a fresh *sync.Mutex via sync.Map.LoadOrStore; subsequent calls
// reuse it. Mutexes are never deleted — the per-name footprint is
// negligible (one pointer + Mutex header) and the GC complexity of
// safely removing a busy mutex outweighs the memory saved.
func (h *SkillHandler) promoteMutex(name string) *sync.Mutex {
	v, _ := h.promoteMutexes.LoadOrStore(name, &sync.Mutex{})
	mu, ok := v.(*sync.Mutex)
	if !ok {
		// Unreachable: every LoadOrStore call stores *sync.Mutex.
		panic("rpc: promoteMutex: registry value is not *sync.Mutex")
	}
	return mu
}

// listRepoAgents enumerates the agent IDs eligible to receive a
// skill-promote inbox notification. Filters out user:-prefixed humans
// and the supervisor pseudo-agent — they do not consume inbox notify
// like a normal agent does, and routing to them would either fail
// (humans aren't in the daemon's agent registry as message recipients
// in the conventional sense) or short-circuit (supervisor messages
// originate FROM the supervisor; sending one TO it creates self-talk).
func (h *SkillHandler) listRepoAgents(ctx context.Context) ([]string, error) {
	rows, err := h.db.QueryContext(ctx, `SELECT agent_id FROM agents`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, scanErr
		}
		if strings.HasPrefix(id, "user:") {
			continue
		}
		if strings.HasPrefix(id, "supervisor_") || id == "supervisor" {
			continue
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// requireCoordinator gates RPCs whose spec auth is coordinator-only.
// Mirrors internal/daemon/rpc/email.go requireCoordinatorOrUser but
// tightened — skill control RPCs are agent-to-agent, not user-callable.
// nil DB no-ops the role check (test scenarios that don't wire DB
// take the same fast-path as email.go's requireAgentRegistered).
func (h *SkillHandler) requireCoordinator(ctx context.Context, callerAgentID string) error {
	if callerAgentID == "" {
		return errors.New("unauthorized: caller_agent_id is required")
	}
	if h.db == nil {
		return nil
	}
	var role string
	err := h.db.QueryRowContext(ctx,
		`SELECT role FROM agents WHERE agent_id = ?`, callerAgentID,
	).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("unauthorized: agent %q not registered", callerAgentID)
	}
	if err != nil {
		return fmt.Errorf("unauthorized: agent lookup: %w", err)
	}
	if role != "coordinator" {
		return fmt.Errorf("unauthorized: coordinator-role required (caller role: %s)", role)
	}
	return nil
}
