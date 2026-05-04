package tmux

import (
	"slices"
	"testing"
)

// TestBuildCreateSessionArgs_ScrubsThrumVars pins thrum-t8mj: long-running
// tmux servers retain stale THRUM_* environ that propagates to new sessions.
// The `-e KEY=` flags assembled here override per-session, regardless of
// server age.
func TestBuildCreateSessionArgs_ScrubsThrumVars(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/Users/test",
		"THRUM_HOME=/poisoned/path",
		"THRUM_AGENT_ID=coordinator_main",
		"THRUM_NAME=coordinator_main",
		"THRUM_ROLE=coordinator",
	}
	args := buildCreateSessionArgs("coord", "/tmp/repo", env)

	wantKeys := []string{"THRUM_HOME=", "THRUM_AGENT_ID=", "THRUM_NAME=", "THRUM_ROLE="}
	for _, want := range wantKeys {
		if !hasFlagPair(args, "-e", want) {
			t.Errorf("missing -e %q in args: %v", want, args)
		}
	}

	// Non-THRUM_* keys must NOT appear as -e flags.
	for _, leak := range []string{"PATH=", "HOME=/Users/test"} {
		if hasFlagPair(args, "-e", leak) {
			t.Errorf("non-THRUM_ var leaked as -e flag %q in args: %v", leak, args)
		}
	}
}

// TestBuildCreateSessionArgs_NoThrumVars verifies that a clean environ
// produces no -e flags. On a freshly-bounced machine where the daemon was
// launched without THRUM_* in scope, no per-session override is needed.
func TestBuildCreateSessionArgs_NoThrumVars(t *testing.T) {
	env := []string{"PATH=/usr/bin", "HOME=/Users/test", "USER=test"}
	args := buildCreateSessionArgs("coord", "/tmp/repo", env)

	for i, a := range args {
		if a == "-e" {
			t.Errorf("unexpected -e flag at index %d in args: %v", i, args)
		}
	}
}

// TestBuildCreateSessionArgs_PreservesBaseFlags pins backwards compatibility:
// the original `new-session -d -s NAME -c CWD` shape must remain.
func TestBuildCreateSessionArgs_PreservesBaseFlags(t *testing.T) {
	args := buildCreateSessionArgs("mysess", "/some/cwd", nil)

	wantPrefix := []string{"new-session", "-d", "-s", "mysess", "-c", "/some/cwd"}
	if !slices.Equal(args[:len(wantPrefix)], wantPrefix) {
		t.Errorf("base args = %v, want prefix %v", args, wantPrefix)
	}
	if len(args) != len(wantPrefix) {
		t.Errorf("nil env produced extra flags: %v", args[len(wantPrefix):])
	}
}

// TestBuildCreateSessionArgs_OmitsCwdWhenEmpty matches the pre-fix behavior
// where an empty cwd produced just the new-session base.
func TestBuildCreateSessionArgs_OmitsCwdWhenEmpty(t *testing.T) {
	args := buildCreateSessionArgs("mysess", "", []string{"THRUM_HOME=/x"})

	for i, a := range args {
		if a == "-c" {
			t.Errorf("unexpected -c flag at index %d (cwd was empty): %v", i, args)
		}
	}
	if !hasFlagPair(args, "-e", "THRUM_HOME=") {
		t.Errorf("THRUM_HOME scrub missing when cwd empty: %v", args)
	}
}

// TestBuildCreateSessionArgs_DedupsAcrossSources pins behavior when the
// caller passes the union of os.Environ() and tmux show-environment output
// — both sources commonly contain the same THRUM_* keys. Duplicate `-e
// KEY=` flags would be harmless but wasteful; we dedupe instead.
func TestBuildCreateSessionArgs_DedupsAcrossSources(t *testing.T) {
	// Same THRUM_HOME appears twice (e.g., once in daemon env, once in
	// tmux server global env).
	env := []string{
		"THRUM_HOME=/from-daemon",
		"THRUM_NAME=daemon-name",
		"THRUM_HOME=/from-tmux-server",
	}
	args := buildCreateSessionArgs("s", "", env)

	count := 0
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-e" && args[i+1] == "THRUM_HOME=" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("THRUM_HOME -e flag emitted %d times, want 1: %v", count, args)
	}
}

// TestBuildCreateSessionArgs_SkipsMalformedEnvEntries defends against
// unusual env-entry shapes:
//
//   - `THRUM_NOEQUALS`        — exercises the `eq < 0` guard inside the loop.
//   - `=THRUM_LEADING_EQ`     — exercises the HasPrefix("THRUM_") guard at
//     the top of the loop (the leading `=` means the prefix doesn't match,
//     so this entry never reaches the eq check). Pinned here for hygiene.
//   - `-THRUM_HOME`           — negated form that `tmux show-environment -g`
//     can emit when a key is explicitly removed from a session. Same
//     HasPrefix guard catches it (starts with `-`, not `THRUM_`). Pinned so
//     a future change to the prefix logic can't accidentally let `-` keys
//     through and produce a -e flag for a phantom variable.
//
// None of these should reach the args list.
func TestBuildCreateSessionArgs_SkipsMalformedEnvEntries(t *testing.T) {
	env := []string{
		"THRUM_HOME=/poisoned",
		"THRUM_NOEQUALS",
		"=THRUM_LEADING_EQ",
		"-THRUM_HOME",
	}
	args := buildCreateSessionArgs("s", "", env)

	// Sane entry survives.
	if !hasFlagPair(args, "-e", "THRUM_HOME=") {
		t.Errorf("expected THRUM_HOME= -e flag in args: %v", args)
	}
	// THRUM_HOME= flag should appear EXACTLY once even though the env list
	// contains a sane THRUM_HOME=... and a malformed -THRUM_HOME variant
	// (defensive against the negated-entry case slipping through and
	// duplicating the override).
	count := 0
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-e" && args[i+1] == "THRUM_HOME=" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("THRUM_HOME= -e flag emitted %d times, want 1: %v", count, args)
	}

	// None of the malformed forms (or partial forms of them) should leak
	// into the args slice as a literal entry.
	for _, malformed := range []string{
		"THRUM_NOEQUALS", "=THRUM_LEADING_EQ", "=", "-THRUM_HOME",
	} {
		for i, a := range args {
			if a == malformed {
				t.Errorf("malformed entry %q leaked into args at index %d: %v", malformed, i, args)
			}
		}
	}
	// And no -e flag for the negated/malformed forms with empty value.
	for _, ghost := range []string{"-THRUM_HOME=", "THRUM_NOEQUALS=", "=THRUM_LEADING_EQ="} {
		if hasFlagPair(args, "-e", ghost) {
			t.Errorf("ghost -e flag for malformed entry %q in args: %v", ghost, args)
		}
	}
}

// hasFlagPair returns true if args contains a consecutive [flag, value]
// pair anywhere in the slice.
func hasFlagPair(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

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
