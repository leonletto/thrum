package safecmd

import (
	"strings"
	"testing"
)

func TestBuildProbeEnv_DropsAllAuthVectors(t *testing.T) {
	// Simulate a daemon that inherited gh/CI auth (the live-incident vector).
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	t.Setenv("GIT_ASKPASS", "/usr/bin/askpass")
	t.Setenv("GIT_HTTP_EXTRAHEADER", "AUTHORIZATION: bearer ghs_secret")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "http.extraheader")
	t.Setenv("GIT_CONFIG_VALUE_0", "AUTHORIZATION: bearer ghs_secret")
	t.Setenv("XDG_CONFIG_HOME", "/home/u/.config")
	t.Setenv("PATH", "/usr/bin:/bin")

	env := buildProbeEnv()
	joined := strings.Join(env, "\n")

	// Allowlist carries PATH and a clean HOME pointing at an empty temp dir.
	if !hasEnvKey(env, "PATH=") {
		t.Fatal("probe env must carry PATH")
	}
	home := envValue(env, "HOME=")
	if home == "" || home == "/home/u" {
		t.Fatalf("probe env HOME must be a clean temp dir, got %q", home)
	}
	// Explicit disable flags present.
	for _, want := range []string{
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_ASKPASS=", "SSH_ASKPASS=", "SSH_AUTH_SOCK=",
		"GIT_HTTP_EXTRAHEADER=", "GIT_SSH_COMMAND=",
	} {
		if !contains(env, want) {
			t.Errorf("probe env missing disable flag %q", want)
		}
	}
	// No auth/config-injection vector carries a non-empty value.
	for _, bad := range []string{
		"AUTHORIZATION", "ghs_secret", "GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=", "GIT_CONFIG_VALUE_0=", "XDG_CONFIG_HOME=/home",
		"/tmp/agent.sock", "/usr/bin/askpass",
	} {
		if strings.Contains(joined, bad) {
			t.Errorf("probe env leaked auth vector %q:\n%s", bad, joined)
		}
	}
}

func contains(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

func hasEnvKey(env []string, prefix string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

func envValue(env []string, prefix string) string {
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}
