package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/context/roleconfig"
)

func TestRolesSaveConfig_ValidJSON(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	input := `{
  "schema_version": 1,
  "plugin_version": "0.9.2",
  "configured_at": "2026-04-25T15:00:00Z",
  "roles": {
    "coordinator": {"autonomy": "autonomous", "scope": "cross_worktree"}
  }
}`
	if err := runRolesSaveConfig(thrumDir, strings.NewReader(input)); err != nil {
		t.Fatalf("save-config: %v", err)
	}

	cfg, err := roleconfig.Load(thrumDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned nil after save")
	}
	if cfg.Roles["coordinator"].Autonomy != "autonomous" {
		t.Errorf("config not saved correctly: %+v", cfg)
	}
}

func TestRolesSaveConfig_RejectsInvalidJSON(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runRolesSaveConfig(thrumDir, strings.NewReader(`{not valid json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestRolesSaveConfig_RejectsUnknownFields(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runRolesSaveConfig(thrumDir, strings.NewReader(`{
  "schema_version": 1,
  "roles": {},
  "bogus_field": "nope"
}`))
	if err == nil {
		t.Error("expected error for unknown field")
	}
}

// TestRolesSaveConfig_FillsRenderedHashes asserts that save-config backfills
// rendered_hash from the current shipped body_hash for every role with a
// valid (role,autonomy) variant. Without this, drift detection would fire
// body-diff hints immediately after configure-roles ran.
func TestRolesSaveConfig_FillsRenderedHashes(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	input := `{
  "schema_version": 1,
  "plugin_version": "0.9.2",
  "configured_at": "2026-04-25T15:00:00Z",
  "roles": {"coordinator": {"autonomy": "autonomous", "scope": "cross_worktree"}}
}`
	if err := runRolesSaveConfig(thrumDir, strings.NewReader(input)); err != nil {
		t.Fatalf("save-config: %v", err)
	}

	cfg, err := roleconfig.Load(thrumDir)
	if err != nil {
		t.Fatal(err)
	}
	_, expected, err := roleconfig.ShippedTemplateInfo("coordinator", "autonomous")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Roles["coordinator"].RenderedHash != expected {
		t.Errorf("rendered_hash not backfilled: got %q, want %q",
			cfg.Roles["coordinator"].RenderedHash, expected)
	}
}

// TestRolesTemplatesPrint covers the rolesTemplatesPrintCmd CLI shim used
// by /thrum:configure-roles. Asserts that the multi-variant lookup
// (coordinator-autonomous) and the single-variant fallback (orchestrator)
// both write non-empty embedded content to the command's stdout.
func TestRolesTemplatesPrint(t *testing.T) {
	cases := []struct {
		name string
		arg  string
	}{
		{"multi-variant", "coordinator-autonomous"},
		{"single-variant fallback", "orchestrator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := rolesTemplatesPrintCmd()

			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			origStdout := os.Stdout
			os.Stdout = w
			t.Cleanup(func() { os.Stdout = origStdout })

			cmd.SetArgs([]string{tc.arg})
			runErr := cmd.Execute()
			if cerr := w.Close(); cerr != nil {
				t.Fatal(cerr)
			}
			if runErr != nil {
				t.Fatalf("Execute(%q): %v", tc.arg, runErr)
			}

			out, readErr := io.ReadAll(r)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(out) == 0 {
				t.Fatalf("expected non-empty embedded template content for %q", tc.arg)
			}
			if !bytes.Contains(out, []byte("schema_version: 1")) {
				t.Errorf("printed template missing frontmatter for %q:\n%s", tc.arg, out[:min(120, len(out))])
			}
		})
	}
}

// TestRolesSaveConfig_FillsDefaults asserts that when the JSON omits
// schema_version / plugin_version / configured_at, save-config injects
// CurrentSchemaVersion / Version / time.Now() so partially specified inputs
// still produce a complete role_config record.
func TestRolesSaveConfig_FillsDefaults(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	input := `{"roles": {"implementer": {"autonomy": "strict", "scope": "single_worktree"}}}`
	if err := runRolesSaveConfig(thrumDir, strings.NewReader(input)); err != nil {
		t.Fatalf("save-config: %v", err)
	}

	cfg, err := roleconfig.Load(thrumDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != roleconfig.CurrentSchemaVersion {
		t.Errorf("schema_version default not applied: got %d", cfg.SchemaVersion)
	}
	if cfg.PluginVersion == "" {
		t.Errorf("plugin_version default not applied")
	}
	if cfg.ConfiguredAt.IsZero() {
		t.Errorf("configured_at default not applied")
	}
}
