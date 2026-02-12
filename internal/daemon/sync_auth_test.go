package daemon

import (
	"strings"
	"testing"
)

func TestSyncAuth_NoAuthRequired(t *testing.T) {
	cfg := SyncAuthConfig{
		RequireAuth: false,
	}
	auth := NewSyncAuthorizer(cfg)

	// All peers should pass when auth is not required
	tests := []struct {
		name          string
		hostname      string
		tags          []string
		loginName     string
		shouldSucceed bool
	}{
		{"any peer 1", "peer1", []string{}, "alice@example.com", true},
		{"any peer 2", "peer2", []string{"tag:something"}, "bob@other.com", true},
		{"any peer 3", "untrusted", []string{}, "evil@bad.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.AuthorizePeer(tt.hostname, tt.tags, tt.loginName)
			if tt.shouldSucceed && err != nil {
				t.Errorf("expected success, got error: %v", err)
			}
			if !tt.shouldSucceed && err == nil {
				t.Errorf("expected error, got success")
			}
		})
	}
}

func TestSyncAuth_AllowedPeers(t *testing.T) {
	cfg := SyncAuthConfig{
		RequireAuth:  true,
		AllowedPeers: []string{"peer1", "peer2", "trusted-host"},
	}
	auth := NewSyncAuthorizer(cfg)

	tests := []struct {
		name          string
		hostname      string
		tags          []string
		loginName     string
		shouldSucceed bool
	}{
		{"allowed peer1", "peer1", []string{}, "alice@example.com", true},
		{"allowed peer2", "peer2", []string{}, "bob@example.com", true},
		{"allowed trusted-host", "trusted-host", []string{}, "charlie@example.com", true},
		{"not allowed peer3", "peer3", []string{}, "dave@example.com", false},
		{"not allowed unknown", "unknown", []string{}, "eve@example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.AuthorizePeer(tt.hostname, tt.tags, tt.loginName)
			if tt.shouldSucceed && err != nil {
				t.Errorf("expected success, got error: %v", err)
			}
			if !tt.shouldSucceed && err == nil {
				t.Errorf("expected error, got success")
			}
			if !tt.shouldSucceed && err != nil {
				if !strings.Contains(err.Error(), "not in allowed peers list") {
					t.Errorf("expected 'not in allowed peers list' in error, got: %v", err)
				}
			}
		})
	}
}

func TestSyncAuth_RequiredTags(t *testing.T) {
	cfg := SyncAuthConfig{
		RequireAuth:  true,
		RequiredTags: []string{"tag:thrum-daemon", "tag:sync-server"},
	}
	auth := NewSyncAuthorizer(cfg)

	tests := []struct {
		name          string
		hostname      string
		tags          []string
		loginName     string
		shouldSucceed bool
	}{
		{"has first required tag", "peer1", []string{"tag:thrum-daemon"}, "alice@example.com", true},
		{"has second required tag", "peer2", []string{"tag:sync-server"}, "bob@example.com", true},
		{"has both tags", "peer3", []string{"tag:thrum-daemon", "tag:sync-server"}, "charlie@example.com", true},
		{"has required tag plus others", "peer4", []string{"tag:other", "tag:thrum-daemon", "tag:more"}, "dave@example.com", true},
		{"no tags", "peer5", []string{}, "eve@example.com", false},
		{"wrong tags", "peer6", []string{"tag:wrong", "tag:other"}, "frank@example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.AuthorizePeer(tt.hostname, tt.tags, tt.loginName)
			if tt.shouldSucceed && err != nil {
				t.Errorf("expected success, got error: %v", err)
			}
			if !tt.shouldSucceed && err == nil {
				t.Errorf("expected error, got success")
			}
			if !tt.shouldSucceed && err != nil {
				if !strings.Contains(err.Error(), "required tags") {
					t.Errorf("expected 'required tags' in error, got: %v", err)
				}
			}
		})
	}
}

func TestSyncAuth_AllowedDomain(t *testing.T) {
	cfg := SyncAuthConfig{
		RequireAuth:   true,
		AllowedDomain: "@company.com",
	}
	auth := NewSyncAuthorizer(cfg)

	tests := []struct {
		name          string
		hostname      string
		tags          []string
		loginName     string
		shouldSucceed bool
	}{
		{"allowed domain", "peer1", []string{}, "alice@company.com", true},
		{"allowed domain different user", "peer2", []string{}, "bob@company.com", true},
		{"wrong domain", "peer3", []string{}, "charlie@other.com", false},
		{"wrong domain similar", "peer4", []string{}, "dave@mycompany.com", false},
		{"no domain", "peer5", []string{}, "eve", false},
		{"empty login", "peer6", []string{}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.AuthorizePeer(tt.hostname, tt.tags, tt.loginName)
			if tt.shouldSucceed && err != nil {
				t.Errorf("expected success, got error: %v", err)
			}
			if !tt.shouldSucceed && err == nil {
				t.Errorf("expected error, got success")
			}
			if !tt.shouldSucceed && err != nil {
				if !strings.Contains(err.Error(), "allowed domain") {
					t.Errorf("expected 'allowed domain' in error, got: %v", err)
				}
			}
		})
	}
}

