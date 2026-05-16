package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// D-B1.3 — Email secrets loader. .thrum/secrets/email.json holds the
// IMAP/SMTP passwords (and reserved OAuth fields) that must NOT live in
// .thrum/config.json. The loader enforces 0600 mode to prevent other
// users on the machine from reading credentials, and uses sentinel
// errors so callers can distinguish "secret-file missing" (operator
// hasn't provisioned email yet, fine when bridge is disabled) from
// "JSON malformed" (programmer error, never fine).

// writeSecretsFile writes the given JSON body at .thrum/secrets/email.json
// inside tmpDir and forces the desired mode (avoiding umask interference).
func writeSecretsFile(t *testing.T, tmpDir, body string, mode os.FileMode) string {
	t.Helper()
	dir := filepath.Join(tmpDir, "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "email.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEmailSecrets_LoadHappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	path := writeSecretsFile(t, tmpDir,
		`{"imap_password":"i-pw","smtp_password":"s-pw"}`, 0o600)

	secrets, err := config.LoadEmailSecrets(path, true)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if secrets == nil {
		t.Fatal("expected non-nil secrets struct")
	}
	if secrets.IMAPPassword != "i-pw" {
		t.Errorf("IMAPPassword=%q, want i-pw", secrets.IMAPPassword)
	}
	if secrets.SMTPPassword != "s-pw" {
		t.Errorf("SMTPPassword=%q, want s-pw", secrets.SMTPPassword)
	}
}

func TestEmailSecrets_FileMode0644Rejected(t *testing.T) {
	tmpDir := t.TempDir()
	path := writeSecretsFile(t, tmpDir,
		`{"imap_password":"i-pw","smtp_password":"s-pw"}`, 0o644)

	_, err := config.LoadEmailSecrets(path, true)
	if err == nil {
		t.Fatal("expected mode-error for 0644, got nil")
	}
	if !errors.Is(err, config.ErrEmailSecretsMode) {
		t.Errorf("expected ErrEmailSecretsMode, got %v", err)
	}
}

func TestEmailSecrets_FileMode0666Rejected(t *testing.T) {
	tmpDir := t.TempDir()
	path := writeSecretsFile(t, tmpDir,
		`{"imap_password":"i-pw","smtp_password":"s-pw"}`, 0o666)

	_, err := config.LoadEmailSecrets(path, true)
	if err == nil {
		t.Fatal("expected mode-error for 0666, got nil")
	}
	if !errors.Is(err, config.ErrEmailSecretsMode) {
		t.Errorf("expected ErrEmailSecretsMode, got %v", err)
	}
}

func TestEmailSecrets_MissingFileEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	missing := filepath.Join(tmpDir, "no-such-file.json")

	_, err := config.LoadEmailSecrets(missing, true)
	if err == nil {
		t.Fatal("expected ErrEmailSecretsMissing for absent file + enabled, got nil")
	}
	if !errors.Is(err, config.ErrEmailSecretsMissing) {
		t.Errorf("expected ErrEmailSecretsMissing, got %v", err)
	}
}

func TestEmailSecrets_MissingFileDisabled(t *testing.T) {
	// Bridge disabled + no secrets file = startup proceeds without email.
	tmpDir := t.TempDir()
	missing := filepath.Join(tmpDir, "no-such-file.json")

	secrets, err := config.LoadEmailSecrets(missing, false)
	if err != nil {
		t.Fatalf("expected nil error for absent file + disabled, got %v", err)
	}
	if secrets != nil {
		t.Errorf("expected nil secrets when bridge disabled and file absent, got %#v", secrets)
	}
}

func TestEmailSecrets_EmptyImapPassword(t *testing.T) {
	tmpDir := t.TempDir()
	path := writeSecretsFile(t, tmpDir,
		`{"imap_password":"","smtp_password":"s-pw"}`, 0o600)

	_, err := config.LoadEmailSecrets(path, true)
	if err == nil {
		t.Fatal("expected ErrEmailSecretMissingField for empty imap_password, got nil")
	}
	if !errors.Is(err, config.ErrEmailSecretMissingField) {
		t.Errorf("expected ErrEmailSecretMissingField, got %v", err)
	}
}

func TestEmailSecrets_OauthFieldsParsedIgnored(t *testing.T) {
	// OAuth fields reserved for v0.11.x — present in JSON should parse
	// (no error) but the loader does not consume them in v0.11.
	tmpDir := t.TempDir()
	path := writeSecretsFile(t, tmpDir,
		`{"imap_password":"i-pw","smtp_password":"s-pw","oauth":{"client_id":"cid","refresh_token":"rt","token_endpoint":"te"}}`,
		0o600)

	secrets, err := config.LoadEmailSecrets(path, true)
	if err != nil {
		t.Fatalf("expected nil error with oauth fields present, got %v", err)
	}
	if secrets.OAuth.ClientID != "cid" || secrets.OAuth.RefreshToken != "rt" || secrets.OAuth.TokenEndpoint != "te" {
		t.Errorf("OAuth fields not parsed correctly: %#v", secrets.OAuth)
	}
}
