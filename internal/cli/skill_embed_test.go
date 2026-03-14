package cli

import (
	"strings"
	"testing"
)

func TestSkillFS_ContainsSKILLMD(t *testing.T) {
	data, err := SkillFS.ReadFile("skill/thrum/SKILL.md")
	if err != nil {
		t.Fatalf("SKILL.md not embedded: %v", err)
	}
	if !strings.Contains(string(data), "name: thrum") {
		t.Error("SKILL.md missing expected frontmatter")
	}
}

func TestSkillFS_ContainsReferences(t *testing.T) {
	for _, name := range []string{
		"skill/thrum/references/CLI_REFERENCE.md",
		"skill/thrum/references/MESSAGING.md",
		"skill/thrum/references/LISTENER_PATTERN.md",
	} {
		if _, err := SkillFS.ReadFile(name); err != nil {
			t.Errorf("reference file not embedded: %s: %v", name, err)
		}
	}
}

func TestSkillFS_AgentAgnostic(t *testing.T) {
	forbidden := []string{
		"claude-plugin",
		"plugin.json",
		"sessionstart",
		"precompact",
		"allowed-tools",
		".claude/",
	}

	files := []string{
		"skill/thrum/SKILL.md",
		"skill/thrum/references/CLI_REFERENCE.md",
		"skill/thrum/references/MESSAGING.md",
		"skill/thrum/references/LISTENER_PATTERN.md",
	}

	for _, file := range files {
		data, err := SkillFS.ReadFile(file)
		if err != nil {
			t.Fatalf("%s not embedded: %v", file, err)
		}
		content := strings.ToLower(string(data))
		for _, term := range forbidden {
			if strings.Contains(content, term) {
				t.Errorf("%s contains agent-specific reference: %q", file, term)
			}
		}
	}
}
