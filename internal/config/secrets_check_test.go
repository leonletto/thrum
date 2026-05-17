package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// D-B1.2 — secret-name validator. The leak check (MB-1 Seed 1) rejects
// .thrum/config.json containing field names that belong in
// .thrum/secrets/email.json. Field names are matched at any nesting depth
// inside the config tree.

func TestSecretsCheck_RejectImapPassword(t *testing.T) {
	cases := []string{
		`{"email":{"imap":{"host":"x","imap_password":"shh"}}}`,
		`{"imap_password":"shh"}`,
		`{"email":{"nested":{"deeper":{"imap_password":"shh"}}}}`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			err := config.CheckForSecretNames([]byte(raw))
			if err == nil {
				t.Fatalf("expected error for imap_password, got nil")
			}
			if !strings.Contains(err.Error(), "imap_password") {
				t.Errorf("expected error to mention imap_password, got %q", err.Error())
			}
		})
	}
}

func TestSecretsCheck_RejectSmtpPassword(t *testing.T) {
	cases := []string{
		`{"email":{"smtp":{"host":"x","smtp_password":"shh"}}}`,
		`{"smtp_password":"shh"}`,
		`{"a":{"b":{"c":{"smtp_password":"shh"}}}}`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			err := config.CheckForSecretNames([]byte(raw))
			if err == nil {
				t.Fatalf("expected error for smtp_password, got nil")
			}
			if !strings.Contains(err.Error(), "smtp_password") {
				t.Errorf("expected error to mention smtp_password, got %q", err.Error())
			}
		})
	}
}

func TestSecretsCheck_RejectOauthRefresh(t *testing.T) {
	// oauth.refresh_token = a refresh_token key whose parent key is oauth.
	// A bare refresh_token field elsewhere is not flagged (no false positive
	// on tokens that aren't OAuth-scoped — e.g. a "refresh_token" field
	// inside an unrelated cache config).
	cases := []string{
		`{"email":{"oauth":{"refresh_token":"r"}}}`,
		`{"oauth":{"refresh_token":"r"}}`,
		`{"a":{"oauth":{"client_id":"x","refresh_token":"r"}}}`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			err := config.CheckForSecretNames([]byte(raw))
			if err == nil {
				t.Fatalf("expected error for oauth.refresh_token, got nil")
			}
			if !strings.Contains(err.Error(), "oauth.refresh_token") {
				t.Errorf("expected error to mention oauth.refresh_token, got %q", err.Error())
			}
		})
	}
}

func TestSecretsCheck_AllowEmailPeersField(t *testing.T) {
	// email.peers[].daemon_id is NOT a credential — daemon-IDs are public
	// peer identifiers. The check must not flag them.
	raw := `{
	  "email": {
	    "peers": [
	      {"handle": "laptop-thrum", "daemon_id": "ab12cd34-...", "trust": "full"}
	    ]
	  }
	}`
	if err := config.CheckForSecretNames([]byte(raw)); err != nil {
		t.Errorf("expected daemon_id to be allowed, got error: %v", err)
	}
}

func TestSecretsCheck_LoadFailsOnSecret(t *testing.T) {
	// Wired into LoadThrumConfig — load fails fast on a config.json that
	// contains a known-secret field name. This is the integration assertion
	// behind the in-isolation tests above.
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"email":{"smtp_password":"leaked"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadThrumConfig(tmpDir)
	if err == nil {
		t.Fatal("expected load to fail on leaked smtp_password, got nil")
	}
	if !strings.Contains(err.Error(), "smtp_password") {
		t.Errorf("expected error to mention smtp_password, got %q", err.Error())
	}
}
