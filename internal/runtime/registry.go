package runtime

// AgentDefinition describes a supported AI coding agent's detection
// signals and skill installation preferences.
type AgentDefinition struct {
	Name        string        `json:"name"`
	DisplayName string        `json:"display_name"`
	RepoMarkers []string      `json:"repo_markers,omitempty"`
	EnvVars     []string      `json:"env_vars,omitempty"`
	Binaries    []BinaryCheck `json:"binaries,omitempty"`
	SkillsDir   string        `json:"skills_dir"`
	SkillFormat string        `json:"skill_format,omitempty"` // "standard" (default) or "gemini-extension"
}

// BinaryCheck describes how to find and verify an agent binary on PATH.
type BinaryCheck struct {
	Name       string   `json:"name"`
	VerifyArgs []string `json:"verify_args"`
	MatchAny   []string `json:"match_any"`
	Timeout    int      `json:"timeout,omitempty"` // ms, default 3000
}

// builtinAgents is the canonical list of supported agents.
var builtinAgents = []AgentDefinition{
	{
		Name:        "claude",
		DisplayName: "Claude Code",
		RepoMarkers: []string{".claude/settings.json", ".claude/"},
		EnvVars:     []string{"CLAUDE_SESSION_ID"},
		Binaries:    []BinaryCheck{{Name: "claude", VerifyArgs: []string{"--version"}, MatchAny: []string{"claude"}}},
		SkillsDir:   ".claude/skills",
	},
	{
		Name:        "cursor",
		DisplayName: "Cursor Agent",
		RepoMarkers: []string{".cursor/rules/", ".cursor/"},
		EnvVars:     []string{"CURSOR_SESSION"},
		Binaries: []BinaryCheck{
			{Name: "cursor-agent", VerifyArgs: []string{"--version"}, MatchAny: []string{"cursor"}},
			{Name: "agent", VerifyArgs: []string{"--version"}, MatchAny: []string{"cursor"}},
		},
		SkillsDir: ".cursor/skills",
	},
	{
		Name:        "codex",
		DisplayName: "OpenAI Codex",
		RepoMarkers: []string{".codex/"},
		Binaries:    []BinaryCheck{{Name: "codex", VerifyArgs: []string{"--version"}, MatchAny: []string{"openai", "codex"}}},
		SkillsDir:   ".codex/skills",
	},
	{
		Name:        "gemini",
		DisplayName: "Gemini CLI",
		RepoMarkers: []string{".gemini/"},
		EnvVars:     []string{"GEMINI_CLI"},
		Binaries:    []BinaryCheck{{Name: "gemini", VerifyArgs: []string{"--version"}, MatchAny: []string{"gemini", "google"}}},
		SkillsDir:   ".gemini/skills",
		SkillFormat: "gemini-extension",
	},
	{
		Name:        "auggie",
		DisplayName: "Augment Code",
		RepoMarkers: []string{".augment/"},
		EnvVars:     []string{"AUGMENT_AGENT"},
		Binaries:    []BinaryCheck{{Name: "auggie", VerifyArgs: []string{"--version"}, MatchAny: []string{"augment"}}},
		SkillsDir:   ".augment/skills",
	},
	{
		Name:        "amp",
		DisplayName: "Sourcegraph Amp",
		Binaries:    []BinaryCheck{{Name: "amp", VerifyArgs: []string{"--version"}, MatchAny: []string{"sourcegraph"}}},
		SkillsDir:   ".agents/skills",
	},
}

// BuiltinAgents returns a copy of the built-in agent definitions.
func BuiltinAgents() []AgentDefinition {
	result := make([]AgentDefinition, len(builtinAgents))
	copy(result, builtinAgents)
	return result
}

// GetAgent returns the agent definition for the given name.
func GetAgent(name string) (AgentDefinition, bool) {
	for _, a := range builtinAgents {
		if a.Name == name {
			return a, true
		}
	}
	return AgentDefinition{}, false
}
