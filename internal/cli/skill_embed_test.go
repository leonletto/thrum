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
	data, err := SkillFS.ReadFile("skill/thrum/SKILL.md")
	if err != nil {
		t.Fatalf("SKILL.md not embedded: %v", err)
	}
	content := strings.ToLower(string(data))
	for _, forbidden := range []string{
		"claude-plugin",
		"plugin.json",
		"sessionstart",
		"precompact",
		"allowed-tools",
		".claude/",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("SKILL.md contains agent-specific reference: %q", forbidden)
		}
	}
}
