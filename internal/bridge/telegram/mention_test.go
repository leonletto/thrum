package telegram

import "testing"

func TestParseMentions(t *testing.T) {
	tests := []struct {
		text     string
		mentions []string
	}{
		{"hello world", nil},
		{"@bot_name hello", []string{"bot_name"}},
		{"@bot_a @bot_b check this", []string{"bot_a", "bot_b"}},
		{"email@example.com", nil},
		{"@coordinator_main check the API", []string{"coordinator_main"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := ParseMentions(tt.text)
			if len(got) != len(tt.mentions) {
				t.Errorf("ParseMentions(%q) = %v, want %v", tt.text, got, tt.mentions)
			}
		})
	}
}

func TestStripMention(t *testing.T) {
	tests := []struct {
		text    string
		mention string
		want    string
	}{
		{"@bot_name hello", "bot_name", "hello"},
		{"@bot_a @bot_b check", "bot_a", "@bot_b check"},
		{"no mention", "bot_name", "no mention"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := StripMention(tt.text, tt.mention)
			if got != tt.want {
				t.Errorf("StripMention(%q, %q) = %q, want %q", tt.text, tt.mention, got, tt.want)
			}
		})
	}
}
