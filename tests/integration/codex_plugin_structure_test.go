//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRootForCodex returns the repository root by walking up from this source
// file's location (tests/integration/ → repo root).
func repoRootForCodex(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// thisFile is .../tests/integration/codex_plugin_structure_test.go
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// TestCodexPluginStructureMarketplacePath asserts that marketplace.json has
// the correct source.path for the "thrum" plugin entry.
//
// Codex 0.130.0 silently rejects paths like ".", "./.", "./" or empty strings
// with the error "local plugin source path must not be empty". Only
// "./plugins/thrum" is known-good.
func TestCodexPluginStructureMarketplacePath(t *testing.T) {
	repoRoot := repoRootForCodex(t)
	marketplacePath := filepath.Join(repoRoot, "codex-plugin", ".agents", "plugins", "marketplace.json")

	data, err := os.ReadFile(marketplacePath)
	if err != nil {
		t.Fatalf("cannot read marketplace.json: %v", err)
	}

	var manifest struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Source string `json:"source"`
				Path   string `json:"path"`
			} `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("cannot parse marketplace.json: %v", err)
	}

	found := false
	for _, p := range manifest.Plugins {
		if p.Name != "thrum" {
			continue
		}
		found = true
		if p.Source.Source != "local" {
			t.Errorf("thrum plugin source.source = %q, want %q", p.Source.Source, "local")
		}
		if p.Source.Path != "./plugins/thrum" {
			t.Errorf("thrum plugin source.path = %q, want %q (codex 0.130.0 rejects other values)", p.Source.Path, "./plugins/thrum")
		}
	}
	if !found {
		t.Errorf("no plugin entry with name == %q found in marketplace.json", "thrum")
	}
}

// TestCodexPluginStructureHooksEnvVar asserts that hooks.json uses ${PLUGIN_ROOT}
// (the env var codex 0.130.0 actually expands inside hook command paths) and
// never ${CODEX_PLUGIN_ROOT} or ${CLAUDE_*} variants.
//
// Empirical: codex 0.130.0 expands ${PLUGIN_ROOT} (and ${CLAUDE_PLUGIN_ROOT}
// for compatibility) but does NOT recognize ${CODEX_PLUGIN_ROOT}. The fix
// landed in commit 391cf57c9c after a post-merge live-codex probe found the
// hooks silently failing to resolve when the spec template's
// ${CODEX_PLUGIN_ROOT} wasn't expanded. ${CLAUDE_PROJECT_DIR} is also
// forbidden — it's the wrong scope (project, not plugin).
func TestCodexPluginStructureHooksEnvVar(t *testing.T) {
	repoRoot := repoRootForCodex(t)
	hooksPath := filepath.Join(repoRoot, "codex-plugin", "plugins", "thrum", "hooks", "hooks.json")

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("cannot read hooks.json: %v", err)
	}

	// Parse into a generic structure so we can walk all string values.
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("cannot parse hooks.json: %v", err)
	}

	forbidden := []string{
		"${CODEX_PLUGIN_ROOT}",
		"${CLAUDE_PROJECT_DIR}",
	}

	var walkStrings func(v interface{}, path string)
	walkStrings = func(v interface{}, path string) {
		switch val := v.(type) {
		case string:
			if !strings.Contains(val, "${") {
				return
			}
			for _, bad := range forbidden {
				if strings.Contains(val, bad) {
					t.Errorf("hooks.json[%s] contains forbidden env var %q (value: %q); use ${PLUGIN_ROOT} instead — codex 0.130.0 does not expand %s", path, bad, val, bad)
				}
			}
		case map[string]interface{}:
			for k, child := range val {
				walkStrings(child, path+"."+k)
			}
		case []interface{}:
			for i, child := range val {
				walkStrings(child, path+"."+strings.TrimLeft(string(rune('0'+i)), ""))
			}
		}
	}
	walkStrings(raw, "")
}

// TestCodexPluginStructureDefaultPromptLimits asserts that plugin.json's
// interface.defaultPrompt has at most 3 entries and each entry is at most 128
// characters.
//
// Codex 0.130.0 logs a WARN and silently drops extras when there are >3
// prompts; it also rejects prompts ≥128 chars.
func TestCodexPluginStructureDefaultPromptLimits(t *testing.T) {
	repoRoot := repoRootForCodex(t)
	pluginJSONPath := filepath.Join(repoRoot, "codex-plugin", "plugins", "thrum", ".codex-plugin", "plugin.json")

	data, err := os.ReadFile(pluginJSONPath)
	if err != nil {
		t.Fatalf("cannot read plugin.json: %v", err)
	}

	var manifest struct {
		Interface struct {
			DefaultPrompt []string `json:"defaultPrompt"`
		} `json:"interface"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("cannot parse plugin.json: %v", err)
	}

	prompts := manifest.Interface.DefaultPrompt
	if len(prompts) == 0 {
		// Field is absent or empty — nothing to check.
		return
	}

	const maxPrompts = 3
	const maxLen = 128

	if len(prompts) > maxPrompts {
		t.Errorf("interface.defaultPrompt has %d entries, want ≤%d (codex 0.130.0 silently drops extras)", len(prompts), maxPrompts)
	}
	for i, p := range prompts {
		if len(p) > maxLen {
			t.Errorf("interface.defaultPrompt[%d] is %d chars, want ≤%d: %q", i, len(p), maxLen, p)
		}
	}
}

