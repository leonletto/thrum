package daemon

import (
	"testing"
	"time"
)

func TestRateLimiter_DisabledAlwaysAllows(t *testing.T) {
	cfg := RateLimitConfig{
		MaxRequestsPerSecond: 1,
		BurstSize:            1,
		MaxSyncQueueDepth:    10,
		Enabled:              false,
	}

	limiter := NewSyncRateLimiter(cfg)

	// Make many requests - all should succeed when disabled
	for i := range 100 {
		if err := limiter.Allow("peer1"); err != nil {
			t.Errorf("request %d was denied when limiter is disabled: %v", i, err)
		}
	}
}

func TestRateLimiter_BurstHandling(t *testing.T) {
	cfg := RateLimitConfig{
		MaxRequestsPerSecond: 1,
		BurstSize:            5,
		MaxSyncQueueDepth:    100,
		Enabled:              true,
	}

	limiter := NewSyncRateLimiter(cfg)

	// First 5 requests (burst) should succeed immediately
	for i := range 5 {
		if err := limiter.Allow("peer1"); err != nil {
			t.Errorf("burst request %d was denied: %v", i, err)
		}
	}

	// Next request should be rate limited
	err := limiter.Allow("peer1")
	if err == nil {
		t.Error("expected rate limit error after burst exhausted")
	}

	rateLimitErr, ok := err.(*RateLimitError)
	if !ok {
		t.Errorf("expected *RateLimitError, got %T", err)
	}

	if rateLimitErr.Code != 429 {
		t.Errorf("expected code 429, got %d", rateLimitErr.Code)
	}

	if rateLimitErr.PeerID != "peer1" {
		t.Errorf("expected PeerID 'peer1', got %q", rateLimitErr.PeerID)
	}
}

func TestRateLimiter_PerPeerIsolation(t *testing.T) {
	cfg := RateLimitConfig{
		MaxRequestsPerSecond: 1,
		BurstSize:            2,
		MaxSyncQueueDepth:    100,
		Enabled:              true,
	}

	limiter := NewSyncRateLimiter(cfg)

	// Exhaust peer1's burst
	for i := range 2 {
		if err := limiter.Allow("peer1"); err != nil {
			t.Errorf("peer1 burst request %d was denied: %v", i, err)
		}
	}

	// peer1 should now be rate limited
	if err := limiter.Allow("peer1"); err == nil {
		t.Error("expected peer1 to be rate limited")
	}

	// peer2 should still have full burst available
	for i := range 2 {
		if err := limiter.Allow("peer2"); err != nil {
			t.Errorf("peer2 burst request %d was denied: %v", i, err)
		}
	}

	// peer2 should now be rate limited
	if err := limiter.Allow("peer2"); err == nil {
		t.Error("expected peer2 to be rate limited")
	}
}

func TestRateLimiter_QueueDepthEnforcement(t *testing.T) {
	cfg := RateLimitConfig{
		MaxRequestsPerSecond: 100,
		BurstSize:            100,
		MaxSyncQueueDepth:    5,
		Enabled:              true,
	}

	limiter := NewSyncRateLimiter(cfg)

	// Increment queue to max
	for range 5 {
		limiter.IncrementQueue()
	}

	// Verify queue depth
	if depth := limiter.GetQueueDepth(); depth != 5 {
		t.Errorf("expected queue depth 5, got %d", depth)
	}

	// Next request should be rejected with 503
	err := limiter.Allow("peer1")
	if err == nil {
		t.Error("expected queue depth error when queue is full")
	}

	rateLimitErr, ok := err.(*RateLimitError)
	if !ok {
		t.Errorf("expected *RateLimitError, got %T", err)
	}

	if rateLimitErr.Code != 503 {
		t.Errorf("expected code 503, got %d", rateLimitErr.Code)
	}

	// Decrement queue and try again
	limiter.DecrementQueue()

	if err := limiter.Allow("peer1"); err != nil {
		t.Errorf("request should succeed after queue space freed: %v", err)
	}
}

func TestRateLimiter_DefaultConfigValues(t *testing.T) {
	cfg := RateLimitConfig{
		Enabled: true,
		// All other fields are zero
	}

	limiter := NewSyncRateLimiter(cfg)

	// Verify defaults were applied
	if limiter.config.MaxRequestsPerSecond != float64(DefaultMaxRequestsPerSecond) {
		t.Errorf("expected MaxRequestsPerSecond=%v, got %v",
			float64(DefaultMaxRequestsPerSecond), limiter.config.MaxRequestsPerSecond)
	}

	if limiter.config.BurstSize != DefaultBurstSize {
		t.Errorf("expected BurstSize=%d, got %d",
			DefaultBurstSize, limiter.config.BurstSize)
	}

	if limiter.config.MaxSyncQueueDepth != DefaultMaxSyncQueueDepth {
		t.Errorf("expected MaxSyncQueueDepth=%d, got %d",
			DefaultMaxSyncQueueDepth, limiter.config.MaxSyncQueueDepth)
	}

	// Verify burst works with defaults (should allow DefaultBurstSize requests)
	for i := range DefaultBurstSize {
		if err := limiter.Allow("peer1"); err != nil {
			t.Errorf("burst request %d failed with default config: %v", i, err)
		}
	}

	// Next should be rate limited
	if err := limiter.Allow("peer1"); err == nil {
		t.Error("expected rate limit after default burst exhausted")
	}
}

