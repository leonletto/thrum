package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Sentinel errors for the email-secrets loader. Callers (daemon startup,
// bridge wiring) discriminate with errors.Is to decide whether the
// condition is fatal (bridge wanted but un-provisioned) or benign
// (bridge disabled, no secrets file is normal).
var (
	// ErrEmailSecretsMissing — the secrets file does not exist on disk.
	// Fatal when EmailConfig.Enabled is true; benign when false.
	ErrEmailSecretsMissing = errors.New("email secrets file not found")

	// ErrEmailSecretsMode — the secrets file mode permits read/write
	// access beyond the owner. Always fatal: a 0644 credentials file
	// is a security incident regardless of bridge state.
	ErrEmailSecretsMode = errors.New("email secrets file has too-permissive mode (require 0600)")

	// ErrEmailSecretMissingField — the secrets file parsed but a
	// required credential is empty. Fatal when bridge is enabled.
	ErrEmailSecretMissingField = errors.New("email secrets file missing required field")
)

// EmailSecrets holds the credentials read from .thrum/secrets/email.json.
// Mirrors the v0.11 password-auth surface plus the v0.11.x-reserved
// OAuth block. The struct is intentionally narrow — only fields the
// daemon directly consumes appear here.
type EmailSecrets struct {
	IMAPPassword string           `json:"imap_password,omitempty"`
	SMTPPassword string           `json:"smtp_password,omitempty"`
	OAuth        EmailOAuthFields `json:"oauth,omitzero"` // reserved v0.11.x; parsed but unused
}

// EmailOAuthFields mirrors the OAuth 2.0 / XOAUTH2 block reserved in
// design-spec §4. Present in the loader so a v0.11.x release can wire
// it without a struct-shape migration; v0.11 does not consume any field.
type EmailOAuthFields struct {
	ClientID      string `json:"client_id,omitempty"`
	RefreshToken  string `json:"refresh_token,omitempty"`
	TokenEndpoint string `json:"token_endpoint,omitempty"`
}

// LoadEmailSecrets reads and parses .thrum/secrets/email.json.
//
// enabled is the EmailConfig.Enabled value from .thrum/config.json — it
// gates the missing-file behavior: an absent file is fatal when the
// bridge is on, benign (returns nil, nil) when off. The mode check and
// missing-field check always fire regardless of enabled.
//
// Mode rule: stat.Mode() & 0o177 must equal zero (i.e. permissions are
// at most 0o600). 0o644, 0o660, 0o666 all reject.
func LoadEmailSecrets(path string, enabled bool) (*EmailSecrets, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !enabled {
				return nil, nil
			}
			return nil, fmt.Errorf("%w: %s", ErrEmailSecretsMissing, path)
		}
		return nil, fmt.Errorf("stat email secrets: %w", err)
	}

	if info.Mode().Perm()&0o177 != 0 {
		return nil, fmt.Errorf("%w: %s has mode %#o", ErrEmailSecretsMode, path, info.Mode().Perm())
	}

	data, err := os.ReadFile(path) // #nosec G304 -- path comes from daemon config, treated as trusted input
	if err != nil {
		return nil, fmt.Errorf("read email secrets: %w", err)
	}

	var secrets EmailSecrets
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("parse email secrets: %w", err)
	}

	if enabled {
		if secrets.IMAPPassword == "" {
			return nil, fmt.Errorf("%w: imap_password", ErrEmailSecretMissingField)
		}
		if secrets.SMTPPassword == "" {
			return nil, fmt.Errorf("%w: smtp_password", ErrEmailSecretMissingField)
		}
	}

	return &secrets, nil
}
