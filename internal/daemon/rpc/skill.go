package rpc

import (
	"bytes"
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

// SkillMirrorEnqueuer is the subset of internal/skills/mirror.Worker
// consumed by HandleDelete to fan out a Kind=delete event across every
// destination worktree. Defined as a local interface (rather than
// taking *mirror.Worker directly) so tests can inject a recording
// fake without spinning up the full worker lifecycle.
type SkillMirrorEnqueuer interface {
	EnqueueAll(event skills.MirrorEvent) (int, error)
}

// SkillMirrorReconciler is the subset of internal/skills/mirror.Worker
// consumed by HandleSync. Reconcile triggers a full canonical-vs-
// destination pass across every worktree; ReconcileNames runs the
// same pass scoped to the supplied skill names.
type SkillMirrorReconciler interface {
	Reconcile(ctx context.Context) error
	ReconcileNames(ctx context.Context, names []string) error
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
	enqueuer   SkillMirrorEnqueuer   // defaults to worker via ensurePromoteDefaults
	reconciler SkillMirrorReconciler // defaults to worker via ensurePromoteDefaults
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

	// defaultsOnce guards ensurePromoteDefaults against concurrent
	// callers racing on the lazy-init reads + writes of the optional
	// collaborator fields. Every handler entry point calls
	// ensurePromoteDefaults; without the once, concurrent RPCs would
	// race on `if h.clock == nil { h.clock = time.Now }` (read +
	// write of a non-atomic pointer). E10.5–E10.10 Phase 3 fix-batch
	// finding.
	defaultsOnce sync.Once
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
	// ErrSkillNotFoundCode signals skill.delete was called with a name
	// that has no matching .thrum/skills/<name>/SKILL.md. Surfaced via
	// the structured response so the CLI surfaces a clear error rather
	// than wrapping a Library sentinel through the wire.
	ErrSkillNotFoundCode = "skill_not_found"
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
//
// Guarded by sync.Once so concurrent handler entries don't race on the
// nil-check + assign pattern. Tests that assign fields directly
// (h.stamper = ..., h.scanner = ..., etc.) MUST do so BEFORE the first
// handler call — the Once captures whichever set is present at first-
// call time. Test helpers (newPromoteFixture) follow this discipline.
func (h *SkillHandler) ensurePromoteDefaults() {
	h.defaultsOnce.Do(func() {
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
		// enqueuer defaults to the concrete worker when present. When
		// worker is also nil, leave enqueuer nil — HandleDelete checks
		// before invoking, treating "no enqueuer" as a no-op mirror
		// cleanup (the post-restart Reconcile pass picks up the drift).
		if h.enqueuer == nil && h.worker != nil {
			h.enqueuer = h.worker
		}
		if h.reconciler == nil && h.worker != nil {
			h.reconciler = h.worker
		}
	})
}

// --- HandleValidate ---

// SkillValidateRequest is the params shape for skill.validate
// (design-spec §7.9). Empty Name validates every skill in the library.
type SkillValidateRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	Name          string `json:"name,omitempty"`
}

// ValidationFinding is one validator finding, mirroring skills.Finding
// with explicit JSON tags for stable wire shape.
type ValidationFinding struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ValidationResult is one per-skill validation outcome. Status is
// "ok" / "invalid" / "duplicate_provenance" per spec §15. The
// CLI's exit-code classifier (ClassifyValidationResults) consumes
// this slice; the JSON wire shape is consumed by the CLI's
// --format=json path.
type ValidationResult struct {
	Name     string              `json:"name"`
	Status   string              `json:"status"`
	Findings []ValidationFinding `json:"findings,omitempty"`
}

// SkillValidateResponse is the JSON shape for skill.validate. Results
// is one entry per validated skill. Error populates only for system-
// level failures (Library read errors); per-skill validation findings
// surface via the status + findings fields on each entry.
type SkillValidateResponse struct {
	Results []ValidationResult `json:"results"`
	Error   string             `json:"error,omitempty"`
}

// ClassifyValidationResults returns the CLI exit code for a validate
// result slice. 0 when every result is "ok" (or no results); 1 when
// any result is non-"ok". Pure function so the cobra exit-code path
// is testable without subprocess spawning.
func ClassifyValidationResults(results []ValidationResult) int {
	for _, r := range results {
		if r.Status != "ok" {
			return 1
		}
	}
	return 0
}

// HandleValidate serves skill.validate (design-spec §7.9 + §15).
// Any-agent auth per §7.10. For each skill (or just the named one):
//
//  1. Read the raw SKILL.md bytes
//  2. Run ValidateRawFrontmatter on the raw bytes — catches duplicate
//     top-level keys (the post-merge defense per §15)
//  3. Run ValidatePromoted on the parsed frontmatter — catches every
//     missing_required / regex_violation / name_mismatch finding
//  4. Combine: duplicate findings → status="duplicate_provenance";
//     other findings → status="invalid"; clean → status="ok"
//
// Missing .thrum/skills/ is NOT an error: a fresh repo returns an
// empty results slice (consistent with Library.ListPending's
// fresh-repo handling).
func (h *SkillHandler) HandleValidate(ctx context.Context, params json.RawMessage) (any, error) {
	var req SkillValidateRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid request: %w", err)
		}
	}
	if req.CallerAgentID == "" {
		return nil, errors.New("unauthorized: caller_agent_id is required")
	}

	h.ensurePromoteDefaults()

	if h.validator == nil {
		return SkillValidateResponse{Error: "validator_not_wired"}, nil
	}

	var toValidate []skills.Skill
	if req.Name != "" {
		s, err := h.library.Get(ctx, req.Name)
		if err != nil {
			if errors.Is(err, skills.ErrSkillNotFound) {
				return SkillValidateResponse{Error: ErrSkillNotFoundCode}, nil
			}
			return nil, fmt.Errorf("lookup skill: %w", err)
		}
		toValidate = []skills.Skill{*s}
	} else {
		list, err := h.library.List(ctx)
		if err != nil {
			// Empty library (fresh repo) is not an error: callers
			// expect to wire `thrum skill validate` into pre-commit
			// hooks before any skill exists.
			if errors.Is(err, skills.ErrLibraryNotInitialized) {
				return SkillValidateResponse{Results: nil}, nil
			}
			return nil, fmt.Errorf("list skills: %w", err)
		}
		toValidate = list
	}

	results := make([]ValidationResult, 0, len(toValidate))
	for _, s := range toValidate {
		results = append(results, h.validateOne(s))
	}
	return SkillValidateResponse{Results: results}, nil
}

