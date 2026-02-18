package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeMdOptions contains options for generating CLAUDE.md content.
type ClaudeMdOptions struct {
	RepoPath string
	Apply    bool   // Append to CLAUDE.md
	Force    bool   // Overwrite existing section
	Runtime  string // Optional override (auto-detected if empty)
}

// ClaudeMdResult contains the result of a CLAUDE.md generation.
type ClaudeMdResult struct {
	Content    string // Rendered CLAUDE.md content
	Applied    bool   // Whether it was written to disk
	FilePath   string // Path to CLAUDE.md (if applied)
	Skipped    bool   // True if section already exists and !Force
	SkipReason string
}

// claudeMdHeader is the section marker used for duplicate detection.
const claudeMdHeader = "# Thrum Agent Coordination"

// GenerateClaudeMd renders the CLAUDE.md template and optionally applies it.
func GenerateClaudeMd(opts ClaudeMdOptions) (*ClaudeMdResult, error) {
	data := TemplateData{
		AgentRole:   "implementer",
		AgentModule: "main",
		MCPCommand:  "thrum",
	}

	content, err := RenderTemplate("shared", "CLAUDE.md.tmpl", data)
	if err != nil {
		return nil, fmt.Errorf("render CLAUDE.md template: %w", err)
	}

	result := &ClaudeMdResult{Content: content}

	if !opts.Apply {
		return result, nil
	}

	// Apply mode: write to CLAUDE.md
	claudeMdPath := filepath.Join(opts.RepoPath, "CLAUDE.md")
	result.FilePath = claudeMdPath

	existing, err := os.ReadFile(filepath.Clean(claudeMdPath))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read CLAUDE.md: %w", err)
	}

	existingStr := string(existing)

	if hasThrumSection(existingStr) {
		if !opts.Force {
			result.Skipped = true
			result.SkipReason = "Thrum section already exists (use --force to overwrite)"
			return result, nil
		}
		// Force: replace existing section
		existingStr = replaceThrumSection(existingStr, content)
	} else {
		// Append with separator
		if existingStr != "" {
			existingStr = strings.TrimRight(existingStr, "\n") + "\n\n---\n\n"
		}
		existingStr += content
	}

	if err := os.WriteFile(claudeMdPath, []byte(existingStr), 0600); err != nil {
		return nil, fmt.Errorf("write CLAUDE.md: %w", err)
	}

	result.Applied = true
	return result, nil
}

// hasThrumSection checks if the content contains a Thrum Agent Coordination section.
func hasThrumSection(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == claudeMdHeader {
			return true
		}
	}
	return false
}

// replaceThrumSection replaces the existing Thrum section with new content.
// The section starts at "# Thrum Agent Coordination" and ends at the next
// top-level header ("# ") or "---" separator or EOF.
func replaceThrumSection(existing, newContent string) string {
	lines := strings.Split(existing, "\n")
	var before, after []string
	inSection := false
	sectionEnded := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inSection && trimmed == claudeMdHeader {
			inSection = true
			continue
		}
		if inSection && !sectionEnded {
			// Section ends at next top-level header or --- separator
			if (strings.HasPrefix(trimmed, "# ") && trimmed != claudeMdHeader) || trimmed == "---" {
				// If it's a separator before more content, skip the separator
				if trimmed == "---" {
					// A trailing "---" with no content after it is part of the
					// thrum section (the separator that was added when the section
					// was originally appended). We drop it so the replacement
					// doesn't produce a dangling separator at the end of the file.
					// If there IS content after it, the separator belongs to the
					// next section and must be preserved.
					hasMore := false
					for j := i + 1; j < len(lines); j++ {
						if strings.TrimSpace(lines[j]) != "" {
							hasMore = true
							break
						}
					}
					if hasMore {
						after = append(after, lines[i:]...)
					}
				} else {
					after = append(after, lines[i:]...)
				}
				break
			}
			continue
		}
		if !inSection {
			before = append(before, line)
		}
	}

	var result strings.Builder
	if len(before) > 0 {
		result.WriteString(strings.TrimRight(strings.Join(before, "\n"), "\n"))
		result.WriteString("\n\n---\n\n")
	}
	result.WriteString(newContent)
	if len(after) > 0 {
		afterStr := strings.Join(after, "\n")
		if !strings.HasPrefix(afterStr, "\n") {
			result.WriteString("\n")
		}
		result.WriteString(afterStr)
	}

	return result.String()
}