func TestSyncAuth_CombinedChecks(t *testing.T) {
	cfg := SyncAuthConfig{
		RequireAuth:   true,
		AllowedPeers:  []string{"peer1", "peer2"},
		RequiredTags:  []string{"tag:thrum-daemon"},
		AllowedDomain: "@company.com",
	}
	auth := NewSyncAuthorizer(cfg)

	tests := []struct {
		name          string
		hostname      string
		tags          []string
		loginName     string
		shouldSucceed bool
		expectedErr   string
	}{
		{
			name:          "all checks pass",
			hostname:      "peer1",
			tags:          []string{"tag:thrum-daemon"},
			loginName:     "alice@company.com",
			shouldSucceed: true,
		},
		{
			name:          "all checks pass peer2",
			hostname:      "peer2",
			tags:          []string{"tag:thrum-daemon", "tag:other"},
			loginName:     "bob@company.com",
			shouldSucceed: true,
		},
		{
			name:          "wrong hostname",
			hostname:      "peer3",
			tags:          []string{"tag:thrum-daemon"},
			loginName:     "charlie@company.com",
			shouldSucceed: false,
			expectedErr:   "not in allowed peers list",
		},
		{
			name:          "wrong tags",
			hostname:      "peer1",
			tags:          []string{"tag:wrong"},
			loginName:     "dave@company.com",
			shouldSucceed: false,
			expectedErr:   "required tags",
		},
		{
			name:          "wrong domain",
			hostname:      "peer1",
			tags:          []string{"tag:thrum-daemon"},
			loginName:     "eve@other.com",
			shouldSucceed: false,
			expectedErr:   "allowed domain",
		},
		{
			name:          "multiple failures",
			hostname:      "peer3",
			tags:          []string{"tag:wrong"},
			loginName:     "frank@other.com",
			shouldSucceed: false,
			expectedErr:   "not in allowed peers list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.AuthorizePeer(tt.hostname, tt.tags, tt.loginName)
			if tt.shouldSucceed && err != nil {
				t.Errorf("expected success, got error: %v", err)
			}
			if !tt.shouldSucceed && err == nil {
				t.Errorf("expected error, got success")
			}
			if !tt.shouldSucceed && err != nil && tt.expectedErr != "" {
				if !strings.Contains(err.Error(), tt.expectedErr) {
					t.Errorf("expected %q in error, got: %v", tt.expectedErr, err)
				}
			}
		})
	}
}

func TestSyncAuth_EmptyConfig(t *testing.T) {
	// Empty config with requireAuth=true should allow all peers
	// (no checks configured, so all configured checks pass)
	cfg := SyncAuthConfig{
		RequireAuth: true,
	}
	auth := NewSyncAuthorizer(cfg)

	tests := []struct {
		name      string
		hostname  string
		tags      []string
		loginName string
	}{
		{"peer1", "peer1", []string{}, "alice@example.com"},
		{"peer2", "peer2", []string{"tag:something"}, "bob@other.com"},
		{"peer3", "peer3", []string{"tag:a", "tag:b"}, "charlie@company.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.AuthorizePeer(tt.hostname, tt.tags, tt.loginName)
			if err != nil {
				t.Errorf("expected success with empty config, got error: %v", err)
			}
		})
	}
}

func TestSyncAuth_DenialReasons(t *testing.T) {
	// Test that denial reasons are specific and helpful
	t.Run("hostname not in list", func(t *testing.T) {
		cfg := SyncAuthConfig{
			RequireAuth:  true,
			AllowedPeers: []string{"peer1"},
		}
		auth := NewSyncAuthorizer(cfg)
		err := auth.AuthorizePeer("peer2", []string{}, "alice@example.com")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "peer2") {
			t.Errorf("error should contain the hostname: %v", err)
		}
		if !strings.Contains(err.Error(), "not in allowed peers list") {
			t.Errorf("error should explain the reason: %v", err)
		}
	})

	t.Run("missing required tags", func(t *testing.T) {
		cfg := SyncAuthConfig{
			RequireAuth:  true,
			RequiredTags: []string{"tag:thrum-daemon"},
		}
		auth := NewSyncAuthorizer(cfg)
		err := auth.AuthorizePeer("peer1", []string{"tag:other"}, "alice@example.com")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "required tags") {
			t.Errorf("error should mention required tags: %v", err)
		}
		if !strings.Contains(err.Error(), "tag:thrum-daemon") {
			t.Errorf("error should show what tags are required: %v", err)
		}
		if !strings.Contains(err.Error(), "tag:other") {
			t.Errorf("error should show what tags the peer has: %v", err)
		}
	})

	t.Run("wrong domain", func(t *testing.T) {
		cfg := SyncAuthConfig{
			RequireAuth:   true,
			AllowedDomain: "@company.com",
		}
		auth := NewSyncAuthorizer(cfg)
		err := auth.AuthorizePeer("peer1", []string{}, "alice@other.com")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "alice@other.com") {
			t.Errorf("error should contain the login name: %v", err)
		}
		if !strings.Contains(err.Error(), "@company.com") {
			t.Errorf("error should contain the allowed domain: %v", err)
		}
		if !strings.Contains(err.Error(), "allowed domain") {
			t.Errorf("error should explain the reason: %v", err)
		}
	})
}
