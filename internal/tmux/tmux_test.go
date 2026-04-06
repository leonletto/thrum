package tmux

import (
	"testing"
)

func TestSanitizeSessionName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"implementer-api", "implementer-api"},
		{"impl_writer_website_dev", "impl_writer_website_dev"},
		{"has spaces", "has-spaces"},
		{"has.dots", "has-dots"},
		{"has:colons", "has-colons"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeSessionName(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeSessionName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDefaultSessionName(t *testing.T) {
	tests := []struct {
		role, module string
		want         string
	}{
		{"implementer", "website-dev", "implementer-website-dev"},
		{"coordinator", "main", "coordinator-main"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := DefaultSessionName(tt.role, tt.module)
			if got != tt.want {
				t.Errorf("DefaultSessionName(%q, %q) = %q, want %q", tt.role, tt.module, got, tt.want)
			}
		})
	}
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		target  string
		session string
		window  string
		pane    string
	}{
		{"my-session:0.0", "my-session", "0", "0"},
		{"my-session", "my-session", "", ""},
		{"sess:3", "sess", "3", ""},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			s, w, p := ParseTarget(tt.target)
			if s != tt.session || w != tt.window || p != tt.pane {
				t.Errorf("ParseTarget(%q) = (%q,%q,%q), want (%q,%q,%q)",
					tt.target, s, w, p, tt.session, tt.window, tt.pane)
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"with spaces", "'with spaces'"},
		{"it's quoted", "'it'\\''s quoted'"},
		{"", "''"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestInTmux(t *testing.T) {
	// InTmux reads $TMUX — test with and without
	t.Setenv("TMUX", "")
	if InTmux() {
		t.Error("InTmux() = true with empty TMUX")
	}

	t.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
	if !InTmux() {
		t.Error("InTmux() = false with TMUX set")
	}
}
