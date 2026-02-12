package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// SecurityConfig holds security-related configuration for sync.
type SecurityConfig struct {
	// Event validation limits
	MaxEventSize      int `json:"max_event_size"`       // Maximum event size in bytes (default: 1MB)
	MaxBatchSize      int `json:"max_batch_size"`       // Maximum events per sync batch (default: 1000)
	MaxMessageSize    int `json:"max_message_size"`     // Maximum message content size (default: 100KB)
	RequireSignatures bool `json:"require_signatures"`  // Reject unsigned events (default: false)

	// Rate limiting
	RateLimitEnabled     bool    `json:"rate_limit_enabled"`      // Enable rate limiting (default: true)
	MaxRequestsPerSecond float64 `json:"max_requests_per_second"` // Per-peer request rate (default: 10)
	BurstSize            int     `json:"burst_size"`              // Per-peer burst allowance (default: 20)
	MaxSyncQueueDepth    int     `json:"max_sync_queue_depth"`    // Maximum sync queue depth (default: 1000)

	// Authorization
	RequireAuth   bool     `json:"require_auth"`    // Require Tailscale WhoIs auth (default: false)
	AllowedPeers  []string `json:"allowed_peers"`   // Allowed peer hostnames
	RequiredTags  []string `json:"required_tags"`   // Required ACL tags (e.g., ["tag:thrum-daemon"])
	AllowedDomain string   `json:"allowed_domain"`  // Required domain suffix (e.g., "@company.com")
}

// Default security configuration values.
const (
	DefaultMaxEventSize      = 1 * 1024 * 1024  // 1 MB
	DefaultMaxBatchSize      = 1000
	DefaultMaxMessageSize    = 100 * 1024        // 100 KB
	DefaultMaxRequestsPerSec = 10.0
	DefaultBurst             = 20
	DefaultMaxQueueDepth     = 1000
)

// LoadSecurityConfig loads security configuration from environment variables.
// Falls back to sensible defaults if not set.
//
// Environment variables:
//   - THRUM_SECURITY_MAX_EVENT_SIZE: max event size in bytes
//   - THRUM_SECURITY_MAX_BATCH_SIZE: max events per sync batch
//   - THRUM_SECURITY_MAX_MESSAGE_SIZE: max message content size
//   - THRUM_SECURITY_REQUIRE_SIGNATURES: "true" to reject unsigned events
//   - THRUM_SECURITY_RATE_LIMIT_ENABLED: "true"/"false" (default: true)
//   - THRUM_SECURITY_MAX_RPS: max requests per second per peer
//   - THRUM_SECURITY_BURST_SIZE: burst allowance
//   - THRUM_SECURITY_MAX_QUEUE_DEPTH: max sync queue depth
//   - THRUM_SECURITY_REQUIRE_AUTH: "true" to require WhoIs auth
//   - THRUM_SECURITY_ALLOWED_DOMAIN: required login domain suffix
func LoadSecurityConfig() SecurityConfig {
	cfg := SecurityConfig{
		MaxEventSize:         DefaultMaxEventSize,
		MaxBatchSize:         DefaultMaxBatchSize,
		MaxMessageSize:       DefaultMaxMessageSize,
		RateLimitEnabled:     true,
		MaxRequestsPerSecond: DefaultMaxRequestsPerSec,
		BurstSize:            DefaultBurst,
		MaxSyncQueueDepth:    DefaultMaxQueueDepth,
	}

	if v := envInt("THRUM_SECURITY_MAX_EVENT_SIZE"); v > 0 {
		cfg.MaxEventSize = v
	}
	if v := envInt("THRUM_SECURITY_MAX_BATCH_SIZE"); v > 0 {
		cfg.MaxBatchSize = v
	}
	if v := envInt("THRUM_SECURITY_MAX_MESSAGE_SIZE"); v > 0 {
		cfg.MaxMessageSize = v
	}
	if envBool("THRUM_SECURITY_REQUIRE_SIGNATURES") {
		cfg.RequireSignatures = true
	}
	// Rate limit enabled defaults to true; allow explicit disable
	if v := os.Getenv("THRUM_SECURITY_RATE_LIMIT_ENABLED"); v == "false" || v == "0" {
		cfg.RateLimitEnabled = false
	}
	if v := envFloat("THRUM_SECURITY_MAX_RPS"); v > 0 {
		cfg.MaxRequestsPerSecond = v
	}
	if v := envInt("THRUM_SECURITY_BURST_SIZE"); v > 0 {
		cfg.BurstSize = v
	}
	if v := envInt("THRUM_SECURITY_MAX_QUEUE_DEPTH"); v > 0 {
		cfg.MaxSyncQueueDepth = v
	}
	if envBool("THRUM_SECURITY_REQUIRE_AUTH") {
		cfg.RequireAuth = true
	}
	if v := os.Getenv("THRUM_SECURITY_ALLOWED_DOMAIN"); v != "" {
		cfg.AllowedDomain = v
	}
	if v := os.Getenv("THRUM_SECURITY_ALLOWED_PEERS"); v != "" {
		for _, peer := range strings.Split(v, ",") {
			peer = strings.TrimSpace(peer)
			if peer != "" {
				cfg.AllowedPeers = append(cfg.AllowedPeers, peer)
			}
		}
	}
	if v := os.Getenv("THRUM_SECURITY_REQUIRED_TAGS"); v != "" {
		for _, tag := range strings.Split(v, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				cfg.RequiredTags = append(cfg.RequiredTags, tag)
			}
		}
	}

	return cfg
}

// Validate checks that the security configuration has valid values.
func (c *SecurityConfig) Validate() error {
	if c.MaxEventSize <= 0 {
		return fmt.Errorf("max_event_size must be positive, got %d", c.MaxEventSize)
	}
	if c.MaxBatchSize <= 0 {
		return fmt.Errorf("max_batch_size must be positive, got %d", c.MaxBatchSize)
	}
	if c.MaxMessageSize <= 0 {
		return fmt.Errorf("max_message_size must be positive, got %d", c.MaxMessageSize)
	}
	if c.MaxRequestsPerSecond <= 0 {
		return fmt.Errorf("max_requests_per_second must be positive, got %v", c.MaxRequestsPerSecond)
	}
	if c.BurstSize <= 0 {
		return fmt.Errorf("burst_size must be positive, got %d", c.BurstSize)
	}
	if c.MaxSyncQueueDepth <= 0 {
		return fmt.Errorf("max_sync_queue_depth must be positive, got %d", c.MaxSyncQueueDepth)
	}
	return nil
}

// envInt reads an integer from an environment variable, returning 0 if unset or invalid.
func envInt(key string) int {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

// envFloat reads a float64 from an environment variable, returning 0 if unset or invalid.
func envFloat(key string) float64 {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f
}
