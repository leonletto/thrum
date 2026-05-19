package runtime

import "testing"

// TestClaudeBottomAnchorRegex pins the U+2500 horizontal-rule pattern
// that separates Claude Code's conversation transcript from its input
// chrome. The silence watchdog (thrum-84xc) keys engagement decisions
// on the FIRST occurrence of this anchor after the identity sentinel —
// false positives here would mis-classify chrome as agent output.
func TestClaudeBottomAnchorRegex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want bool
	}{
		// Positives — minimum width and beyond.
		{"min_width_20", "────────────────────", true},
		{"width_40", "────────────────────────────────────────", true},
		{"width_100_oversized", string([]rune{
			'─', '─', '─', '─', '─', '─', '─', '─', '─', '─',
			'─', '─', '─', '─', '─', '─', '─', '─', '─', '─',
			'─', '─', '─', '─', '─', '─', '─', '─', '─', '─',
			'─', '─', '─', '─', '─', '─', '─', '─', '─', '─',
			'─', '─', '─', '─', '─', '─', '─', '─', '─', '─',
		}), true},
		// Negatives — too short, contaminated, wrong char.
		{"too_short_19", "───────────────────", false},
		{"trailing_text", "──────────────────── footer", false},
		{"leading_text", "transcript ────────────────────", false},
		{"em_dash_not_box_draw", "————————————————————", false},
		{"ascii_dash_not_box_draw", "--------------------", false},
		{"empty", "", false},
		{"single_dash", "─", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := claudeBottomAnchorRegex.MatchString(tc.line)
			if got != tc.want {
				t.Errorf("claudeBottomAnchorRegex.MatchString(%q) = %v; want %v",
					tc.line, got, tc.want)
			}
		})
	}
}

// TestClaudeSpinnerRegex pins the three observed forms of Claude Code's
// animated thinking indicator. The silence watchdog ignores matched
// lines as chrome — false negatives here would re-classify the spinner
// as agent output and skip a legitimate nudge.
func TestClaudeSpinnerRegex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want bool
	}{
		// Positives — short form (sub-minute).
		{"short_form_typical", "✻ Churned for 17s", true},
		{"short_form_single_digit", "✻ Twirling for 3s", true},
		{"short_form_three_digit", "✻ Brewing for 142s", true},
		// Positives — long form (≥1m).
		{"long_form_typical", "✻ Baked for 1m 45s", true},
		{"long_form_double_digit_minutes", "✻ Pondered for 12m 7s", true},
		// Positives — dot form (2.1.141+).
		{"dot_form_typical", "· Twisting…", true},
		{"dot_form_multi_word_verb", "· Restructuring…", true},
		// Positives — non-ASCII verb (\S+ tolerates).
		{"non_ascii_verb_short_form", "✻ Sautéed for 3s", true},
		{"non_ascii_verb_dot_form", "· Sautéing…", true},
		// Negatives — missing components.
		{"missing_for_short_form", "✻ Churned 17s", false},
		{"missing_unit_short_form", "✻ Churned for 17", false},
		{"missing_ellipsis_dot_form", "· Twisting", false},
		{"wrong_unit_word", "✻ Churned for 17 sec", false},
		// Negatives — wrong leading char.
		{"asterisk_not_bullet", "* Twisting…", false},
		{"hyphen_not_middle_dot", "- Twisting…", false},
		{"plain_text_no_marker", "Twisting…", false},
		// Negatives — chrome lines that look adjacent.
		{"divider_line", "────────────────────────────────────────", false},
		{"input_box_chrome", "│ >                                     │", false},
		{"empty", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := claudeSpinnerRegex.MatchString(tc.line)
			if got != tc.want {
				t.Errorf("claudeSpinnerRegex.MatchString(%q) = %v; want %v",
					tc.line, got, tc.want)
			}
		})
	}
}

// TestClaudePresetWiresAnchors confirms BuiltinPresets["claude"] sources
// its anchor regexes from this file's vars rather than redeclaring them.
// Step 3 of plan §Task 71 — verifies "no duplicate anchor pattern
// definitions remain"; a future contributor copy-pasting the regex into
// presets.go would break this pin.
func TestClaudePresetWiresAnchors(t *testing.T) {
	t.Parallel()
	preset, ok := BuiltinPresets["claude"]
	if !ok {
		t.Fatal("BuiltinPresets[\"claude\"] missing")
	}
	if preset.BottomAnchorRegex != claudeBottomAnchorRegex {
		t.Error("claude preset BottomAnchorRegex is not the canonical claudeBottomAnchorRegex var (duplicate definition?)")
	}
	if preset.SpinnerRegex != claudeSpinnerRegex {
		t.Error("claude preset SpinnerRegex is not the canonical claudeSpinnerRegex var (duplicate definition?)")
	}
}
