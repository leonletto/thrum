package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeEnvFile is a test helper that writes content to path and registers
// cleanup so the file is removed when the test ends.
func writeEnvFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeEnvFile(%q): %v", path, err)
	}
	t.Cleanup(func() { os.Remove(path) })
}

// TestLoadEnvFile_BasicLoad verifies that variables from a .env file are
// loaded when the corresponding env vars are not already set.
func TestLoadEnvFile_BasicLoad(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	writeEnvFile(t, envPath, "THRUM_TS_HOSTNAME=from-file\nTHRUM_TS_ENABLED=true\n")

	// Ensure these are clear before the test.
	t.Setenv("THRUM_TS_HOSTNAME", "")
	t.Setenv("THRUM_TS_ENABLED", "")

	loadEnvFile(envPath)

	if got := os.Getenv("THRUM_TS_HOSTNAME"); got != "from-file" {
		t.Errorf("THRUM_TS_HOSTNAME = %q, want %q", got, "from-file")
	}
	if got := os.Getenv("THRUM_TS_ENABLED"); got != "true" {
		t.Errorf("THRUM_TS_ENABLED = %q, want %q", got, "true")
	}
}

// TestLoadEnvFile_EnvPrecedence verifies that an existing env var is NOT
// overwritten by a value in the .env file.
func TestLoadEnvFile_EnvPrecedence(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	writeEnvFile(t, envPath, "THRUM_TS_HOSTNAME=from-file\n")

	t.Setenv("THRUM_TS_HOSTNAME", "from-env")

	loadEnvFile(envPath)

	if got := os.Getenv("THRUM_TS_HOSTNAME"); got != "from-env" {
		t.Errorf("THRUM_TS_HOSTNAME = %q, want %q (env should win)", got, "from-env")
	}
}

// TestLoadEnvFile_CommentsAndBlanks verifies that comment lines (# …) and
// blank lines are silently skipped.
func TestLoadEnvFile_CommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	writeEnvFile(t, envPath, `# This is a comment
THRUM_TS_HOSTNAME=real-value

# Another comment
`)
	t.Setenv("THRUM_TS_HOSTNAME", "")

	loadEnvFile(envPath)

	if got := os.Getenv("THRUM_TS_HOSTNAME"); got != "real-value" {
		t.Errorf("THRUM_TS_HOSTNAME = %q, want %q", got, "real-value")
	}
}

// TestLoadEnvFile_NonThrumVarsIgnored verifies that variables without the
// THRUM_ or TAILSCALE_ prefix are not loaded into the environment.
func TestLoadEnvFile_NonThrumVarsIgnored(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	writeEnvFile(t, envPath, "DATABASE_URL=postgres://localhost/mydb\nSECRET_KEY=hunter2\n")

	// Make sure they are not set beforehand.
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("SECRET_KEY")
	t.Cleanup(func() {
		os.Unsetenv("DATABASE_URL")
		os.Unsetenv("SECRET_KEY")
	})

	loadEnvFile(envPath)

	if got := os.Getenv("DATABASE_URL"); got != "" {
		t.Errorf("DATABASE_URL should not have been set, got %q", got)
	}
	if got := os.Getenv("SECRET_KEY"); got != "" {
		t.Errorf("SECRET_KEY should not have been set, got %q", got)
	}
}

// TestLoadEnvFile_TailscalePrefixAllowed verifies that TAILSCALE_ prefixed
// variables (e.g., for custom control URLs) are also loaded.
func TestLoadEnvFile_TailscalePrefixAllowed(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	writeEnvFile(t, envPath, "TAILSCALE_SOME_VAR=hello\n")

	t.Setenv("TAILSCALE_SOME_VAR", "")
	t.Cleanup(func() { os.Unsetenv("TAILSCALE_SOME_VAR") })

	loadEnvFile(envPath)

	if got := os.Getenv("TAILSCALE_SOME_VAR"); got != "hello" {
		t.Errorf("TAILSCALE_SOME_VAR = %q, want %q", got, "hello")
	}
}

// TestLoadEnvFile_MissingFileIgnored verifies that a non-existent path is
// silently skipped without error.
func TestLoadEnvFile_MissingFileIgnored(t *testing.T) {
	// Should not panic or produce any visible error.
	loadEnvFile("/nonexistent/path/.env")
}

// TestLoadEnvFile_ThrumDirPriority verifies that when LoadTailscaleConfig is
// given a thrumDir, the .thrum/.env file takes priority over the repo root
// .env file.
func TestLoadEnvFile_ThrumDirPriority(t *testing.T) {
	// Set up a temporary directory tree:
	//   repoRoot/
	//     .env              ← repo root .env (lower priority)
	//     .thrum/
	//       .env            ← thrumDir .env (higher priority)
	repoRoot := t.TempDir()
	thrumDir := filepath.Join(repoRoot, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}

	writeEnvFile(t, filepath.Join(thrumDir, ".env"), "THRUM_TS_HOSTNAME=from-thrum-dir\n")
	writeEnvFile(t, filepath.Join(repoRoot, ".env"), "THRUM_TS_HOSTNAME=from-repo-root\n")

	// Ensure the env var is clear so both files compete.
	t.Setenv("THRUM_TS_HOSTNAME", "")

	loadEnvFile(
		filepath.Join(thrumDir, ".env"),
		filepath.Join(repoRoot, ".env"),
	)

	if got := os.Getenv("THRUM_TS_HOSTNAME"); got != "from-thrum-dir" {
		t.Errorf("THRUM_TS_HOSTNAME = %q, want %q (.thrum/.env should win)", got, "from-thrum-dir")
	}
}

// TestLoadTailscaleConfig_FromEnvFile exercises the full LoadTailscaleConfig
// path: values come from a .thrum/.env file rather than from process env vars.
func TestLoadTailscaleConfig_FromEnvFile(t *testing.T) {
	repoRoot := t.TempDir()
	thrumDir := filepath.Join(repoRoot, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}

	writeEnvFile(t, filepath.Join(thrumDir, ".env"), `THRUM_TS_ENABLED=true
THRUM_TS_HOSTNAME=daemon-from-file
THRUM_TS_AUTHKEY=tskey-file-abc
`)

	// Clear env vars so file is the sole source.
	for _, k := range []string{
		"THRUM_TS_ENABLED", "THRUM_TS_HOSTNAME", "THRUM_TS_AUTHKEY",
		"THRUM_TS_PORT", "THRUM_TS_STATE_DIR", "THRUM_TAILSCALE_CONTROL_URL",
	} {
		t.Setenv(k, "")
	}

	cfg := LoadTailscaleConfig(thrumDir)

	if !cfg.Enabled {
		t.Error("expected Enabled=true from .env file")
	}
	if cfg.Hostname != "daemon-from-file" {
		t.Errorf("Hostname = %q, want %q", cfg.Hostname, "daemon-from-file")
	}
	if cfg.AuthKey != "tskey-file-abc" {
		t.Errorf("AuthKey = %q, want %q", cfg.AuthKey, "tskey-file-abc")
	}
}
