package daemon

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// Default rate limit constants
const (
	DefaultMaxRequestsPerSecond = 10
	DefaultBurstSize            = 20
	DefaultMaxSyncQueueDepth    = 1000
)

// RateLimitConfig holds configuration for rate limiting.
type RateLimitConfig struct {
	MaxRequestsPerSecond float64 `json:"max_requests_per_second" yaml:"max_requests_per_second"`
	BurstSize            int     `json:"burst_size" yaml:"burst_size"`
	MaxSyncQueueDepth    int     `json:"max_sync_queue_depth" yaml:"max_sync_queue_depth"`
	Enabled              bool    `json:"enabled" yaml:"enabled"`
}

// SyncRateLimiter provides per-peer rate limiting for sync requests.
type SyncRateLimiter struct {
	mu         sync.Mutex
	limiters   map[string]*peerLimiter // keyed by peer daemon ID
	config     RateLimitConfig
	queueDepth int32 // atomic counter for current sync queue depth
}

// peerLimiter wraps a rate limiter with last access time for cleanup.
type peerLimiter struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// NewSyncRateLimiter creates a new rate limiter with the given config.
// If config has zero values, defaults are used.
func NewSyncRateLimiter(cfg RateLimitConfig) *SyncRateLimiter {
	// Apply defaults for zero values
	if cfg.MaxRequestsPerSecond == 0 {
		cfg.MaxRequestsPerSecond = DefaultMaxRequestsPerSecond
	}
	if cfg.BurstSize == 0 {
		cfg.BurstSize = DefaultBurstSize
	}
	if cfg.MaxSyncQueueDepth == 0 {
		cfg.MaxSyncQueueDepth = DefaultMaxSyncQueueDepth
	}

	return &SyncRateLimiter{
		limiters:   make(map[string]*peerLimiter),
		config:     cfg,
		queueDepth: 0,
	}
}

// Allow checks if a request from the given peer should be allowed.
// Returns nil if allowed, or an error with details if rate limited.
// Error will indicate whether it's a 429 (rate limit) or 503 (queue depth).
func (r *SyncRateLimiter) Allow(peerID string) error {
	if !r.config.Enabled {
		return nil
	}

	// First check global queue depth
	currentDepth := atomic.LoadInt32(&r.queueDepth)
	if currentDepth >= int32(r.config.MaxSyncQueueDepth) {
		return &RateLimitError{
			Code:    503,
			Message: fmt.Sprintf("sync queue full (%d/%d)", currentDepth, r.config.MaxSyncQueueDepth),
			PeerID:  peerID,
		}
	}

	// Then check per-peer rate limit
	limiter := r.getLimiter(peerID)
	if !limiter.Allow() {
		return &RateLimitError{
			Code:    429,
			Message: "rate limit exceeded",
			PeerID:  peerID,
		}
	}

	return nil
}

// IncrementQueue increments the sync queue depth counter.
func (r *SyncRateLimiter) IncrementQueue() {
	atomic.AddInt32(&r.queueDepth, 1)
}

// DecrementQueue decrements the sync queue depth counter.
func (r *SyncRateLimiter) DecrementQueue() {
	atomic.AddInt32(&r.queueDepth, -1)
}

// GetQueueDepth returns the current sync queue depth.
func (r *SyncRateLimiter) GetQueueDepth() int32 {
	return atomic.LoadInt32(&r.queueDepth)
}

// CleanupStale removes limiters for peers not seen in the given duration.
// Returns the number of limiters removed.
func (r *SyncRateLimiter) CleanupStale(maxAge time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	for id, pl := range r.limiters {
		if pl.lastAccess.Before(cutoff) {
			delete(r.limiters, id)
			removed++
		}
	}

	return removed
}

// getLimiter returns or creates a rate limiter for the given peer.
func (r *SyncRateLimiter) getLimiter(peerID string) *rate.Limiter {
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	if pl, ok := r.limiters[peerID]; ok {
		pl.lastAccess = now
		return pl.limiter
	}

	// Create new limiter for this peer
	limiter := rate.NewLimiter(rate.Limit(r.config.MaxRequestsPerSecond), r.config.BurstSize)
	r.limiters[peerID] = &peerLimiter{
		limiter:    limiter,
		lastAccess: now,
	}

	return limiter
}

// RateLimitError represents a rate limit or overload error.
type RateLimitError struct {
	Code    int    // 429 for rate limit, 503 for overload
	Message string
	PeerID  string
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit error (code %d) for peer %s: %s", e.Code, e.PeerID, e.Message)
}