// validateOne runs both the raw-frontmatter merge-conflict check AND
// the parsed-form ValidatePromoted check on a single skill, then
// combines findings into a ValidationResult with the appropriate
// status discriminator.
func (h *SkillHandler) validateOne(s skills.Skill) ValidationResult {
	out := ValidationResult{Name: s.Name, Status: "ok"}

	// Re-read the raw bytes — Library.List already populated s.Frontmatter +
	// s.Body but discarded the raw frontmatter region; we need the raw
	// bytes for the ValidateRawFrontmatter duplicate-key walker.
	raw, readErr := os.ReadFile(s.Path) //nolint:gosec // s.Path comes from Library.List (containment guaranteed)
	if readErr != nil {
		out.Status = "invalid"
		out.Findings = append(out.Findings, ValidationFinding{
			Kind:   "read_error",
			Detail: readErr.Error(),
		})
		return out
	}
	// Strip body — ValidateRawFrontmatter wants only the YAML region.
	// Library uses splitFrontmatter to do this; for the validator we
	// pass the entire file's leading "---" block. The walker is
	// tolerant of body presence: it looks at the first DocumentNode's
	// MappingNode only.
	fmBytes := extractFrontmatterRegion(raw)

	dupFindings := h.validator.ValidateRawFrontmatter(fmBytes)
	for _, f := range dupFindings {
		out.Findings = append(out.Findings, ValidationFinding{Kind: f.Kind, Path: f.Path, Detail: f.Detail})
	}

	parsedFindings := h.validator.ValidatePromoted(&s)
	for _, f := range parsedFindings {
		out.Findings = append(out.Findings, ValidationFinding{Kind: f.Kind, Path: f.Path, Detail: f.Detail})
	}

	// Status discriminator: a duplicate_field finding outranks
	// missing_required + regex_violation in surface treatment because
	// it usually indicates a merge-conflict aftermath and the operator
	// fix is different (resolve the merge, not edit a field).
	switch {
	case findingsContain(out.Findings, "duplicate_field"):
		out.Status = "duplicate_provenance"
	case len(out.Findings) > 0:
		out.Status = "invalid"
	default:
		out.Status = "ok"
	}
	return out
}

