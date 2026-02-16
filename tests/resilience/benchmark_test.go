//go:build resilience

package resilience

import (
	"fmt"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// setupFixtureForBench copies the shared fixture for benchmark use.
// Each benchmark gets a mutable copy since daemons modify the DB.
func setupFixtureForBench(b *testing.B) string {
	b.Helper()

	if sharedFixtureDir == "" {
		b.Fatal("shared fixture not initialized (TestMain not run?)")
	}

	tmpDir := b.TempDir()
	thrumDir := tmpDir + "/.thrum"

	// Copy shared fixture using cp -a (preserves permissions, much faster than tar.gz extraction)
	cpCmd := exec.Command("cp", "-a", sharedFixtureDir, thrumDir)
	if out, err := cpCmd.CombinedOutput(); err != nil {
		b.Fatalf("cp shared fixture: %v\n%s", err, out)
	}

	return thrumDir
}

// startTestDaemonForBench starts a daemon for benchmark use.
func startTestDaemonForBench(b *testing.B, thrumDir string) string {
	b.Helper()
	_, _, socketPath := startTestDaemon(b, thrumDir)
	return socketPath
}

// ensureSessionForBench starts a session for the given agent (benchmark version).
func ensureSessionForBench(b *testing.B, socketPath, agentID string) {
	b.Helper()
	_, err := rpcCallRaw(socketPath, "session.start", map[string]any{
		"agent_id": agentID,
	})
	if err != nil {
		b.Fatalf("ensureSession for %s: %v", agentID, err)
	}
}

func BenchmarkSendMessage(b *testing.B) {
	thrumDir := setupFixtureForBench(b)
	socketPath := startTestDaemonForBench(b, thrumDir)
	ensureSessionForBench(b, socketPath, "coordinator_0000")

	b.ResetTimer()
	for i := range b.N {
		_, err := rpcCallRaw(socketPath, "message.send", map[string]any{
			"caller_agent_id": "coordinator_0000",
			"content":         fmt.Sprintf("Benchmark message %d", i),
			"format":          "markdown",
		})
		if err != nil {
			b.Fatalf("send: %v", err)
		}
	}
}

func BenchmarkInbox10K(b *testing.B) {
	thrumDir := setupFixtureForBench(b)
	socketPath := startTestDaemonForBench(b, thrumDir)

	b.ResetTimer()
	for range b.N {
		_, err := rpcCallRaw(socketPath, "message.list", map[string]any{
			"caller_agent_id": "coordinator_0000",
			"page_size":       100,
		})
		if err != nil {
			b.Fatalf("inbox: %v", err)
		}
	}
}

func BenchmarkInboxUnread(b *testing.B) {
	thrumDir := setupFixtureForBench(b)
	socketPath := startTestDaemonForBench(b, thrumDir)

	b.ResetTimer()
	for range b.N {
		_, err := rpcCallRaw(socketPath, "message.list", map[string]any{
			"caller_agent_id": "coordinator_0000",
			"unread":          true,
			"page_size":       100,
		})
		if err != nil {
			b.Fatalf("inbox unread: %v", err)
		}
	}
}

func BenchmarkAgentList50(b *testing.B) {
	thrumDir := setupFixtureForBench(b)
	socketPath := startTestDaemonForBench(b, thrumDir)

	b.ResetTimer()
	for range b.N {
		_, err := rpcCallRaw(socketPath, "agent.list", map[string]any{})
		if err != nil {
			b.Fatalf("agent list: %v", err)
		}
	}
}

func BenchmarkGroupList(b *testing.B) {
	thrumDir := setupFixtureForBench(b)
	socketPath := startTestDaemonForBench(b, thrumDir)

	b.ResetTimer()
	for range b.N {
		_, err := rpcCallRaw(socketPath, "group.list", nil)
		if err != nil {
			b.Fatalf("group list: %v", err)
		}
	}
}

func BenchmarkConcurrentSend10(b *testing.B) {
	thrumDir := setupFixtureForBench(b)
	socketPath := startTestDaemonForBench(b, thrumDir)
	// Ensure all sender agents have sessions
	for i := range 10 {
		ensureSessionForBench(b, socketPath, fixtureAgentName(i+1))
	}

	b.ResetTimer()
	for range b.N {
		done := make(chan struct{})
		go func() {
			var wg sync.WaitGroup
			for j := range 10 {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					rpcCallRaw(socketPath, "message.send", map[string]any{
						"caller_agent_id": fixtureAgentName(idx + 1),
						"content":         "Benchmark concurrent",
						"format":          "markdown",
					})
				}(j)
			}
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(30 * time.Second):
			b.Fatal("BenchmarkConcurrentSend10: deadlock detected (30s timeout)")
		}
	}
}
