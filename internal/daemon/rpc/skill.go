package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/leonletto/thrum/internal/daemon/permission"
	"github.com/leonletto/thrum/internal/skills"
	"github.com/leonletto/thrum/internal/skills/mirror"
)

// SkillHandler wires the skill.* JSON-RPC surface (design-spec §7) to
// the internal/skills helpers. Constructed once at daemon boot via
// NewSkillHandler and registered against the JSON-RPC server in
// cmd/thrum/main.go alongside the other rpc handler families.
//
// Fields beyond library are stored as-supplied at E10.2 — the methods
// landing in later C-B1 tasks (E10.4 promote, E10.6 delete) add their
// own nil-checks at the entry points that consume them, so the
// defensive panic on missing wiring still fires at first-use rather
// than silently NPE'ing inside a goroutine.
//
// Plan-errata vs E10.2 AC: the constructor adds a *sql.DB param the
// plan AC omitted. The DB is required for the coordinator-role check
// on check_status (spec §7.10) — there's no other path to look up an
// agent's role from inside the rpc package. Mirrors the same pattern
// in internal/daemon/rpc/email.go (NewEmailHandler also takes a
// *sql.DB for its requireCoordinatorOrUser auth helper).
type SkillHandler struct {
	library   *skills.Library
	validator *skills.Validator
	perm      *permission.Permission
	staleness skills.ProposalReminderer
	worker    *mirror.Worker
	db        *sql.DB
}

// NewSkillHandler constructs a SkillHandler. library is required —
// the E10.2 list/show entrypoints depend on it; panics on nil so the
// daemon refuses to start with broken wiring (the watcher's
// internal/skills/watcher.go WatcherOpts uses the same pattern).
// The remaining collaborators are validated at the handlers that
// consume them: perm + staleness at E10.4 (promote), worker at E10.6
// (delete). db is consumed by requireCoordinator on check_status.
func NewSkillHandler(library *skills.Library, validator *skills.Validator, perm *permission.Permission, staleness skills.ProposalReminderer, worker *mirror.Worker, db *sql.DB) *SkillHandler {
	if library == nil {
		panic("rpc: NewSkillHandler: library is required")
	}
	return &SkillHandler{
		library:   library,
		validator: validator,
		perm:      perm,
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
// §8.3). Exported so E10.3's HandleCheck can reuse the identifier when
// it also returns the same code.
const ErrCheckSkillNotAvailableCode = "check_the_skill_not_available"

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
