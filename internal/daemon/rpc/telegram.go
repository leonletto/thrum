package rpc

import (
	"context"
	"encoding/json"
	"fmt"

	telegram "github.com/leonletto/thrum/internal/bridge/telegram"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/paths"
)

// TelegramConfigureRequest is the request for telegram.configure.
// All fields are optional — nil means "don't change".
type TelegramConfigureRequest struct {
	Token     *string `json:"token,omitempty"`
	Target    *string `json:"target,omitempty"`
	UserID    *string `json:"user_id,omitempty"`
	ChatID    *int64  `json:"chat_id,omitempty"`
	Enabled   *bool   `json:"enabled,omitempty"`
	AllowFrom []int64 `json:"allow_from,omitempty"`
	AllowAll  *bool   `json:"allow_all,omitempty"`
}

// TelegramConfigureResponse is the response for telegram.configure.
type TelegramConfigureResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// TelegramStatusResponse is the response for telegram.status.
type TelegramStatusResponse struct {
	Configured   bool    `json:"configured"`
	Enabled      bool    `json:"enabled"`
	Running      bool    `json:"running"`
	Token        string  `json:"token,omitempty"`
	Target       string  `json:"target"`
	UserID       string  `json:"user_id"`
	ChatID       int64   `json:"chat_id,omitempty"`
	AllowAll     bool    `json:"allow_all"`
	AllowFrom    []int64 `json:"allow_from,omitempty"`
	ConnectedAt  string  `json:"connected_at,omitempty"`
	InboundCount int64   `json:"inbound_count"`
	Error        string  `json:"error,omitempty"`
}

// TelegramHandler handles telegram.configure and telegram.status RPCs.
type TelegramHandler struct {
	repoPath string
	bridge   *telegram.Bridge // may be nil if bridge not started
}

// NewTelegramHandler creates a new TelegramHandler.
func NewTelegramHandler(repoPath string) *TelegramHandler {
	return &TelegramHandler{repoPath: repoPath}
}

// SetBridge sets the bridge reference for restart/status operations.
func (h *TelegramHandler) SetBridge(b *telegram.Bridge) {
	h.bridge = b
}

// HandleConfigure handles the telegram.configure RPC.
func (h *TelegramHandler) HandleConfigure(_ context.Context, params json.RawMessage) (any, error) {
	var req TelegramConfigureRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	thrumDir, err := paths.ResolveThrumDir(h.repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Merge: only update fields that are provided
	if req.Token != nil {
		cfg.Telegram.Token = *req.Token
	}
	if req.Target != nil {
		cfg.Telegram.Target = *req.Target
	}
	if req.UserID != nil {
		cfg.Telegram.UserID = *req.UserID
	}
	if req.ChatID != nil {
		cfg.Telegram.ChatID = *req.ChatID
	}
	if req.Enabled != nil {
		cfg.Telegram.Enabled = req.Enabled
	}
	if req.AllowFrom != nil {
		cfg.Telegram.AllowFrom = req.AllowFrom
	}
	if req.AllowAll != nil {
		cfg.Telegram.AllowAll = *req.AllowAll
	}

	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}

	status := "saved"
	msg := "Config saved."

	// Restart bridge if running
	if h.bridge != nil {
		h.bridge.Restart(cfg.Telegram)
		status = "saved_and_restarted"
		msg = "Config saved. Bridge restarting."
	}

	return TelegramConfigureResponse{
		Status:  status,
		Message: msg,
	}, nil
}

// HandleStatus handles the telegram.status RPC.
func (h *TelegramHandler) HandleStatus(_ context.Context, _ json.RawMessage) (any, error) {
	thrumDir, err := paths.ResolveThrumDir(h.repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	tg := cfg.Telegram
	resp := TelegramStatusResponse{
		Configured: tg.Token != "",
		Enabled:    tg.TelegramEnabled(),
		Target:     tg.Target,
		UserID:     tg.UserID,
		ChatID:     tg.ChatID,
		AllowAll:   tg.AllowAll,
		AllowFrom:  tg.AllowFrom,
	}

	// Mask token
	if tg.Token != "" {
		resp.Token = tg.MaskedToken() + "..."
	}

	// Add runtime status if bridge is available
	if h.bridge != nil {
		bs := h.bridge.Status()
		resp.Running = bs.Running
		resp.ConnectedAt = bs.ConnectedAt
		resp.InboundCount = bs.InboundCount
		resp.Error = bs.LastError
	}

	return resp, nil
}
