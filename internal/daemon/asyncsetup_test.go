package daemon

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestValidateASyncRemoteURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
		errHint string // substring that must appear in the error (optional)
	}{
		{"empty", "", true, "required"},
		{"https-plain", "https://github.com/leon/thrum.git", false, ""},
		{"https-with-credentials", "https://user:token@github.com/leon/thrum.git", false, ""},
		{"http-plain", "http://git.example.com/repo.git", false, ""},
		{"ssh-url", "ssh://git@github.com/leon/thrum.git", false, ""},
		{"git-scheme", "git://git.kernel.org/linux.git", false, ""},
		{"scp-shorthand", "git@github.com:leon/thrum.git", false, ""},
		{"scp-shorthand-complex", "deploy-bot@gitlab.company.net:org/repo-name.git", false, ""},
		{"file-scheme", "file:///tmp/repo", true, "not allowed"},
		{"ftp-scheme", "ftp://host/repo", true, "not allowed"},
		{"scheme-missing-host", "https:///path", true, "host"},
		{"random-junk", "not a url", true, "not a recognized git URL"},
		{"whitespace-only", "   ", true, "required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateASyncRemoteURL(c.url)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if c.errHint != "" && !strings.Contains(err.Error(), c.errHint) {
					t.Fatalf("error %q does not contain %q", err.Error(), c.errHint)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSanitizeURLForLog(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://user:token@host/path", "https://host/path"},
		{"https://user@host/path", "https://host/path"},
		{"https://host/path", "https://host/path"},
		{"git@github.com:user/repo.git", "git@github.com:user/repo.git"}, // SCP shorthand untouched
		{"", ""},
		{"ssh://user:pass@host/x", "ssh://host/x"},
	}
	for _, c := range cases {
		got := sanitizeURLForLog(c.in)
		if got != c.want {
			t.Errorf("sanitizeURLForLog(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateASyncRemoteURL_ErrorDoesNotLeakCredentials(t *testing.T) {
	// The scheme-reject path does not interpolate the URL body, so a URL
	// with an inline token does not echo the token in the error.
	err := ValidateASyncRemoteURL("ftp://user:secrettoken@host/repo")
	if err == nil {
		t.Fatalf("expected error for ftp scheme")
	}
	if strings.Contains(err.Error(), "secrettoken") {
		t.Fatalf("error message leaked credential: %q", err.Error())
	}
}

func TestASyncPeerDaemonID_Deterministic(t *testing.T) {
	a := ASyncPeerDaemonID("https://github.com/leon/thrum.git")
	b := ASyncPeerDaemonID("https://github.com/leon/thrum.git")
	if a != b {
		t.Fatalf("expected deterministic output, got %s vs %s", a, b)
	}
	if !strings.HasPrefix(a, "async:") {
		t.Fatalf("expected async: prefix, got %s", a)
	}
	c := ASyncPeerDaemonID("https://github.com/other/repo.git")
	if c == a {
		t.Fatalf("different URLs should produce different IDs")
	}
}

func TestASyncPeerDaemonID_WhitespaceTolerant(t *testing.T) {
	a := ASyncPeerDaemonID("https://github.com/leon/thrum.git")
	b := ASyncPeerDaemonID("  https://github.com/leon/thrum.git  ")
	if a != b {
		t.Fatalf("whitespace should not change ID: %s vs %s", a, b)
	}
}

func TestASyncPeerName(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://github.com/leon/thrum.git", "async-github-com-leon-thrum-git"},
		{"git@github.com:leon/thrum.git", "async-github-com-leon-thrum-git"},
		{"ssh://user:pass@gitlab.example.com/org/repo.git", "async-gitlab-example-com-org-repo-git"},
		{"https://host.tld/", "async-host-tld"},
		{"", "async-peer"},
	}
	for _, c := range cases {
		got := ASyncPeerName(c.url)
		if got != c.want {
			t.Errorf("ASyncPeerName(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// --- ConfigureASyncRemote integration tests (local git repo fixtures) ---

// newEmptyRepo creates a fresh, empty git repo at a temp dir and returns its
// path. Fails the test on any git error.
func newEmptyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v (%s)", dir, err, out)
	}
	return dir
}

func getOrigin(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "remote", "get-url", "origin").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func TestConfigureASyncRemote_AddsWhenMissing(t *testing.T) {
	repo := newEmptyRepo(t)
	url := "https://github.com/leon/thrum.git"

	if err := ConfigureASyncRemote(context.Background(), repo, url); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if got := getOrigin(t, repo); got != url {
		t.Fatalf("origin = %q, want %q", got, url)
	}
}

func TestConfigureASyncRemote_IdempotentWhenMatches(t *testing.T) {
	repo := newEmptyRepo(t)
	url := "https://github.com/leon/thrum.git"

	if err := ConfigureASyncRemote(context.Background(), repo, url); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := ConfigureASyncRemote(context.Background(), repo, url); err != nil {
		t.Fatalf("re-add (should be idempotent): %v", err)
	}
	if got := getOrigin(t, repo); got != url {
		t.Fatalf("origin = %q, want %q", got, url)
	}
}

func TestConfigureASyncRemote_ErrorsOnMismatch(t *testing.T) {
	repo := newEmptyRepo(t)
	first := "https://github.com/leon/thrum.git"
	second := "https://github.com/other/repo.git"

	if err := ConfigureASyncRemote(context.Background(), repo, first); err != nil {
		t.Fatalf("first add: %v", err)
	}
	err := ConfigureASyncRemote(context.Background(), repo, second)
	if err == nil {
		t.Fatalf("expected error on URL mismatch")
	}
	// Both URLs must be mentioned (sanitized) so the user can resolve.
	if !strings.Contains(err.Error(), "other/repo.git") {
		t.Errorf("expected second URL in error: %q", err.Error())
	}
	// Original URL must not be clobbered.
	if got := getOrigin(t, repo); got != first {
		t.Errorf("origin changed unexpectedly: %q (want %q)", got, first)
	}
}

func TestConfigureASyncRemote_ErrorDoesNotLeakCredentials(t *testing.T) {
	repo := newEmptyRepo(t)
	first := "https://alice:firstsecret@example.com/a.git"
	second := "https://bob:secondsecret@example.com/b.git"

	if err := ConfigureASyncRemote(context.Background(), repo, first); err != nil {
		t.Fatalf("first add: %v", err)
	}
	err := ConfigureASyncRemote(context.Background(), repo, second)
	if err == nil {
		t.Fatalf("expected mismatch error")
	}
	if strings.Contains(err.Error(), "firstsecret") || strings.Contains(err.Error(), "secondsecret") {
		t.Fatalf("error leaked credentials: %q", err.Error())
	}
}

func TestConfigureASyncRemote_RejectsInvalidURL(t *testing.T) {
	repo := newEmptyRepo(t)
	err := ConfigureASyncRemote(context.Background(), repo, "file:///tmp/repo")
	if err == nil {
		t.Fatalf("expected rejection of file:// scheme")
	}
	if got := getOrigin(t, repo); got != "" {
		t.Errorf("origin should not have been set, got %q", got)
	}
}
