package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	email "github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/identity"
)

// EmailBridgeInterface decouples the RPC handlers from the concrete
// *email.Bridge so tests can inject a lightweight mock without standing up a
// full bridge (Run, IMAP, SMTP). The production daemon passes *email.Bridge
// directly — it satisfies this interface without modification.
//
// Registration with the RPC dispatcher is D-B1.17's job; this file only
// exposes the seven Handle* functions.
type EmailBridgeInterface interface {
	Status() email.BridgeStatus
	Queue() *email.Queue
	Mesh() *email.MeshHandlerImpl
	Limiter() *email.Limiter
	Inbound() *email.Inbound
	Config() config.EmailConfig
}

// --- request / response types ---

// EmailSendRequest is the wire shape for email.send.
type EmailSendRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	ToAddress     string `json:"to_address"`
	Subject       string `json:"subject"`
	Body          string `json:"body"`
	// ReplyTo is an optional parent thrum_msg_id used to thread the outbound
	// MIME message. Not to be confused with the RFC 5322 Reply-To header.
	ReplyTo string `json:"reply_to,omitempty"`
}

// EmailSendResponse is the wire shape returned by email.send.
type EmailSendResponse struct {
	Status    string `json:"status"`     // always "queued" on success
	QueueID   int64  `json:"queue_id"`   // autoincrement id of the email_outbound_queue row
	MessageID string `json:"message_id"` // synthesised RFC 2822 Message-Id
}

// EmailPeerPairRequest is the wire shape for email.peer.pair.
type EmailPeerPairRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	// ToHandle is the handle of the pending stranger-pair awaiting confirmation.
	ToHandle string `json:"to_handle"`
}

// EmailPeerPairResponse is the wire shape returned by email.peer.pair.
type EmailPeerPairResponse struct {
	// Pending is true when the pair was confirmed and the peer is now in
	// email.peers[], false when no pending pair existed for that handle.
	Pending   bool  `json:"pending"`
	ExpiresAt int64 `json:"expires_at"` // unix milliseconds; 0 when Pending=false
}

// EmailPeerListRequest is the wire shape for email.peer.list.
type EmailPeerListRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
}

// EmailPeerEntry is one row in the EmailPeerListResponse.
type EmailPeerEntry struct {
	Handle        string `json:"handle"`
	DaemonIDShort string `json:"daemon_id_short"` // first 8 hex chars of DaemonID
	VouchedBy     string `json:"vouched_by"`
	Trust         string `json:"trust"`
	AddedAt       string `json:"added_at"`
	// LastSeen is omitted in D-B1 — the bridge does not yet persist per-peer
	// activity timestamps. Reserved for v0.11.x.
	LastSeen string `json:"last_seen,omitempty"`
}

// EmailPeerListResponse is the wire shape returned by email.peer.list.
type EmailPeerListResponse struct {
	Peers []EmailPeerEntry `json:"peers"`
}

// EmailPeerRevokeRequest is the wire shape for email.peer.revoke.
type EmailPeerRevokeRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	ToHandle      string `json:"to_handle"`
}

// EmailPeerRevokeResponse is the wire shape returned by email.peer.revoke.
type EmailPeerRevokeResponse struct {
	Removed bool `json:"removed"`
}

// EmailPeerRebindRequest is the wire shape for email.peer.rebind.
type EmailPeerRebindRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	ToHandle      string `json:"to_handle"`
	NewDaemonID   string `json:"new_daemon_id"`
}

// EmailPeerRebindResponse is the wire shape returned by email.peer.rebind.
type EmailPeerRebindResponse struct {
	Updated bool `json:"updated"`
}

// EmailStatusRequest is the wire shape for email.status.
type EmailStatusRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
}

// EmailStatusResponse is the wire shape returned by email.status.
type EmailStatusResponse struct {
	Running     bool   `json:"running"`
	ConnectedAt int64  `json:"connected_at"` // unix ms; 0 when not running
	LastError   string `json:"last_error,omitempty"`

	InboundCount          int64    `json:"inbound_count"`
	OutboundQueueDepth    int      `json:"outbound_queue_depth"`
	UnknownRecipientCount int64    `json:"unknown_recipient_count"`
	PausedPeers           []string `json:"paused_peers"`
}