// TestCodexPluginStructureNoClaudeSyntaxLeak asserts that skill files under
// the codex plugin do not contain claude-specific syntax that would have been
// left behind after a sync from claude-plugin.
//
// Forbidden substrings / patterns:
//   - ${CLAUDE_PLUGIN_ROOT}  — claude env var
//   - ${CLAUDE_PROJECT_DIR}  — claude env var
//   - /thrum:<word>          — claude slash-skill syntax (codex uses $thrum-foo)
func TestCodexPluginStructureNoClaudeSyntaxLeak(t *testing.T) {
	repoRoot := repoRootForCodex(t)

	skillsDirs := []string{
		filepath.Join(repoRoot, "codex-plugin", "skills"),
		filepath.Join(repoRoot, "codex-plugin", "plugins", "thrum", "skills"),
	}

	type forbiddenCheck struct {
		label  string
		testFn func(content string) bool
	}
	checks := []forbiddenCheck{
		{
			label:  "${CLAUDE_PLUGIN_ROOT}",
			testFn: func(s string) bool { return strings.Contains(s, "${CLAUDE_PLUGIN_ROOT}") },
		},
		{
			label:  "${CLAUDE_PROJECT_DIR}",
			testFn: func(s string) bool { return strings.Contains(s, "${CLAUDE_PROJECT_DIR}") },
		},
		{
			// Claude slash-skill syntax: /thrum:word (not preceded by word char)
			// We detect the simpler fixed prefix /thrum: followed by a letter/digit/_.
			label: "/thrum:<skill> slash-skill syntax",
			testFn: func(s string) bool {
				idx := strings.Index(s, "/thrum:")
				if idx < 0 {
					return false
				}
				after := s[idx+len("/thrum:"):]
				if len(after) == 0 {
					return false
				}
				c := after[0]
				return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
			},
		},
	}

	for _, dir := range skillsDirs {
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			t.Logf("skills directory does not exist, skipping: %s", dir)
			continue
		}

		walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}

			content, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Errorf("cannot read %s: %v", path, readErr)
				return nil
			}
			text := string(content)
			rel, _ := filepath.Rel(repoRoot, path)

			for _, check := range checks {
				if check.testFn(text) {
					t.Errorf("%s: contains forbidden claude-syntax %q (sync-skills.sh should have substituted this)", rel, check.label)
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Errorf("walking %s: %v", dir, walkErr)
		}
	}
}

// TestCodexPluginStructureInstallMDNoRemovedSubcommands asserts that INSTALL.md
// does not reference removed codex subcommands.
//
// `codex plugin marketplace list` was removed in codex 0.130.0; only
// add/upgrade/remove/help remain. Referencing the removed subcommand causes
// user confusion.
func TestCodexPluginStructureInstallMDNoRemovedSubcommands(t *testing.T) {
	repoRoot := repoRootForCodex(t)
	installMDPath := filepath.Join(repoRoot, "codex-plugin", "plugins", "thrum", "INSTALL.md")

	data, err := os.ReadFile(installMDPath)
	if err != nil {
		t.Fatalf("cannot read INSTALL.md: %v", err)
	}
	content := string(data)

	forbidden := "codex plugin marketplace list"
	if strings.Contains(content, forbidden) {
		t.Errorf("INSTALL.md contains %q — this subcommand was removed in codex 0.130.0 (only add/upgrade/remove/help remain)", forbidden)
	}
}
