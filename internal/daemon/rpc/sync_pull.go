package rpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/leonletto/thrum/internal/daemon/eventlog"
)

// MaxSyncBatchSize is the maximum number of events returned in a single sync.pull response.
const MaxSyncBatchSize = 1000

// SyncPullRequest represents the params for a sync.pull RPC call.
type SyncPullRequest struct {
	Token         string `json:"token"`
	AfterSequence int64  `json:"after_sequence"`
	MaxBatch      int    `json:"max_batch"`
}

// SyncPullResponse represents the result of a sync.pull RPC call.
type SyncPullResponse struct {
	Events        []eventlog.Event `json:"events"`
	NextSequence  int64            `json:"next_sequence"`
	MoreAvailable bool             `json:"more_available"`
}

// EventQuerier is the interface for querying events by sequence.
type EventQuerier interface {
	GetEventsSince(afterSeq int64, limit int) ([]eventlog.Event, int64, bool, error)
}

// SyncPullHandler handles the sync.pull RPC method.
type SyncPullHandler struct {
	querier EventQuerier
}

// NewSyncPullHandler creates a new sync.pull handler.
func NewSyncPullHandler(querier EventQuerier) *SyncPullHandler {
	return &SyncPullHandler{querier: querier}
}

// Handle handles a sync.pull request.
func (h *SyncPullHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	var req SyncPullRequest
	if params != nil {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
	}

	if req.AfterSequence < 0 {
		return nil, fmt.Errorf("after_sequence must be >= 0")
	}

	// Cap max_batch
	limit := req.MaxBatch
	if limit <= 0 || limit > MaxSyncBatchSize {
		limit = MaxSyncBatchSize
	}

	events, nextSeq, more, err := h.querier.GetEventsSince(req.AfterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}

	if events == nil {
		events = []eventlog.Event{}
	}

	return SyncPullResponse{
		Events:        events,
		NextSequence:  nextSeq,
		MoreAvailable: more,
	}, nil
}
