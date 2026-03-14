package runtime

import "testing"

func TestBuiltinAgents_AllHaveRequiredFields(t *testing.T) {
	agents := BuiltinAgents()
	if len(agents) < 6 {
		t.Fatalf("expected at least 6 built-in agents, got %d", len(agents))
	}
	for _, a := range agents {
		if a.Name == "" {
			t.Error("agent has empty Name")
		}
		if a.DisplayName == "" {
			t.Errorf("agent %q has empty DisplayName", a.Name)
		}
		if a.SkillsDir == "" {
			t.Errorf("agent %q has empty SkillsDir", a.Name)
		}
		// Every agent must have at least one detection signal
		if len(a.RepoMarkers) == 0 && len(a.EnvVars) == 0 && len(a.Binaries) == 0 {
			t.Errorf("agent %q has no detection signals", a.Name)
		}
	}
}

func TestBuiltinAgents_NamesAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, a := range BuiltinAgents() {
		if seen[a.Name] {
			t.Errorf("duplicate agent name: %q", a.Name)
		}
		seen[a.Name] = true
	}
}

func TestGetAgent_BuiltinFound(t *testing.T) {
	a, ok := GetAgent("claude")
	if !ok {
		t.Fatal("expected to find claude agent")
	}
	if a.DisplayName != "Claude Code" {
		t.Errorf("expected DisplayName 'Claude Code', got %q", a.DisplayName)
	}
}

func TestGetAgent_NotFound(t *testing.T) {
	_, ok := GetAgent("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent agent")
	}
}
