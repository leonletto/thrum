package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateClaudeMd_Stdout(t *testing.T) {
	result, err := GenerateClaudeMd(ClaudeMdOptions{
		RepoPath: t.TempDir(),
		Apply:    false,
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Content == "" {
		t.Error("expected non-empty content")
	}
	if result.Applied {
		t.Error("should not be applied in stdout mode")
	}

	// Verify no files written
	claudeMd := filepath.Join(t.TempDir(), "CLAUDE.md")
	if _, err := os.Stat(claudeMd); !os.IsNotExist(err) {
		t.Error("stdout mode should not create CLAUDE.md")
	}
}

func TestGenerateClaudeMd_TemplateVariables(t *testing.T) {
	result, err := GenerateClaudeMd(ClaudeMdOptions{
		RepoPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(result.Content, "{{") {
		t.Error("output contains unsubstituted template variables")
	}
}

func TestGenerateClaudeMd_ValidMarkdown(t *testing.T) {
	result, err := GenerateClaudeMd(ClaudeMdOptions{
		RepoPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(result.Content, claudeMdHeader) {
		t.Errorf("expected content to start with %q, got: %s", claudeMdHeader, result.Content[:50])
	}
}

func TestGenerateClaudeMd_Apply_NewFile(t *testing.T) {
	tmpDir := t.TempDir()

	result, err := GenerateClaudeMd(ClaudeMdOptions{
		RepoPath: tmpDir,
		Apply:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !result.Applied {
		t.Error("expected Applied=true")
	}

	content, err := os.ReadFile(filepath.Clean(filepath.Join(tmpDir, "CLAUDE.md")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), claudeMdHeader) {
		t.Error("CLAUDE.md should contain Thrum header")
	}
}

func TestGenerateClaudeMd_Apply_AppendToExisting(t *testing.T) {
	tmpDir := t.TempDir()
	existing := "# My Project\n\nSome existing content.\n"
	_ = os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte(existing), 0644)

	result, err := GenerateClaudeMd(ClaudeMdOptions{
		RepoPath: tmpDir,
		Apply:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !result.Applied {
		t.Error("expected Applied=true")
	}

	content, err := os.ReadFile(filepath.Clean(filepath.Join(tmpDir, "CLAUDE.md")))
	if err != nil {
		t.Fatal(err)
	}
	contentStr := string(content)

	if !strings.Contains(contentStr, "# My Project") {
		t.Error("original content should be preserved")
	}
	if !strings.Contains(contentStr, "---") {
		t.Error("should have separator between existing and new content")
	}
	if !strings.Contains(contentStr, claudeMdHeader) {
		t.Error("should contain Thrum header")
	}
}

func TestGenerateClaudeMd_Apply_DuplicateDetection(t *testing.T) {
	tmpDir := t.TempDir()

	// First apply
	_, err := GenerateClaudeMd(ClaudeMdOptions{
		RepoPath: tmpDir,
		Apply:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Second apply — should skip
	result, err := GenerateClaudeMd(ClaudeMdOptions{
		RepoPath: tmpDir,
		Apply:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !result.Skipped {
		t.Error("expected Skipped=true on second apply")
	}
	if result.SkipReason == "" {
		t.Error("expected SkipReason to be set")
	}

	// Verify content not duplicated
	content, _ := os.ReadFile(filepath.Clean(filepath.Join(tmpDir, "CLAUDE.md")))
	count := strings.Count(string(content), claudeMdHeader)
	if count != 1 {
		t.Errorf("expected exactly 1 Thrum header, got %d", count)
	}
}

func TestGenerateClaudeMd_Apply_ForceOverwrite(t *testing.T) {
	tmpDir := t.TempDir()

	// First apply
	_, err := GenerateClaudeMd(ClaudeMdOptions{
		RepoPath: tmpDir,
		Apply:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Force overwrite
	result, err := GenerateClaudeMd(ClaudeMdOptions{
		RepoPath: tmpDir,
		Apply:    true,
		Force:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !result.Applied {
		t.Error("expected Applied=true with force")
	}
	if result.Skipped {
		t.Error("should not skip with force")
	}

	// Verify exactly one section
	content, _ := os.ReadFile(filepath.Clean(filepath.Join(tmpDir, "CLAUDE.md")))
	count := strings.Count(string(content), claudeMdHeader)
	if count != 1 {
		t.Errorf("expected exactly 1 Thrum header after force, got %d", count)
	}
}

func TestGenerateClaudeMd_Apply_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte(""), 0644)

	result, err := GenerateClaudeMd(ClaudeMdOptions{
		RepoPath: tmpDir,
		Apply:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !result.Applied {
		t.Error("expected Applied=true")
	}

	content, _ := os.ReadFile(filepath.Clean(filepath.Join(tmpDir, "CLAUDE.md")))
	if !strings.HasPrefix(string(content), claudeMdHeader) {
		t.Error("empty file should get content starting with Thrum header")
	}
}

func TestGenerateClaudeMd_SectionBoundary(t *testing.T) {
	tests := []struct {
		name     string
		existing string
	}{
		{
			name:     "section at start",
			existing: claudeMdHeader + "\n\nOld thrum content.\n",
		},
		{
			name:     "section in middle",
			existing: "# My Project\n\nIntro.\n\n---\n\n" + claudeMdHeader + "\n\nOld thrum.\n\n---\n\n# Other Section\n\nMore.\n",
		},
		{
			name:     "section at end",
			existing: "# My Project\n\nIntro.\n\n---\n\n" + claudeMdHeader + "\n\nOld thrum content.\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			_ = os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte(tt.existing), 0644)

			result, err := GenerateClaudeMd(ClaudeMdOptions{
				RepoPath: tmpDir,
				Apply:    true,
				Force:    true,
			})
			if err != nil {
				t.Fatal(err)
			}

			if !result.Applied {
				t.Error("expected Applied=true")
			}

			content, _ := os.ReadFile(filepath.Clean(filepath.Join(tmpDir, "CLAUDE.md")))
			count := strings.Count(string(content), claudeMdHeader)
			if count != 1 {
				t.Errorf("expected exactly 1 Thrum header, got %d in:\n%s", count, content)
			}
		})
	}
}

func TestGenerateClaudeMd_PreservesContent(t *testing.T) {
	tmpDir := t.TempDir()
	existing := "# My Project\n\nBefore thrum.\n\n---\n\n" + claudeMdHeader + "\n\nOld thrum.\n\n---\n\n# After Section\n\nAfter thrum.\n"
	_ = os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte(existing), 0644)

	_, err := GenerateClaudeMd(ClaudeMdOptions{
		RepoPath: tmpDir,
		Apply:    true,
		Force:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	content, _ := os.ReadFile(filepath.Clean(filepath.Join(tmpDir, "CLAUDE.md")))
	contentStr := string(content)

	if !strings.Contains(contentStr, "# My Project") {
		t.Error("content before thrum section should be preserved")
	}
	if !strings.Contains(contentStr, "# After Section") {
		t.Error("content after thrum section should be preserved")
	}
	if !strings.Contains(contentStr, "After thrum.") {
		t.Error("content after thrum section body should be preserved")
	}
	if strings.Contains(contentStr, "Old thrum.") {
		t.Error("old thrum content should be replaced")
	}
}
