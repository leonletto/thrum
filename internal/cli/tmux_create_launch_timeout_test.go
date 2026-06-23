package cli

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Test6yt7Sibling_TmuxCreateLaunchUseLongerDeadline pins the create/launch half
// of the thrum-6yt7 false-negative fix. `thrum tmux create` (HandleCreate:
// worktree create + agent register + tmux spawn) and `thrum tmux launch`
// (runtime boot + shell-ready probe) are synchronous and routinely exceed the
// 10s default Call deadline under load, returning an i/o-timeout on an op that
// actually SUCCEEDED. Both client calls must use CallWithTimeout with a generous
// deadline. Surfaced on the full release-test gate (scenarios 26/69/70-75).
func Test6yt7Sibling_TmuxCreateLaunchUseLongerDeadline(t *testing.T) {
	// 1. The timeout value exceeds the 10s default by a wide margin.
	const oldDefault = 10 * time.Second
	if tmuxCreateLaunchCallTimeout <= oldDefault {
		t.Fatalf("tmuxCreateLaunchCallTimeout = %s, must exceed the 10s default Call deadline", tmuxCreateLaunchCallTimeout)
	}
	if tmuxCreateLaunchCallTimeout < 90*time.Second {
		t.Errorf("tmuxCreateLaunchCallTimeout = %s, expected >= 90s to cover create+launch under load", tmuxCreateLaunchCallTimeout)
	}

	// 2. The tmux.create + tmux.launch client calls are wired through
	// CallWithTimeout, not the bare 10s Call. Guards against a revert that
	// silently reintroduces the false-negative. (go test runs with CWD = the
	// package dir.)
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("read tmux.go: %v", err)
	}
	body := string(src)

	if !strings.Contains(body, `client.CallWithTimeout("tmux.create"`) {
		t.Error("tmux.create client call must use client.CallWithTimeout (thrum-6yt7 sibling)")
	}
	if strings.Contains(body, `client.Call("tmux.create"`) {
		t.Error("tmux.create uses the bare 10s client.Call — reintroduces the thrum-6yt7 false-negative")
	}

	if !strings.Contains(body, `client.CallWithTimeout("tmux.launch"`) {
		t.Error("tmux.launch client call must use client.CallWithTimeout (thrum-6yt7 sibling)")
	}
	if strings.Contains(body, `client.Call("tmux.launch"`) {
		t.Error("tmux.launch uses the bare 10s client.Call — reintroduces the thrum-6yt7 false-negative")
	}
}
