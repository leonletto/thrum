package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// EnsureIdentityKeys checks if an Ed25519 key pair exists at {stateDir}/identity.key.
// If the key file exists, it loads and parses the PEM-encoded private key.
// If the key file does not exist, it generates a new Ed25519 key pair and saves it.
// Returns the public key, private key, and any error encountered.
//
// The private key is saved as a PKCS8-encoded PEM file with 0600 permissions.
// The parent directory is created with 0700 permissions if it doesn't exist.
// The public key fingerprint (SHA256) is logged for verification purposes.
func EnsureIdentityKeys(stateDir string) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	keyPath := filepath.Join(stateDir, "identity.key")

	// Ensure parent directory exists with 0700 permissions
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("create state directory: %w", err)
	}

	// Check if key file exists
	if _, err := os.Stat(keyPath); err == nil {
		// Key file exists, load it
		pub, priv, err := loadIdentityKeys(keyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("load identity keys: %w", err)
		}

		fingerprint := PublicKeyFingerprint(pub)
		log.Printf("identity: loaded existing keys from %s (fingerprint: %s)", keyPath, fingerprint)
		return pub, priv, nil
	} else if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("stat key file: %w", err)
	}

	// Key file doesn't exist, generate new keys
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	// Save private key as PEM
	if err := saveIdentityKeys(keyPath, priv); err != nil {
		return nil, nil, fmt.Errorf("save identity keys: %w", err)
	}

	fingerprint := PublicKeyFingerprint(pub)
	log.Printf("identity: generated new keys at %s (fingerprint: %s)", keyPath, fingerprint)
	return pub, priv, nil
}

// PublicKeyFingerprint computes the fingerprint of an Ed25519 public key.
// Returns a string in the format "SHA256:base64(sha256(publicKeyBytes))",
// similar to SSH fingerprint format.
func PublicKeyFingerprint(pub ed25519.PublicKey) string {
	hash := sha256.Sum256(pub)
	encoded := base64.StdEncoding.EncodeToString(hash[:])
	return fmt.Sprintf("SHA256:%s", encoded)
}

// loadIdentityKeys loads an Ed25519 private key from a PEM file and derives the public key.
func loadIdentityKeys(keyPath string) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	// Read PEM file
	pemData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read key file: %w", err)
	}

	// Decode PEM block
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, nil, fmt.Errorf("failed to decode PEM block")
	}

	if block.Type != "ED25519 PRIVATE KEY" {
		return nil, nil, fmt.Errorf("unexpected PEM block type: %s (expected ED25519 PRIVATE KEY)", block.Type)
	}

	// Parse PKCS8 private key
	privKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse PKCS8 private key: %w", err)
	}

	// Type assert to Ed25519 private key
	ed25519Priv, ok := privKey.(ed25519.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("private key is not Ed25519 (got %T)", privKey)
	}

	// Derive public key from private key
	pub := ed25519Priv.Public().(ed25519.PublicKey)

	return pub, ed25519Priv, nil
}

// saveIdentityKeys saves an Ed25519 private key to a PEM file with 0600 permissions.
func saveIdentityKeys(keyPath string, priv ed25519.PrivateKey) error {
	// Marshal private key to PKCS8 format
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal PKCS8 private key: %w", err)
	}

	// Encode as PEM
	pemBlock := &pem.Block{
		Type:  "ED25519 PRIVATE KEY",
		Bytes: pkcs8Bytes,
	}

	pemData := pem.EncodeToMemory(pemBlock)

	// Write to file with 0600 permissions
	if err := os.WriteFile(keyPath, pemData, 0600); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}

	return nil
}
