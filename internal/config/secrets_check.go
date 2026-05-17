package config

import (
	"encoding/json"
	"fmt"
)

// CheckForSecretNames walks decoded JSON and rejects field names that
// belong in .thrum/secrets/email.json (MB-1 Seed 1). The check protects
// operators from accidentally committing credentials into .thrum/config.json.
//
// Matched names (at any nesting depth):
//
//   - imap_password
//   - smtp_password
//   - oauth.refresh_token   (refresh_token whose immediate parent key is "oauth")
//
// daemon_id is NOT a credential — public peer identifiers under
// email.peers[] pass through cleanly.
//
// Returns nil on success; on a hit, returns an error naming the leaked
// field. LoadThrumConfig calls this before the typed Unmarshal so we
// fail load fast rather than after partial parse.
func CheckForSecretNames(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// Malformed JSON is not our problem — let the typed unmarshal in
		// LoadThrumConfig surface a useful error.
		return nil //nolint:nilerr // delegated upstream
	}
	return walkSecretNames(v, "")
}

func walkSecretNames(v any, parentKey string) error {
	switch node := v.(type) {
	case map[string]any:
		for k, child := range node {
			if leaked := matchSecretName(k, parentKey); leaked != "" {
				return fmt.Errorf("secret-name leak: %q must live in .thrum/secrets/email.json, not .thrum/config.json", leaked)
			}
			if err := walkSecretNames(child, k); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range node {
			// Lists carry no useful parent-key context for nested oauth detection;
			// the parent of a list element is the list itself, not an oauth wrapper.
			if err := walkSecretNames(child, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// matchSecretName returns the canonical leaked name (e.g.
// "oauth.refresh_token") or "" if the key is not a known secret.
func matchSecretName(key, parentKey string) string {
	switch key {
	case "imap_password", "smtp_password":
		return key
	case "refresh_token":
		if parentKey == "oauth" {
			return "oauth.refresh_token"
		}
	}
	return ""
}
