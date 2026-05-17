package claude

import (
	"strings"
	"testing"
)

// TestTranscriptDir_FromWorktreePath pins the canonical §8.2 algorithm
// against a real path captured from disk:
//
//	/Users/leon/.thrum/worktrees/thrum/a-b1-impl  →
//	-Users-leon--thrum-worktrees-thrum-a-b1-impl
//
// Note both '/' and '.' map to '-'; that's why /Users/leon/.thrum produces
// the `--thrum` double-dash. Verified against the real on-disk Claude Code
// transcript directories.
func TestTranscriptDir_FromWorktreePath(t *testing.T) {
	t.Setenv("HOME", "/Users/leon")

	cases := []struct {
		worktreePath  string
		wakeTimestamp int64
		agentName     string
		want          string
	}{
		{
			worktreePath:  "/Users/leon/.thrum/worktrees/thrum/docs-bot",
			wakeTimestamp: 1234567890,
			agentName:     "docs_bot",
			want:          "/Users/leon/.claude/projects/-Users-leon--thrum-worktrees-thrum-docs-bot-1234567890/",
		},
		{
			worktreePath:  "/Users/leon/.thrum/worktrees/thrum/a-b1-impl",
			wakeTimestamp: 1747353600,
			agentName:     "impl_a_b1",
			want:          "/Users/leon/.claude/projects/-Users-leon--thrum-worktrees-thrum-a-b1-impl-1747353600/",
		},
	}
	for _, tc := range cases {
		got := TranscriptDir(tc.worktreePath, tc.agentName, tc.wakeTimestamp)
		if got != tc.want {
			t.Errorf("TranscriptDir(%q, %q, %d) = %q; want %q",
				tc.worktreePath, tc.agentName, tc.wakeTimestamp, got, tc.want)
		}
	}
}

// TestTranscriptDir_HandlesTrailingSlash: trailing slash on worktreePath
// must not produce double dashes / empty segments in the hash.
func TestTranscriptDir_HandlesTrailingSlash(t *testing.T) {
	t.Setenv("HOME", "/tmp/home")
	got := TranscriptDir("/path/to/wt/", "agent", 100)
	if !strings.Contains(got, "-path-to-wt-100/") {
		t.Errorf("trailing slash not handled cleanly: %q", got)
	}
}

// TestTranscriptDir_DotsBecomeDashes pins the '.' → '-' substitution
// explicitly (Claude Code's algorithm; needed for `.thrum`, `.local`,
// `.cargo`, etc.).
func TestTranscriptDir_DotsBecomeDashes(t *testing.T) {
	t.Setenv("HOME", "/tmp/home")
	got := TranscriptDir("/home/user/.foo.bar/baz", "agent", 42)
	// .foo.bar → -foo-bar
	if !strings.Contains(got, "-home-user--foo-bar-baz-42/") {
		t.Errorf("dots not replaced: %q", got)
	}
}
