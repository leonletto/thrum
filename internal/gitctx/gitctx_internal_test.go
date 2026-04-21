package gitctx

import (
	"reflect"
	"testing"
)

func TestParseStatusOutput(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   []string
	}{
		{
			name:  "unstaged modification (space M)",
			input: " M README.md",
			want:  []string{"README.md"},
		},
		{
			name:  "staged modification (M space)",
			input: "M  README.md",
			want:  []string{"README.md"},
		},
		{
			name:  "staged and unstaged modification (MM)",
			input: "MM README.md",
			want:  []string{"README.md"},
		},
		{
			name:  "untracked file",
			input: "?? .beads/",
			want:  []string{".beads/"},
		},
		{
			name:  "staged new file",
			input: "A  new.go",
			want:  []string{"new.go"},
		},
		{
			name:  "unstaged deletion",
			input: " D deleted.go",
			want:  []string{"deleted.go"},
		},
		{
			name:  "rename",
			input: "R  old.go -> new.go",
			want:  []string{"old.go -> new.go"},
		},
		{
			name:  "empty input",
			input: "",
			want:  []string{},
		},
		{
			name:  "trailing newline",
			input: "?? foo.txt\n",
			want:  []string{"foo.txt"},
		},
		{
			name:  "multiple files",
			input: " M README.md\n?? .beads/\nA  new.go\n D deleted.go",
			want:  []string{"README.md", ".beads/", "new.go", "deleted.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseStatusOutput(tt.input)
			// Normalise nil vs empty slice for comparison.
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseStatusOutput(%q)\n  got  %v\n  want %v", tt.input, got, tt.want)
			}
		})
	}
}
