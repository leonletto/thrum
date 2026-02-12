package config

import (
	"os"
	"testing"
)

func TestSecurityConfig_Defaults(t *testing.T) {
	// Clear any env vars that might be set
	for _, key := range []string{
		"THRUM_SECURITY_MAX_EVENT_SIZE",
		"THRUM_SECURITY_MAX_BATCH_SIZE",
		"THRUM_SECURITY_REQUIRE_SIGNATURES",
		"THRUM_SECURITY_RATE_LIMIT_ENABLED",
	} {
		os.Unsetenv(key)
	}

	cfg := LoadSecurityConfig()

	if cfg.MaxEventSize != DefaultMaxEventSize {
		t.Errorf("MaxEventSize = %d, want %d", cfg.MaxEventSize, DefaultMaxEventSize)
	}
	if cfg.MaxBatchSize != DefaultMaxBatchSize {
		t.Errorf("MaxBatchSize = %d, want %d", cfg.MaxBatchSize, DefaultMaxBatchSize)
	}
	if cfg.MaxMessageSize != DefaultMaxMessageSize {
		t.Errorf("MaxMessageSize = %d, want %d", cfg.MaxMessageSize, DefaultMaxMessageSize)
	}
	if cfg.RequireSignatures {
		t.Error("RequireSignatures should be false by default")
	}
	if !cfg.RateLimitEnabled {
		t.Error("RateLimitEnabled should be true by default")
	}
	if cfg.MaxRequestsPerSecond != DefaultMaxRequestsPerSec {
		t.Errorf("MaxRequestsPerSecond = %v, want %v", cfg.MaxRequestsPerSecond, DefaultMaxRequestsPerSec)
	}
}

func TestSecurityConfig_EnvOverrides(t *testing.T) {
	t.Setenv("THRUM_SECURITY_MAX_EVENT_SIZE", "2048")
	t.Setenv("THRUM_SECURITY_REQUIRE_SIGNATURES", "true")
	t.Setenv("THRUM_SECURITY_RATE_LIMIT_ENABLED", "false")
	t.Setenv("THRUM_SECURITY_MAX_RPS", "5.5")
	t.Setenv("THRUM_SECURITY_ALLOWED_DOMAIN", "@test.com")

	cfg := LoadSecurityConfig()

	if cfg.MaxEventSize != 2048 {
		t.Errorf("MaxEventSize = %d, want 2048", cfg.MaxEventSize)
	}
	if !cfg.RequireSignatures {
		t.Error("RequireSignatures should be true")
	}
	if cfg.RateLimitEnabled {
		t.Error("RateLimitEnabled should be false")
	}
	if cfg.MaxRequestsPerSecond != 5.5 {
		t.Errorf("MaxRequestsPerSecond = %v, want 5.5", cfg.MaxRequestsPerSecond)
	}
	if cfg.AllowedDomain != "@test.com" {
		t.Errorf("AllowedDomain = %q, want %q", cfg.AllowedDomain, "@test.com")
	}
}

func TestSecurityConfig_Validate_Valid(t *testing.T) {
	cfg := LoadSecurityConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should be valid: %v", err)
	}
}

func TestSecurityConfig_Validate_Invalid(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*SecurityConfig)
	}{
		{"zero max_event_size", func(c *SecurityConfig) { c.MaxEventSize = 0 }},
		{"negative max_batch_size", func(c *SecurityConfig) { c.MaxBatchSize = -1 }},
		{"zero max_message_size", func(c *SecurityConfig) { c.MaxMessageSize = 0 }},
		{"zero max_rps", func(c *SecurityConfig) { c.MaxRequestsPerSecond = 0 }},
		{"negative burst", func(c *SecurityConfig) { c.BurstSize = -5 }},
		{"zero queue_depth", func(c *SecurityConfig) { c.MaxSyncQueueDepth = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := LoadSecurityConfig()
			tt.modify(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}