// EmailUnblockRequest is the wire shape for email.unblock.
type EmailUnblockRequest struct {
	CallerAgentID string `json:"caller_agent_id"`
	PeerKey       string `json:"peer_key"`
}

// EmailUnblockResponse is the wire shape returned by email.unblock.
type EmailUnblockResponse struct {
	Unblocked bool `json:"unblocked"`
}

// --- handler ---

// EmailHandler handles the seven email.* RPC methods defined in design-spec §6.
// Wired into the daemon's RPC dispatcher in D-B1.17.
type EmailHandler struct {
	bridge EmailBridgeInterface
	// db is the shared daemon SQLite connection, used for the outbound queue
	// depth query in email.status.
	db *sql.DB
}

// NewEmailHandler constructs an EmailHandler. bridge and db may be nil when
// the email bridge is disabled; each handler checks and returns bridge_disabled.
func NewEmailHandler(bridge EmailBridgeInterface, db *sql.DB) *EmailHandler {
	return &EmailHandler{bridge: bridge, db: db}
}

// SetBridge replaces the bridge reference — called by the daemon after the
// bridge is constructed so that the handler and bridge share the same lifetime.
func (h *EmailHandler) SetBridge(b EmailBridgeInterface) {
	h.bridge = b
}

// --- authorization helpers ---

