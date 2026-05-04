package safecmd

import (
	"strings"
	"testing"
)

// TestCleanTmuxEnv_ScrubsThrumVars pins thrum-8nro.4: the daemon's tmux
// subprocess environ must not carry THRUM_* vars, even when the daemon
// itself was launched from a primed shell. tmux propagates client environ to
// new sessions as default-environment, so any THRUM_* leak here poisons
// every spawned pane's identity resolution.
func TestCleanTmuxEnv_ScrubsThrumVars(t *testing.T) {
	t.Setenv("THRUM_AGENT_ID", "coordinator_main")
	t.Setenv("THRUM_NAME", "coordinator_main")
	t.Setenv("THRUM_ROLE", "coordinator")
	t.Setenv("THRUM_MODULE", "main")
	t.Setenv("THRUM_INTENT", "Coordinate agents and tasks in thrum")
	t.Setenv("THRUM_HOME", "/Users/leon/dev/opensource/thrum")
	t.Setenv("THRUM_WS_PORT", "54545")
	t.Setenv("THRUM_TS_AUTHKEY", "tskey-auth-redacted")

	env := cleanTmuxEnv()
	for _, e := range env {
		if strings.HasPrefix(e, "THRUM_") {
			t.Errorf("cleanTmuxEnv leaked THRUM_* var: %q", e)
		}
	}
}

// TestCleanTmuxEnv_ScrubsTmuxVars retains the original guarantee: TMUX and
// TMUX_PANE are removed so daemon-spawned tmux subprocesses do not connect
// to the daemon launcher's tmux server.
func TestCleanTmuxEnv_ScrubsTmuxVars(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,12345,0")
	t.Setenv("TMUX_PANE", "%42")

	env := cleanTmuxEnv()
	for _, e := range env {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") {
			t.Errorf("cleanTmuxEnv leaked tmux var: %q", e)
		}
	}
}

// TestCleanTmuxEnv_PreservesUnrelatedVars ensures the scrub is targeted —
// PATH, HOME, USER and other non-THRUM/non-TMUX vars must pass through, or
// the daemon-spawned tmux session will be unable to find binaries or resolve
// the user's shell config.
func TestCleanTmuxEnv_PreservesUnrelatedVars(t *testing.T) {
	t.Setenv("THRUM_HOME", "/should/be/scrubbed")
	t.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin")
	t.Setenv("HOME", "/Users/test")
	t.Setenv("UNRELATED_VAR", "keep-me")

	env := cleanTmuxEnv()

	want := map[string]bool{
		"PATH=":          false,
		"HOME=":          false,
		"UNRELATED_VAR=": false,
	}
	for _, e := range env {
		for prefix := range want {
			if strings.HasPrefix(e, prefix) {
				want[prefix] = true
			}
		}
	}
	for prefix, found := range want {
		if !found {
			t.Errorf("cleanTmuxEnv dropped unrelated var with prefix %q", prefix)
		}
	}
}
