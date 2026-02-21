package cli

import "testing"

func TestDefaultIntent(t *testing.T) {
	tests := []struct {
		role     string
		repo     string
		expected string
	}{
		{"coordinator", "thrum", "Coordinate agents and tasks in thrum"},
		{"implementer", "thrum", "Implement features and fixes in thrum"},
		{"reviewer", "myapp", "Review code and PRs in myapp"},
		{"planner", "thrum", "Plan architecture and design in thrum"},
		{"tester", "thrum", "Test and validate changes in thrum"},
		{"unknown_role", "thrum", "Working in thrum"},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := DefaultIntent(tt.role, tt.repo)
			if got != tt.expected {
				t.Errorf("DefaultIntent(%q, %q) = %q, want %q", tt.role, tt.repo, got, tt.expected)
			}
		})
	}
}

func TestAutoDisplay(t *testing.T) {
	tests := []struct {
		role, module, expected string
	}{
		{"coordinator", "main", "Coordinator (main)"},
		{"implementer", "auth", "Implementer (auth)"},
		{"coordinator", "", "Coordinator"},
		{"", "main", ""},
	}
	for _, tt := range tests {
		t.Run(tt.role+"_"+tt.module, func(t *testing.T) {
			got := AutoDisplay(tt.role, tt.module)
			if got != tt.expected {
				t.Errorf("AutoDisplay(%q, %q) = %q, want %q", tt.role, tt.module, got, tt.expected)
			}
		})
	}
}

func TestGetRepoName(t *testing.T) {
	got := GetRepoName("/nonexistent/path")
	if got != "unknown" {
		t.Logf("GetRepoName for non-repo: %q", got)
	}
}

func TestGetCurrentBranch(t *testing.T) {
	got := GetCurrentBranch("/nonexistent/path")
	if got != "main" {
		t.Errorf("GetCurrentBranch fallback = %q, want %q", got, "main")
	}
}

func TestGetRepoID(t *testing.T) {
	got := GetRepoID("/nonexistent/path")
	if got != "" {
		t.Logf("GetRepoID for non-repo: %q", got)
	}
}