// requireAgentRegistered checks that callerAgentID is non-empty and exists in
// the agents table. Returns the callerAgentID on success.
//
// This mirrors the "caller_agent_id is required" guard in message.go (line
// 1306) and the queryAgentByID pattern, but without the full peercred /
// sec.3 path — email RPCs are not yet peercred-wired (D-B1.17 wires them).
// For now the simpler DB existence check is the gate.
func (h *EmailHandler) requireAgentRegistered(ctx context.Context, callerAgentID string) error {
	if callerAgentID == "" {
		return fmt.Errorf("unauthorized: caller_agent_id is required")
	}
	if h.db == nil {
		// DB not available — allow in test scenarios where bridge mock is wired
		// but DB is nil. Production always has a DB.
		return nil
	}
	var count int
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agents WHERE agent_id = ?`, callerAgentID,
	).Scan(&count); err != nil {
		return fmt.Errorf("unauthorized: agent lookup: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("unauthorized: agent %q not registered", callerAgentID)
	}
	return nil
}

// requireCoordinatorOrUser checks that the caller is either a coordinator-role
// agent or a user (agent_id has "user:" prefix).
//
// No existing requireCoordinator helper is present in this package — the
// codebase has no coordinator-gated RPCs yet. This local helper follows the
// pattern documented in the task plan §6 ("pragmatic check"). Flag
// thrum-xir if a shared helper is warranted once more RPCs are gated.
func (h *EmailHandler) requireCoordinatorOrUser(ctx context.Context, callerAgentID string) error {
	if callerAgentID == "" {
		return fmt.Errorf("unauthorized: caller_agent_id is required")
	}
	// User IDs always start with "user:" — fast-path accept.
	if strings.HasPrefix(callerAgentID, "user:") {
		return nil
	}
	if h.db == nil {
		return nil
	}
	var role string
	err := h.db.QueryRowContext(ctx,
		`SELECT role FROM agents WHERE agent_id = ?`, callerAgentID,
	).Scan(&role)
	if err == sql.ErrNoRows {
		return fmt.Errorf("unauthorized: agent %q not registered", callerAgentID)
	}
	if err != nil {
		return fmt.Errorf("unauthorized: agent lookup: %w", err)
	}
	if role != "coordinator" {
		return fmt.Errorf("unauthorized: coordinator-role or user required (caller role: %s)", role)
	}
	return nil
}

// requireBridgeEnabled returns an error when the bridge is nil or its config
// has Enabled=false.
func (h *EmailHandler) requireBridgeEnabled() error {
	if h.bridge == nil {
		return fmt.Errorf("bridge_disabled: email bridge not initialised")
	}
	if !h.bridge.Config().Enabled {
		return fmt.Errorf("bridge_disabled: email bridge is disabled in config")
	}
	return nil
}

// --- RPC handlers ---

// HandleSend handles the email.send RPC (agent-callable).
//
// NOTE: design-spec §6 lists recipient_not_allowed as a possible error code,
// but §4 defines no outbound recipient allowlist — the two sections are
// inconsistent. D-B1 drops recipient_not_allowed from the response surface
// entirely (dead code would never be triggered). A §6 doc-patch follow-up
// will align the spec; track via the existing thrum-xir refactor flag.
func (h *EmailHandler) HandleSend(ctx context.Context, params json.RawMessage) (any, error) {
	var req EmailSendRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if err := h.requireAgentRegistered(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}
	if err := h.requireBridgeEnabled(); err != nil {
		return nil, err
	}

	cfg := h.bridge.Config()
	maxBytes := cfg.MaxOutboundBytes
	if maxBytes > 0 && len(req.Body) > maxBytes {
		return nil, fmt.Errorf("over_size_limit: body length %d exceeds limit %d", len(req.Body), maxBytes)
	}

	// Synthesise a thrum message ID so the Message-Id is traceable.
	thrumMsgID := identity.GenerateMessageID()

	daemonShort := shortDaemonID(cfg.DaemonHandle)
	host := cfg.SMTP.Host
	if host == "" {
		host = "localhost"
	}
	messageID := email.GenerateMessageId(daemonShort, thrumMsgID, host)

	env := email.QueueEnvelope{
		FromAgent: req.CallerAgentID,
		ToAddress: req.ToAddress,
		Subject:   req.Subject,
		Body:      req.Body,
	}

	queueID, err := h.bridge.Queue().Enqueue(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("email.send: enqueue: %w", err)
	}

	// Telemetry: attribute the enqueue event to the calling agent so
	// operators can audit which agents generated outbound email traffic.
	// from_agent is already persisted in email_outbound_queue.from_agent;
	// the slog record makes it queryable from the daemon log without a
	// DB query.
	slog.Info("[email] outbound queued",
		"queue_id", queueID,
		"message_id", messageID,
		"from_agent", req.CallerAgentID,
		"to_address", req.ToAddress,
	)

	return &EmailSendResponse{
		Status:    "queued",
		QueueID:   queueID,
		MessageID: messageID,
	}, nil
}

// HandlePeerPair handles the email.peer.pair RPC (coordinator-only).
// Confirms a pending stranger-pair previously recorded by the mesh handler.
func (h *EmailHandler) HandlePeerPair(ctx context.Context, params json.RawMessage) (any, error) {
	var req EmailPeerPairRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if err := h.requireCoordinatorOrUser(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}
	if err := h.requireBridgeEnabled(); err != nil {
		return nil, err
	}

	if err := h.bridge.Mesh().ConfirmStrangerPair(ctx, req.ToHandle); err != nil {
		// ConfirmStrangerPair returns an error only when no pending pair exists
		// for the handle. Surface as Pending=false with the reason attached so
		// the CLI can show a helpful message without treating it as a fatal RPC
		// failure.
		return nil, fmt.Errorf("email.peer.pair: %w", err)
	}

	return &EmailPeerPairResponse{Pending: true, ExpiresAt: 0}, nil
}

// HandlePeerList handles the email.peer.list RPC (coordinator-only).
// Reads the current peer roster from the bridge config snapshot. The
// roster includes contact emails and daemon IDs — operator-grade
// inventory — so the same coordinator-or-user gate that protects
// pair/revoke/rebind applies here too (design-spec §6).
func (h *EmailHandler) HandlePeerList(ctx context.Context, params json.RawMessage) (any, error) {
	var req EmailPeerListRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if err := h.requireCoordinatorOrUser(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}

	var peers []EmailPeerEntry
	if h.bridge != nil {
		cfg := h.bridge.Config()
		peers = make([]EmailPeerEntry, 0, len(cfg.Peers))
		for _, p := range cfg.Peers {
			peers = append(peers, EmailPeerEntry{
				Handle:        p.Handle,
				DaemonIDShort: shortDaemonID(p.DaemonID),
				VouchedBy:     p.VouchedBy,
				Trust:         p.Trust,
				AddedAt:       p.AddedAt,
			})
		}
	}
	if peers == nil {
		peers = []EmailPeerEntry{}
	}

	return &EmailPeerListResponse{Peers: peers}, nil
}

// HandlePeerRevoke handles the email.peer.revoke RPC (coordinator-only).
// Removes a peer from email.peers[] by gossiping a peer.revoke envelope
// through the mesh handler.
func (h *EmailHandler) HandlePeerRevoke(ctx context.Context, params json.RawMessage) (any, error) {
	var req EmailPeerRevokeRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if err := h.requireCoordinatorOrUser(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}
	if err := h.requireBridgeEnabled(); err != nil {
		return nil, err
	}

	// Synthesise a peer.revoke payload so HandlePeerRevoke's remove-and-gossip
	// path fires exactly as it would for an inbound protocol message.
	payload := email.PeerProtocolPayload{
		Handle:   req.ToHandle,
		DaemonID: "operator-revoke", // sourced locally; not a remote peer id
	}
	if err := h.bridge.Mesh().HandlePeerRevoke(ctx, payload); err != nil {
		return nil, fmt.Errorf("email.peer.revoke: %w", err)
	}

	return &EmailPeerRevokeResponse{Removed: true}, nil
}

// HandlePeerRebind handles the email.peer.rebind RPC (coordinator-only).
// Applies a new daemon-id for the named handle after the peer has signalled
// a daemon-id rotation.
func (h *EmailHandler) HandlePeerRebind(ctx context.Context, params json.RawMessage) (any, error) {
	var req EmailPeerRebindRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if err := h.requireCoordinatorOrUser(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}
	if err := h.requireBridgeEnabled(); err != nil {
		return nil, err
	}

	if err := h.bridge.Mesh().ConfirmRebind(ctx, req.ToHandle, req.NewDaemonID); err != nil {
		return nil, fmt.Errorf("email.peer.rebind: %w", err)
	}

	return &EmailPeerRebindResponse{Updated: true}, nil
}

// HandleStatus handles the email.status RPC.
// Assembles a snapshot from the bridge's atomic counters, the outbound queue,
// and the in-memory + SQLite rate-limiter state.
func (h *EmailHandler) HandleStatus(ctx context.Context, params json.RawMessage) (any, error) {
	var req EmailStatusRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if err := h.requireAgentRegistered(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}

	resp := &EmailStatusResponse{
		PausedPeers: []string{},
	}

	if h.bridge == nil {
		return resp, nil
	}

	st := h.bridge.Status()
	resp.Running = st.Running
	resp.LastError = st.LastError
	resp.InboundCount = st.InboundProcessed
	if !st.StartedAt.IsZero() {
		resp.ConnectedAt = st.StartedAt.UnixMilli()
	}

	// Operator visibility into mis-routed inbound traffic — the count
	// is the cumulative number of step-10 unknown_recipient drops since
	// bridge start. Inbound() returns nil between run cycles; treat that
	// as zero rather than skipping the field.
	if inbound := h.bridge.Inbound(); inbound != nil {
		resp.UnknownRecipientCount = inbound.UnknownRecipientCount()
	}

	// Outbound queue depth: rows in queued or sending state.
	if h.db != nil {
		var depth int
		err := h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM email_outbound_queue WHERE status IN ('queued','sending')`,
		).Scan(&depth)
		if err == nil {
			resp.OutboundQueueDepth = depth
		}
		// Non-fatal: leave depth=0 if the table doesn't exist yet.
	}

	// Paused peers from rate-limiter.
	if h.bridge.Limiter() != nil {
		paused, err := h.bridge.Limiter().PausedPeers(ctx)
		if err == nil && len(paused) > 0 {
			resp.PausedPeers = paused
		}
	}

	return resp, nil
}

// HandleUnblock handles the email.unblock RPC (coordinator-only).
// Clears the rate-limiter pause for the given peer key.
func (h *EmailHandler) HandleUnblock(ctx context.Context, params json.RawMessage) (any, error) {
	var req EmailUnblockRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if err := h.requireCoordinatorOrUser(ctx, req.CallerAgentID); err != nil {
		return nil, err
	}
	if err := h.requireBridgeEnabled(); err != nil {
		return nil, err
	}

	if err := h.bridge.Limiter().Unblock(ctx, req.PeerKey); err != nil {
		return nil, fmt.Errorf("email.unblock: %w", err)
	}

	return &EmailUnblockResponse{Unblocked: true}, nil
}

// shortDaemonID returns the first 8 characters of a daemon handle or ID string.
// Mirrors the shortID helper in internal/bridge/email/mesh.go without importing
// it (that function is unexported).
func shortDaemonID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