// findingsContain reports whether any finding's Kind matches the
// supplied kind. Tiny helper — keeps the status switch above readable.
func findingsContain(findings []ValidationFinding, kind string) bool {
	for _, f := range findings {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

// extractFrontmatterRegion returns the YAML bytes between the leading
// "---\n" and the next "\n---" line. Returns the full input when
// either delimiter is absent (ValidateRawFrontmatter is tolerant of
// non-YAML input — it surfaces an ErrFrontmatterInvalid finding
// rather than panicking).
func extractFrontmatterRegion(raw []byte) []byte {
	if !bytes.HasPrefix(raw, []byte("---")) {
		return raw
	}
	rest := raw[3:]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	}
	if front, _, ok := bytes.Cut(rest, []byte("\n---")); ok {
		return front
	}
	return rest
}

// --- HandleSync ---

// SkillSyncRequest is the params shape for skill.sync (design-spec §7.8).
// Names is optional — when empty/nil, a full reconcile runs; when set,
// only the listed skill names are scoped.
type SkillSyncRequest struct {
	CallerAgentID string   `json:"caller_agent_id"`
	Names         []string `json:"names,omitempty"`
}

// SkillSyncResponse returns a count + any errors encountered. The count
// is len(names) for a scoped pass and 0 for a full pass — Worker
// doesn't surface a per-skill applied-count, and we don't synthesize
// one. Errors carries the stringified Reconcile errors so the CLI can
// surface them to the operator.
type SkillSyncResponse struct {
	ReconciledCount int      `json:"reconciled_count"`
	Errors          []string `json:"errors,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// HandleSync serves skill.sync (design-spec §7.8). Any-agent auth per
// §7.10. Synchronous from the caller's perspective — returns only after
// Reconcile / ReconcileNames completes against every destination.
//
// When reconciler is nil (no worker wired yet at the lifecycle layer),
// returns Error="reconciler_not_wired" so the operator gets a clear
// signal rather than a silent no-op.
func (h *SkillHandler) HandleSync(ctx context.Context, params json.RawMessage) (any, error) {
	var req SkillSyncRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid request: %w", err)
		}
	}
	if req.CallerAgentID == "" {
		return nil, errors.New("unauthorized: caller_agent_id is required")
	}

	h.ensurePromoteDefaults()

	if h.reconciler == nil {
		return SkillSyncResponse{Error: "reconciler_not_wired"}, nil
	}

	var reconcileErr error
	if len(req.Names) == 0 {
		reconcileErr = h.reconciler.Reconcile(ctx)
	} else {
		reconcileErr = h.reconciler.ReconcileNames(ctx, req.Names)
	}

	resp := SkillSyncResponse{
		ReconciledCount: len(req.Names),
	}
	if reconcileErr != nil {
		resp.Errors = []string{reconcileErr.Error()}
	}
	h.logger.Info("skill sync",
		"caller", req.CallerAgentID,
		"names_count", len(req.Names),
		"errors", len(resp.Errors),
	)
	return resp, nil
}

// --- HandleDelete ---

// SkillDeleteRequest is the params shape for skill.delete (design-spec §7.6).
type SkillDeleteRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	Name          string `json:"name"`
	Force         bool   `json:"force,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// SkillDeleteResponse is the JSON shape for skill.delete. DeletedAt is
// populated on success. MirrorsCleared is the count of destination
// worktrees that received the Delete event (0 when no enqueuer is
// wired — production wiring lands at the lifecycle integration point).
// Error populates on a logical-failure path (skill_not_found) so the
// CLI can distinguish bad input from a system error.
type SkillDeleteResponse struct {
	DeletedAt      time.Time `json:"deleted_at,omitzero"`
	MirrorsCleared int       `json:"mirrors_cleared"`
	Error          string    `json:"error,omitempty"`
}

// HandleDelete serves skill.delete (design-spec §7.6 / §13.4).
// Coordinator-only. Removes .thrum/skills/<name>/ recursively, then
// fans a Kind=delete mirror event to every destination worktree
// (eager cleanup per §12.4). force is RPC-side a no-op — it gates the
// CLI confirmation prompt only.
func (h *SkillHandler) HandleDelete(ctx context.Context, params json.RawMessage) (any, error) {
	var req SkillDeleteRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if err := h.requireCoordinator(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}
	if req.Name == "" {
		return nil, errors.New("invalid request: name is required")
	}

	h.ensurePromoteDefaults()

	// Serialize against concurrent promotes for the same name so a
	// promote can't race with a delete on the same target. The
	// existence check below MUST happen after Lock — otherwise a
	// concurrent HandlePromote could land a new version between the
	// Get and the Lock, and the RemoveAll would wipe a coordinator-
	// approved fresh promote (TOCTOU). E10.5–E10.10 Phase 3 fix-batch
	// finding.
	mu := h.promoteMutex(req.Name)
	mu.Lock()
	defer mu.Unlock()

	// Verify the skill exists. Library.Get returns ErrSkillNotFound
	// for a missing name — map to the structured response so the CLI
	// gets a clear error code rather than a wrapped library sentinel.
	if _, err := h.library.Get(ctx, req.Name); err != nil {
		if errors.Is(err, skills.ErrSkillNotFound) {
			return SkillDeleteResponse{Error: ErrSkillNotFoundCode}, nil
		}
		return nil, fmt.Errorf("lookup skill: %w", err)
	}

	// Note on staleness reminder cancellation: HandleDelete intentionally
	// does NOT call CancelProposalReminder. The reminder is keyed by
	// proposal path (under .thrum/agents/<author>/proposed-skills/<name>/),
	// not by canonical skill name, and HandlePromote already cancels the
	// reminder when the skill was originally promoted. Any in-flight
	// proposal for the same name has its reminder cancelled via the
	// watcher's dir-remove path (spec §13.4) when the submitter cleans
	// up their proposed-skills dir. Plan AC E10.9 Step 7's "delete-cancel"
	// wording is satisfied by this division of responsibility.

	skillDir := filepath.Join(h.library.RepoRoot(), ".thrum", "skills", req.Name)
	if err := os.RemoveAll(skillDir); err != nil {
		return nil, fmt.Errorf("remove canonical: %w", err)
	}

	// Fan Delete event to every destination worktree via the enqueuer.
	// Best-effort — a failing enqueue logs + continues; the post-
	// restart Reconcile pass repairs drift.
	var mirrorsCleared int
	if h.enqueuer != nil {
		count, enqErr := h.enqueuer.EnqueueAll(skills.MirrorEvent{
			Kind:      skills.MirrorEventKindDelete,
			SkillName: req.Name,
			Trigger:   skills.TriggerManualSync,
		})
		mirrorsCleared = count
		if enqErr != nil {
			h.logger.Warn("skill delete: mirror enqueue partial failure", "name", req.Name, "err", enqErr)
		}
	}

	h.logger.Info("skill deleted",
		"name", req.Name,
		"caller", req.CallerAgentID,
		"reason", req.Reason,
		"force", req.Force,
		"mirrors_cleared", mirrorsCleared,
	)

	return SkillDeleteResponse{
		DeletedAt:      h.clock(),
		MirrorsCleared: mirrorsCleared,
	}, nil
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
