package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Test6yt7_TmuxRestartUsesLongerDeadline pins the thrum-6yt7 fix: `thrum tmux
// restart` must NOT use the 10s default Call deadline. HandleRestart is
// synchronous and a graceful restart waits for the agent's snapshot up to
// Restart.graceful_timeout (default 30s, configurable) plus kill/create/launch
// — routinely exceeding 10s and producing a false-negative i/o-timeout on a
// restart that actually SUCCEEDED. The client must pass a generous timeout via
// CallWithTimeout.
func Test6yt7_TmuxRestartUsesLongerDeadline(t *testing.T) {
	// 1. The timeout value exceeds the 10s default by a wide margin.
	const oldDefault = 10 * time.Second
	if tmuxRestartCallTimeout <= oldDefault {
		t.Fatalf("tmuxRestartCallTimeout = %s, must exceed the 10s default Call deadline", tmuxRestartCallTimeout)
	}
	if tmuxRestartCallTimeout < 90*time.Second {
		t.Errorf("tmuxRestartCallTimeout = %s, expected >= 90s to cover graceful_timeout + launch", tmuxRestartCallTimeout)
	}

	// 2. The tmux.restart client call is wired through CallWithTimeout, not the
	// bare 10s Call. Guards against a revert that silently reintroduces the
	// false-negative. (go test runs with CWD = the package dir.)
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, `client.CallWithTimeout("tmux.restart"`) {
		t.Error("tmux.restart client call must use client.CallWithTimeout (thrum-6yt7)")
	}
	if strings.Contains(body, `client.Call("tmux.restart"`) {
		t.Error("tmux.restart uses the bare 10s client.Call — reintroduces the thrum-6yt7 false-negative")
	}
}
