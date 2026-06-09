package sync

import "testing"

func TestDeriveProbeURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://github.com/owner/repo.git", "https://github.com/owner/repo.git"},
		{"http://github.com/owner/repo.git", "https://github.com/owner/repo.git"},
		{"git@github.com:owner/repo.git", "https://github.com/owner/repo.git"},
		{"ssh://git@github.com/owner/repo.git", "https://github.com/owner/repo.git"},
		{"git@git.sr.ht:~user/repo", "https://git.sr.ht/~user/repo"},
		{"ssh://git@host.example.com:2222/team/repo.git", "https://host.example.com/team/repo.git"},
	}
	for _, c := range cases {
		got, ok := deriveProbeURL(c.in)
		if !ok || got != c.want {
			t.Errorf("deriveProbeURL(%q) = (%q,%v), want (%q,true)", c.in, got, ok, c.want)
		}
	}
	// A bare local path is not probeable over HTTPS.
	if _, ok := deriveProbeURL("/srv/git/repo.git"); ok {
		t.Error("local path should not yield a probe URL")
	}
}

func TestCanonicalRemoteIdentity(t *testing.T) {
	want := "github.com/owner/repo"
	for _, in := range []string{
		"https://github.com/owner/repo.git",
		"https://github.com/owner/repo",
		"git@github.com:owner/repo.git",
		"ssh://git@github.com/owner/repo.git",
		"ssh://git@github.com:22/owner/repo.git",
		"https://GitHub.com/owner/repo.git/",
	} {
		if got := canonicalRemoteIdentity(in); got != want {
			t.Errorf("canonicalRemoteIdentity(%q) = %q, want %q", in, got, want)
		}
	}
	if got := canonicalRemoteIdentity("git@git.sr.ht:~user/repo"); got != "git.sr.ht/~user/repo" {
		t.Errorf("sr.ht: got %q", got)
	}
	if got := canonicalRemoteIdentity("/srv/git/repo.git"); got != "" {
		t.Errorf("local path should canonicalize to empty, got %q", got)
	}
}
