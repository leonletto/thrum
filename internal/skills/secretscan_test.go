package skills

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scanFixtureDir writes a single SKILL.md with the given body in a
// fresh tempdir and returns the dir path. Convenience for the
// per-pattern detection tests.
func scanFixtureDir(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

func TestSecretScan_AWSAccessKeyDetected(t *testing.T) {
	t.Parallel()
	// AWS-documented test value — NOT a real key.
	dir := scanFixtureDir(t, "# header\nAKIAIOSFODNN7EXAMPLE in body\n")
	findings, err := NewScanner().Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 || findings[0].PatternCategory != "AWSAccessKey" {
		t.Errorf("expected one AWSAccessKey finding, got %+v", findings)
	}
}

func TestSecretScan_StripeLiveKeyDetected(t *testing.T) {
	t.Parallel()
	dir := scanFixtureDir(t, "Charge with sk_live_FAKEKEY123456789012345678 here\n")
	findings, _ := NewScanner().Scan(dir)
	if len(findings) != 1 || findings[0].PatternCategory != "StripeLiveKey" {
		t.Errorf("expected one StripeLiveKey finding, got %+v", findings)
	}
}

func TestSecretScan_GitHubTokenDetected(t *testing.T) {
	t.Parallel()
	dir := scanFixtureDir(t, "token: ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE\n")
	findings, _ := NewScanner().Scan(dir)
	if len(findings) != 1 || findings[0].PatternCategory != "GitHubToken" {
		t.Errorf("expected one GitHubToken finding, got %+v", findings)
	}
}

func TestSecretScan_PrivateKeyPEMDetected(t *testing.T) {
	t.Parallel()
	dir := scanFixtureDir(t, "-----BEGIN RSA PRIVATE KEY-----\nFAKE BASE64 BODY\n-----END RSA PRIVATE KEY-----\n")
	findings, _ := NewScanner().Scan(dir)
	hit := false
	for _, f := range findings {
		if f.PatternCategory == "PrivateKeyPEM" {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected PrivateKeyPEM finding, got %+v", findings)
	}
}

func TestSecretScan_GenericAPIKeyDetected(t *testing.T) {
	t.Parallel()
	dir := scanFixtureDir(t, "api_key=\"abcDEF1234567890zzzz\"\n")
	findings, _ := NewScanner().Scan(dir)
	hit := false
	for _, f := range findings {
		if f.PatternCategory == "GenericAPIKey" {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected GenericAPIKey finding, got %+v", findings)
	}
}

func TestSecretScan_BearerTokenDetected(t *testing.T) {
	t.Parallel()
	dir := scanFixtureDir(t, "Authorization: Bearer FAKEFAKEFAKEFAKEFAKEFAKE\n")
	findings, _ := NewScanner().Scan(dir)
	hit := false
	for _, f := range findings {
		if f.PatternCategory == "GenericBearer" {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected GenericBearer finding, got %+v", findings)
	}
}

func TestSecretScan_SlackTokenDetected(t *testing.T) {
	t.Parallel()
	dir := scanFixtureDir(t, "slack_token=xoxb-1234567890-FAKEFAKEFAKE\n")
	findings, _ := NewScanner().Scan(dir)
	hit := false
	for _, f := range findings {
		if f.PatternCategory == "SlackToken" {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected SlackToken finding, got %+v", findings)
	}
}

func TestSecretScan_NoFindingsOnCleanFile(t *testing.T) {
	t.Parallel()
	dir := scanFixtureDir(t, `---
name: clean
description: nothing secret here
---

# clean

Pure prose body, no secrets. Just regular operator-facing text.
`)
	findings, err := NewScanner().Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings, got %+v", findings)
	}
}

func TestSecretScan_OverrideFiltersOut(t *testing.T) {
	t.Parallel()
	dir := scanFixtureDir(t, "AKIAIOSFODNN7EXAMPLE in fixture\n")
	overrides := []AllowedPattern{
		{Pattern: `AKIAIOSFODNN7EXAMPLE`, Reason: "AWS-documented test value"}, //nolint:gosec // G101: AWS-documented test value, not a real credential
	}
	active, suppressed, err := NewScanner().ScanWithOverrides(dir, overrides)
	if err != nil {
		t.Fatalf("ScanWithOverrides: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("expected 0 active findings post-override, got %+v", active)
	}
	if len(suppressed) != 1 {
		t.Errorf("expected 1 suppressed finding, got %+v", suppressed)
	}
}

func TestSecretScan_FindingsSorted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Two files, two findings each — assert sort order.
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("AKIAIOSFODNN7EXAMPLE\nghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE\n"), 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("sk_live_FAKEKEY123456789012345678\n"), 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}

	findings, err := NewScanner().Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 3 {
		t.Fatalf("expected 3 findings, got %d: %+v", len(findings), findings)
	}
	// a.md comes before b.md alphabetically.
	if !strings.HasSuffix(findings[0].Path, "a.md") {
		t.Errorf("findings[0].Path = %q (expected a.md first)", findings[0].Path)
	}
	if findings[0].Line > findings[1].Line {
		t.Errorf("Line ordering broken: %d > %d", findings[0].Line, findings[1].Line)
	}
	if !strings.HasSuffix(findings[2].Path, "b.md") {
		t.Errorf("findings[2].Path = %q (expected b.md last)", findings[2].Path)
	}
}

func TestSecretScan_NeverLogsMatchedString(t *testing.T) {
	t.Parallel()

	// Capture every log record the scanner could possibly emit.
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	// Run a scan with a finding so the scanner has something to emit.
	const secretFixture = "AKIAIOSFODNN7EXAMPLE" //nolint:gosec // G101: AWS-documented test value
	dir := scanFixtureDir(t, secretFixture+"\n")
	findings, err := NewScanner().Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("setup: expected 1 finding, got %d", len(findings))
	}

	// Privacy invariant: the captured log output must NEVER contain
	// the matched substring. Pattern category + path + line is OK;
	// the secret itself must stay inside the scanner.
	logs := buf.String()
	if strings.Contains(logs, secretFixture) {
		t.Errorf("PRIVACY LEAK: matched secret %q surfaced in slog output:\n%s", secretFixture, logs)
	}
}

func TestSecretScan_AllEightPatternsCovered(t *testing.T) {
	t.Parallel()

	// One fixture line per pattern category. AWSSecret regex requires
	// the exact assignment shape; rest match looser.
	body := strings.Join([]string{
		"AKIAIOSFODNN7EXAMPLE",
		`aws_secret_access_key = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`,
		"sk_live_FAKEKEY123456789012345678",
		"ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE",
		"Authorization: Bearer FAKEFAKEFAKEFAKEFAKEFAKE",
		`api_key="abcDEF1234567890zzzz"`,
		"-----BEGIN RSA PRIVATE KEY-----",
		"xoxb-1234567890-FAKE",
	}, "\n") + "\n"
	dir := scanFixtureDir(t, body)

	findings, err := NewScanner().Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	seen := map[string]bool{}
	for _, f := range findings {
		seen[f.PatternCategory] = true
	}
	for _, want := range []string{
		"AWSAccessKey",
		"AWSSecret",
		"StripeLiveKey",
		"GitHubToken",
		"GenericBearer",
		"GenericAPIKey",
		"PrivateKeyPEM",
		"SlackToken",
	} {
		if !seen[want] {
			t.Errorf("pattern category %q not detected; findings:\n  %+v", want, findings)
		}
	}
}
