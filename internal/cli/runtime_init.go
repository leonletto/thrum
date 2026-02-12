package cli

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/*
var templateFS embed.FS

// TemplateData contains variables available in all runtime templates.
type TemplateData struct {
	AgentName   string `json:"agent_name"`
	AgentRole   string `json:"agent_role"`
	AgentModule string `json:"agent_module"`
	MCPCommand  string `json:"mcp_command"`
}

// RuntimeInitOptions contains options for generating runtime-specific configs.
type RuntimeInitOptions struct {
	RepoPath  string
	Runtime   string
	DryRun    bool
	Force     bool
	AgentName string
	AgentRole string
	AgentMod  string
}

// FileAction describes a file that would be created or overwritten.
type FileAction struct {
	Path      string `json:"path"`
	Action    string `json:"action"` // "create" or "overwrite"
	Runtime   string `json:"runtime"`
	Template  string `json:"template"`
	Skipped   bool   `json:"skipped,omitempty"`
	SkipReason string `json:"skip_reason,omitempty"`
}

// RuntimeInitResult contains the result of a runtime init operation.
type RuntimeInitResult struct {
	Runtime  string       `json:"runtime"`
	Detected bool         `json:"detected"`
	Files    []FileAction `json:"files"`
	DryRun   bool         `json:"dry_run"`
}

// runtimeTemplate maps a runtime to its template and output path.
type runtimeTemplate struct {
	tmplPath string // path within embedded FS
	outPath  string // output path relative to repo root
	mode     os.FileMode
}

// runtimeTemplates returns the template-to-output mappings for a given runtime.
func runtimeTemplates(runtime string) []runtimeTemplate {
	switch runtime {
	case "claude":
		return []runtimeTemplate{
			{"templates/claude/settings.json.tmpl", ".claude/settings.json", 0644},
			{"templates/shared/startup.sh.tmpl", "scripts/thrum-startup.sh", 0755},
		}
	case "codex":
		return []runtimeTemplate{
			{"templates/codex/session-start.sh.tmpl", ".codex/hooks/session-start", 0755},
			{"templates/codex/AGENTS.md.tmpl", "AGENTS.md", 0644},
			{"templates/shared/startup.sh.tmpl", "scripts/thrum-startup.sh", 0755},
		}
	case "cursor":
		return []runtimeTemplate{
			{"templates/cursor/cursorrules.tmpl", ".cursorrules", 0644},
			{"templates/shared/startup.sh.tmpl", "scripts/thrum-startup.sh", 0755},
		}
	case "gemini":
		return []runtimeTemplate{
			{"templates/gemini/instructions.md.tmpl", ".gemini/instructions.md", 0644},
			{"templates/gemini/settings.json.tmpl", ".gemini/settings.json", 0644},
			{"templates/shared/startup.sh.tmpl", "scripts/thrum-startup.sh", 0755},
		}
	case "auggie":
		return []runtimeTemplate{
			{"templates/auggie/settings.json.tmpl", ".augment/settings.json", 0644},
			{"templates/auggie/rules.md.tmpl", ".augment/rules/thrum.md", 0644},
			{"templates/shared/startup.sh.tmpl", "scripts/thrum-startup.sh", 0755},
		}
	case "cli-only":
		return []runtimeTemplate{
			{"templates/shared/startup.sh.tmpl", "scripts/thrum-startup.sh", 0755},
			{"templates/cli-only/polling-loop.sh.tmpl", "scripts/thrum-polling.sh", 0755},
		}
	default:
		return nil
	}
}

// RenderTemplate renders an embedded template with the given data.
func RenderTemplate(runtime, templateName string, data TemplateData) (string, error) {
	tmplPath := fmt.Sprintf("templates/%s/%s", runtime, templateName)
	return renderTemplatePath(tmplPath, data)
}

// renderTemplatePath renders an embedded template from its full path.
func renderTemplatePath(tmplPath string, data TemplateData) (string, error) {
	content, err := templateFS.ReadFile(tmplPath)
	if err != nil {
		return "", fmt.Errorf("template %q not found: %w", tmplPath, err)
	}

	tmpl, err := template.New(filepath.Base(tmplPath)).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", tmplPath, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %q: %w", tmplPath, err)
	}

	return buf.String(), nil
}

// RuntimeInit generates runtime-specific configuration files.
func RuntimeInit(opts RuntimeInitOptions) (*RuntimeInitResult, error) {
	data := TemplateData{
		AgentName:   opts.AgentName,
		AgentRole:   opts.AgentRole,
		AgentModule: opts.AgentMod,
		MCPCommand:  "thrum",
	}

	if data.AgentName == "" {
		data.AgentName = "default_agent"
	}
	if data.AgentRole == "" {
		data.AgentRole = "implementer"
	}
	if data.AgentModule == "" {
		data.AgentModule = "main"
	}

	// Get runtimes to process
	runtimes := []string{opts.Runtime}
	if opts.Runtime == "all" {
		runtimes = []string{"claude", "codex", "cursor", "gemini", "auggie", "cli-only"}
	}

	result := &RuntimeInitResult{
		Runtime: opts.Runtime,
		DryRun:  opts.DryRun,
	}

	for _, rt := range runtimes {
		tmpls := runtimeTemplates(rt)
		if tmpls == nil {
			return nil, fmt.Errorf("unknown runtime: %q", rt)
		}

		for _, tmpl := range tmpls {
			outPath := filepath.Join(opts.RepoPath, tmpl.outPath)
			action := FileAction{
				Path:     tmpl.outPath,
				Runtime:  rt,
				Template: tmpl.tmplPath,
			}

			// Check if file exists
			if _, err := os.Stat(outPath); err == nil {
				if !opts.Force {
					action.Action = "skip"
					action.Skipped = true
					action.SkipReason = "file exists (use --force to overwrite)"
					result.Files = append(result.Files, action)
					continue
				}
				action.Action = "overwrite"
			} else {
				action.Action = "create"
			}

			if !opts.DryRun {
				// Render template
				rendered, err := renderTemplatePath(tmpl.tmplPath, data)
				if err != nil {
					return nil, fmt.Errorf("render %s: %w", tmpl.tmplPath, err)
				}

				// Create parent directory
				if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
					return nil, fmt.Errorf("mkdir for %s: %w", outPath, err)
				}

				// Write file
				if err := os.WriteFile(outPath, []byte(rendered), tmpl.mode); err != nil {
					return nil, fmt.Errorf("write %s: %w", outPath, err)
				}
			}

			result.Files = append(result.Files, action)
		}
	}

	return result, nil
}

// FormatRuntimeInit formats the runtime init result for human-readable display.
func FormatRuntimeInit(result *RuntimeInitResult) string {
	var out strings.Builder

	if result.DryRun {
		fmt.Fprintf(&out, "Dry run for runtime: %s\n\n", result.Runtime)
		out.WriteString("Would create/update:\n")
	} else {
		fmt.Fprintf(&out, "Runtime: %s\n\n", result.Runtime)
	}

	for _, f := range result.Files {
		switch {
		case f.Skipped:
			fmt.Fprintf(&out, "  SKIP %s (%s)\n", f.Path, f.SkipReason)
		case result.DryRun:
			fmt.Fprintf(&out, "  %s %s\n", f.Action, f.Path)
		default:
			fmt.Fprintf(&out, "  ✓ %s\n", f.Path)
		}
	}

	if !result.DryRun {
		out.WriteString("\nDone. Run 'thrum daemon start' to begin coordination.\n")
	}

	return out.String()
}
