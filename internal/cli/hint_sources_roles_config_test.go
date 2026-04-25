package cli

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/context/roleconfig"
)

// TestContextPrime_EmitsRolesConfigMigrationHint exercises the slog-bridge
// integration: ContextPrime calls roleconfig.DriftStatus and emits any hints
// via slog.Warn, so installSlogBridge / SlogHintHandler can route them into
// either --json hints or stderr.
//
// Regression spec: thrum-z2et.20.3.
func TestContextPrime_EmitsRolesConfigMigrationHint(t *testing.T) {
	repo := t.TempDir()
	thrumDir := filepath.Join(repo, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "role_templates"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "role_templates", "coordinator.md"),
		[]byte("# coord\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// THRUM_HOME pins repo resolution to a specific checkout regardless of
	// cwd; clear it so this test runs against the temp repo.
	t.Setenv("THRUM_HOME", "")

	prevWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	_ = ContextPrime(nil)

	if !strings.Contains(buf.String(), HintRolesConfigMigration) {
		t.Errorf("expected migration hint code %q in slog output, got:\n%s",
			HintRolesConfigMigration, buf.String())
	}
}

// TestEmitRolesConfigDriftHints_RoutesAllThreeCodes pumps a synthetic drift
// report through the helper and asserts each emitted code shows up in the
// captured slog output. Locks the migration / schema-bump / body-diff codes
// to their wire strings so an accidental rename in roleconfig won't silently
// break the bridge.
func TestEmitRolesConfigDriftHints_RoutesAllThreeCodes(t *testing.T) {
	cases := []struct {
		name string
		hint roleconfig.DriftHint
		want string
	}{
		{"migration", roleconfig.DriftHint{Code: HintRolesConfigMigration, Message: "msg"}, HintRolesConfigMigration},
		{"schema-bump", roleconfig.DriftHint{Code: HintRolesConfigSchemaBump, Message: "msg"}, HintRolesConfigSchemaBump},
		{"body-diff", roleconfig.DriftHint{Code: HintRolesConfigBodyDiff, Message: "msg"}, HintRolesConfigBodyDiff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			EmitRolesConfigDriftHints(roleconfig.DriftReport{Hints: []roleconfig.DriftHint{tc.hint}})

			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("emitted slog output missing code %q:\n%s", tc.want, buf.String())
			}
		})
	}
}
