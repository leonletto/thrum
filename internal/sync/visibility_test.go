package sync

import (
	"errors"
	"testing"
)

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

func TestClassifyVisibility(t *testing.T) {
	type tc struct {
		out  string
		err  bool
		want Visibility
	}
	cases := []tc{
		{"<sha>\trefs/heads/a-sync\n", false, VisPublic}, // refs returned
		{"fatal: could not read Username for 'https://github.com': terminal prompts disabled", true, VisPrivate},
		{"fatal: Authentication failed for 'https://gitlab.com/o/r.git/'", true, VisPrivate},
		{"remote: Repository not found.\nfatal: repository 'https://github.com/o/r.git/' not found", true, VisPrivate},
		{"fatal: unable to access 'https://host/': Could not resolve host: host", true, VisUndetectable},
		{"fatal: unable to access 'https://host/': Failed to connect ... Connection refused", true, VisUndetectable},
		{"error: RPC failed; HTTP 500 ... ", true, VisUndetectable},
	}
	for _, c := range cases {
		got := classifyVisibility([]byte(c.out), errIf(c.err))
		if got != c.want {
			t.Errorf("classifyVisibility(%q,err=%v) = %v, want %v", c.out, c.err, got, c.want)
		}
	}
}

func errIf(b bool) error {
	if b {
		return errors.New("exit status 128")
	}
	return nil
}

func TestClassifyVisibility_ExportedWrapper(t *testing.T) {
	if ClassifyVisibility([]byte("<sha>\trefs/heads/x\n"), nil) != VisPublic {
		t.Fatal("exported wrapper must delegate to classifyVisibility")
	}
}