func TestRateLimiter_ErrorCodes(t *testing.T) {
	cfg := RateLimitConfig{
		MaxRequestsPerSecond: 1,
		BurstSize:            1,
		MaxSyncQueueDepth:    1,
		Enabled:              true,
	}

	limiter := NewSyncRateLimiter(cfg)

	// Test rate limit error (429)
	limiter.Allow("peer1") // consume burst
	err := limiter.Allow("peer1")
	if err == nil {
		t.Fatal("expected rate limit error")
	}

	rateLimitErr, ok := err.(*RateLimitError)
	if !ok {
		t.Fatalf("expected *RateLimitError, got %T", err)
	}

	if rateLimitErr.Code != 429 {
		t.Errorf("expected code 429 for rate limit, got %d", rateLimitErr.Code)
	}

	// Test queue depth error (503)
	limiter.IncrementQueue()
	err = limiter.Allow("peer2") // peer2 has fresh burst
	if err == nil {
		t.Fatal("expected queue depth error")
	}

	queueErr, ok := err.(*RateLimitError)
	if !ok {
		t.Fatalf("expected *RateLimitError, got %T", err)
	}

	if queueErr.Code != 503 {
		t.Errorf("expected code 503 for queue depth, got %d", queueErr.Code)
	}
}

func TestRateLimiter_CleanupStale(t *testing.T) {
	cfg := RateLimitConfig{
		MaxRequestsPerSecond: 10,
		BurstSize:            10,
		MaxSyncQueueDepth:    100,
		Enabled:              true,
	}

	limiter := NewSyncRateLimiter(cfg)

	// Create limiters for multiple peers
	peers := []string{"peer1", "peer2", "peer3", "peer4"}
	for _, peer := range peers {
		if err := limiter.Allow(peer); err != nil {
			t.Errorf("failed to create limiter for %s: %v", peer, err)
		}
	}

	// Verify all limiters were created
	limiter.mu.RLock()
	if len(limiter.limiters) != 4 {
		t.Errorf("expected 4 limiters, got %d", len(limiter.limiters))
	}
	limiter.mu.RUnlock()

	// Wait a bit and access some peers
	time.Sleep(50 * time.Millisecond)
	limiter.Allow("peer1")
	limiter.Allow("peer2")

	// Cleanup with maxAge that should remove peer3 and peer4
	time.Sleep(50 * time.Millisecond)
	removed := limiter.CleanupStale(75 * time.Millisecond)

	if removed != 2 {
		t.Errorf("expected to remove 2 stale limiters, removed %d", removed)
	}

	// Verify only peer1 and peer2 remain
	limiter.mu.RLock()
	if len(limiter.limiters) != 2 {
		t.Errorf("expected 2 limiters after cleanup, got %d", len(limiter.limiters))
	}

	if _, ok := limiter.limiters["peer1"]; !ok {
		t.Error("peer1 should still exist")
	}

	if _, ok := limiter.limiters["peer2"]; !ok {
		t.Error("peer2 should still exist")
	}

	if _, ok := limiter.limiters["peer3"]; ok {
		t.Error("peer3 should have been removed")
	}

	if _, ok := limiter.limiters["peer4"]; ok {
		t.Error("peer4 should have been removed")
	}
	limiter.mu.RUnlock()
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	cfg := RateLimitConfig{
		MaxRequestsPerSecond: 100,
		BurstSize:            50,
		MaxSyncQueueDepth:    1000,
		Enabled:              true,
	}

	limiter := NewSyncRateLimiter(cfg)

	// Simulate concurrent requests from multiple peers
	done := make(chan bool, 10)
	for i := range 10 {
		peerID := "peer" + string(rune('0'+i))
		go func(id string) {
			for range 100 {
				limiter.Allow(id)
			}
			done <- true
		}(peerID)
	}

	// Wait for all goroutines
	for range 10 {
		<-done
	}

	// Verify no panics occurred and limiters were created
	limiter.mu.RLock()
	numLimiters := len(limiter.limiters)
	limiter.mu.RUnlock()

	if numLimiters != 10 {
		t.Errorf("expected 10 limiters after concurrent access, got %d", numLimiters)
	}
}

func TestRateLimiter_QueueOperations(t *testing.T) {
	cfg := RateLimitConfig{
		MaxRequestsPerSecond: 100,
		BurstSize:            100,
		MaxSyncQueueDepth:    100,
		Enabled:              true,
	}

	limiter := NewSyncRateLimiter(cfg)

	// Test increment/decrement operations
	if depth := limiter.GetQueueDepth(); depth != 0 {
		t.Errorf("initial queue depth should be 0, got %d", depth)
	}

	limiter.IncrementQueue()
	if depth := limiter.GetQueueDepth(); depth != 1 {
		t.Errorf("queue depth should be 1 after increment, got %d", depth)
	}

	limiter.IncrementQueue()
	limiter.IncrementQueue()
	if depth := limiter.GetQueueDepth(); depth != 3 {
		t.Errorf("queue depth should be 3, got %d", depth)
	}

	limiter.DecrementQueue()
	if depth := limiter.GetQueueDepth(); depth != 2 {
		t.Errorf("queue depth should be 2 after decrement, got %d", depth)
	}

	limiter.DecrementQueue()
	limiter.DecrementQueue()
	if depth := limiter.GetQueueDepth(); depth != 0 {
		t.Errorf("queue depth should be 0 after all decrements, got %d", depth)
	}
}

func TestRateLimiter_ErrorMessage(t *testing.T) {
	err := &RateLimitError{
		Code:    429,
		Message: "too many requests",
		PeerID:  "test-peer",
	}

	expected := "rate limit error (code 429) for peer test-peer: too many requests"
	if err.Error() != expected {
		t.Errorf("error message mismatch\nexpected: %s\ngot:      %s", expected, err.Error())
	}
}
