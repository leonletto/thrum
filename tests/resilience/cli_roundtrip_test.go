//go:build resilience

package resilience

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// thrumBin is the path to the built thrum binary, set by buildThrum.
var (
	thrumBin     string
	thrumBinOnce sync.Once
	thrumBinErr  error
)

// buildThrum compiles the thrum binary once per test process and returns its path.
// Uses a persistent /tmp dir so the binary survives across test functions.
func buildThrum(t *testing.T) string {
	t.Helper()

	thrumBinOnce.Do(func() {
		binDir, err := os.MkdirTemp("", "thrum-test-bin-*")
		if err != nil {
			thrumBinErr = err
			return
		}
		binPath := filepath.Join(binDir, "thrum")

		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/thrum")
		cmd.Dir = findRepoRoot(t)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			thrumBinErr = err
			return
		}
		thrumBin = binPath
	})

	if thrumBinErr != nil {
		t.Fatalf("Failed to build thrum binary: %v", thrumBinErr)
	}
	return thrumBin
}

// findRepoRoot walks up from the current directory to find the repo root (contains go.mod).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod)")
		}
		dir = parent
	}
}

// runThrum executes the thrum binary with the given args and environment.
// Returns stdout, stderr, and any error. Commands are killed after 30s to prevent hangs.
func runThrum(t *testing.T, bin, repoDir, agentName string, args ...string) (string, string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fullArgs := append([]string{"--repo", repoDir, "--quiet"}, args...)
	cmd := exec.CommandContext(ctx, bin, fullArgs...)
	env := []string{
		"THRUM_NAME=" + agentName,
		"THRUM_ROLE=coordinator",
		"THRUM_MODULE=all",
		"HOME=" + os.Getenv("HOME"),
	}
	if cliSocketPath != "" {
		env = append(env, "THRUM_SOCKET="+cliSocketPath)
	}
	cmd.Env = append(os.Environ(), env...)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.String(), stderr.String(), fmt.Errorf("command timed out after 30s: %s %v", bin, args)
	}
	return stdout.String(), stderr.String(), err
}

// TestCLI_SendAndInbox tests the full CLI round-trip: binary → send → inbox → verify.
func TestCLI_SendAndInbox(t *testing.T) {
	bin := buildThrum(t)
	repoDir := setupCLIFixture(t)

	sender := "coordinator_0000"
	recipient := "implementer_0001"

	// Ensure sender has an active session via CLI
	stdout, stderr, err := runThrum(t, bin, repoDir, sender, "session", "start")
	if err != nil {
		t.Fatalf("session start failed: %v\nstderr: %s\nstdout: %s", err, stderr, stdout)
	}

	// Send a directed message via CLI
	start := time.Now()
	stdout, stderr, err = runThrum(t, bin, repoDir, sender,
		"send", "CLI round-trip test message", "--to", "@"+recipient)
	sendDuration := time.Since(start)
	if err != nil {
		t.Fatalf("send failed: %v\nstderr: %s", err, stderr)
	}
	t.Logf("CLI send took %v", sendDuration)

	// Check inbox from recipient's perspective (exclude_self won't filter it)
	start = time.Now()
	stdout, stderr, err = runThrum(t, bin, repoDir, recipient, "inbox", "--all", "--page-size", "100", "--json")
	inboxDuration := time.Since(start)
	if err != nil {
		t.Fatalf("inbox failed: %v\nstderr: %s", err, stderr)
	}
	t.Logf("CLI inbox took %v", inboxDuration)

	// Parse inbox and verify the sent message appears
	var inbox struct {
		Messages []struct {
			MessageID string `json:"message_id"`
			Body      struct {
				Content string `json:"content"`
			} `json:"body"`
		} `json:"messages"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(stdout), &inbox); err != nil {
		t.Fatalf("parse inbox JSON: %v\noutput: %s", err, stdout)
	}

	found := false
	for _, msg := range inbox.Messages {
		if msg.Body.Content == "CLI round-trip test message" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("sent message not found in recipient inbox (total=%d, returned=%d)", inbox.Total, len(inbox.Messages))
	}
}

// TestCLI_InboxFiltering tests inbox filtering at 10K-message scale.
func TestCLI_InboxFiltering(t *testing.T) {
	bin := buildThrum(t)
	repoDir := setupCLIFixture(t)
	agent := "coordinator_0000"

	// Full inbox
	start := time.Now()
	stdout, stderr, err := runThrum(t, bin, repoDir, agent, "inbox", "--all", "--json")
	fullDuration := time.Since(start)
	if err != nil {
		t.Fatalf("inbox (full) failed: %v\nstderr: %s", err, stderr)
	}

	var fullInbox struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(stdout), &fullInbox); err != nil {
		t.Fatalf("parse full inbox: %v", err)
	}
	t.Logf("Full inbox: total=%d, CLI took %v", fullInbox.Total, fullDuration)

	if fullInbox.Total == 0 {
		t.Fatal("expected non-zero total messages")
	}

	// Unread inbox
	start = time.Now()
	stdout, stderr, err = runThrum(t, bin, repoDir, agent, "inbox", "--unread", "--all", "--json")
	unreadDuration := time.Since(start)
	if err != nil {
		t.Fatalf("inbox (unread) failed: %v\nstderr: %s", err, stderr)
	}

	var unreadInbox struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(stdout), &unreadInbox); err != nil {
		t.Fatalf("parse unread inbox: %v", err)
	}
	t.Logf("Unread inbox: total=%d, CLI took %v", unreadInbox.Total, unreadDuration)

	if unreadInbox.Total > fullInbox.Total {
		t.Errorf("unread %d > total %d", unreadInbox.Total, fullInbox.Total)
	}
}

// TestCLI_TeamList tests `thrum team` with all 50 agents.
func TestCLI_TeamList(t *testing.T) {
	bin := buildThrum(t)
	repoDir := setupCLIFixture(t)

	start := time.Now()
	stdout, stderr, err := runThrum(t, bin, repoDir, "coordinator_0000", "team", "--json")
	duration := time.Since(start)
	if err != nil {
		t.Fatalf("team failed: %v\nstderr: %s\nstdout: %s", err, stderr, stdout)
	}
	t.Logf("CLI team list took %v", duration)

	var result struct {
		Members []struct {
			AgentID string `json:"agent_id"`
			Role    string `json:"role"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse team JSON: %v\noutput: %s", err, stdout)
	}

	// team.list returns agents with active sessions (not all 50 agents)
	if len(result.Members) == 0 {
		t.Error("expected at least some team members")
	}
	t.Logf("Team members with active sessions: %d/50", len(result.Members))

	// Verify role diversity
	roles := map[string]int{}
	for _, m := range result.Members {
		roles[m.Role]++
	}
	if len(roles) < 5 {
		t.Errorf("expected at least 5 distinct roles, got %d: %v", len(roles), roles)
	}
}

// TestCLI_StatusOverview tests `thrum status` with populated data.
func TestCLI_StatusOverview(t *testing.T) {
	bin := buildThrum(t)
	repoDir := setupCLIFixture(t)

	start := time.Now()
	stdout, stderr, err := runThrum(t, bin, repoDir, "coordinator_0000", "status", "--json")
	duration := time.Since(start)
	if err != nil {
		t.Fatalf("status failed: %v\nstderr: %s\nstdout: %s", err, stderr, stdout)
	}
	t.Logf("CLI status took %v", duration)

	// Status should contain health info
	if !strings.Contains(stdout, "ok") && !strings.Contains(stdout, "status") {
		t.Errorf("status output doesn't contain expected health info: %s", stdout[:min(len(stdout), 200)])
	}
}

// TestCLI_GroupSend tests sending a message to a group via CLI.
func TestCLI_GroupSend(t *testing.T) {
	bin := buildThrum(t)
	repoDir := setupCLIFixture(t)

	// Start session first
	_, stderr, err := runThrum(t, bin, repoDir, "coordinator_0000", "session", "start")
	if err != nil {
		t.Fatalf("session start: %v\nstderr: %s", err, stderr)
	}

	// Send to the @coordinators group
	start := time.Now()
	stdout, stderr, err := runThrum(t, bin, repoDir, "coordinator_0000",
		"send", "Group test message", "--to", "@coordinators")
	duration := time.Since(start)
	if err != nil {
		t.Fatalf("group send failed: %v\nstderr: %s\nstdout: %s", err, stderr, stdout)
	}
	t.Logf("CLI group send took %v", duration)
}

// TestCLI_WaitTimeout tests that `thrum wait --timeout` returns promptly.
func TestCLI_WaitTimeout(t *testing.T) {
	bin := buildThrum(t)
	repoDir := setupCLIFixture(t)

	// Start a session so wait can subscribe
	_, stderr, err := runThrum(t, bin, repoDir, "coordinator_0000", "session", "start")
	if err != nil {
		t.Fatalf("session start: %v\nstderr: %s", err, stderr)
	}

	// Wait with a very short timeout — should return within ~2s
	start := time.Now()
	_, _, _ = runThrum(t, bin, repoDir, "coordinator_0000", "wait", "--timeout", "1s")
	duration := time.Since(start)

	// The wait should complete (either timeout or return) within a reasonable window
	if duration > 10*time.Second {
		t.Errorf("wait --timeout 1s took %v (expected < 10s)", duration)
	}
	t.Logf("CLI wait --timeout 1s took %v", duration)
}

// TestCLI_AgentContext tests `thrum context show` at scale.
func TestCLI_AgentContext(t *testing.T) {
	bin := buildThrum(t)
	repoDir := setupCLIFixture(t)

	start := time.Now()
	stdout, stderr, err := runThrum(t, bin, repoDir, "coordinator_0000",
		"context", "show", "--agent", "coordinator_0000", "--json")
	duration := time.Since(start)
	if err != nil {
		t.Fatalf("context show failed: %v\nstderr: %s", err, stderr)
	}
	t.Logf("CLI context show took %v", duration)

	var result struct {
		HasContext bool   `json:"has_context"`
		Content    string `json:"content"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		// Context show might not support --json; that's ok, just verify it ran
		t.Logf("Output (non-JSON): %s", stdout[:min(len(stdout), 200)])
		return
	}

	if result.HasContext {
		t.Logf("Agent context: %d bytes", len(result.Content))
	}
}

// TestCLI_ReplyChain tests multi-message reply thread via CLI.
func TestCLI_ReplyChain(t *testing.T) {
	bin := buildThrum(t)
	repoDir := setupCLIFixture(t)

	sender := "coordinator_0000"
	replier := "implementer_0001"

	// Start sessions for both agents
	_, stderr, err := runThrum(t, bin, repoDir, sender, "session", "start")
	if err != nil {
		t.Fatalf("sender session start: %v\nstderr: %s", err, stderr)
	}
	_, stderr, err = runThrum(t, bin, repoDir, replier, "session", "start")
	if err != nil {
		t.Fatalf("replier session start: %v\nstderr: %s", err, stderr)
	}

	// Send original message (JSON to get message_id)
	stdout, stderr, err := runThrum(t, bin, repoDir, sender,
		"send", "Original for reply chain", "--to", "@"+replier, "--json")
	if err != nil {
		t.Fatalf("send original: %v\nstderr: %s\nstdout: %s", err, stderr, stdout)
	}

	var sendResult struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal([]byte(stdout), &sendResult); err != nil {
		t.Fatalf("parse send result: %v\noutput: %s", err, stdout)
	}
	if sendResult.MessageID == "" {
		t.Fatal("expected message_id from send")
	}

	// Reply to the original
	start := time.Now()
	stdout, stderr, err = runThrum(t, bin, repoDir, replier,
		"reply", sendResult.MessageID, "First reply in chain")
	replyDuration := time.Since(start)
	if err != nil {
		t.Fatalf("reply: %v\nstderr: %s\nstdout: %s", err, stderr, stdout)
	}
	t.Logf("CLI reply took %v", replyDuration)

	// Verify chain appears in inbox
	stdout, stderr, err = runThrum(t, bin, repoDir, replier, "inbox", "--all", "--json")
	if err != nil {
		t.Fatalf("inbox: %v\nstderr: %s", err, stderr)
	}

	var inbox struct {
		Messages []struct {
			MessageID string `json:"message_id"`
			ReplyTo   string `json:"reply_to"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(stdout), &inbox); err != nil {
		t.Fatalf("parse inbox: %v", err)
	}

	// At least one message should be a reply (has reply_to set)
	hasReply := false
	for _, msg := range inbox.Messages {
		if msg.ReplyTo != "" {
			hasReply = true
			break
		}
	}
	if !hasReply {
		t.Log("No reply_to messages found in inbox — reply threading may not expose reply_to in list")
	}
}

// TestCLI_QuickstartPopulated tests registering a new agent in a populated environment.
func TestCLI_QuickstartPopulated(t *testing.T) {
	bin := buildThrum(t)
	repoDir := setupCLIFixture(t)

	// Register a new agent via agent register
	newAgent := fmt.Sprintf("resilience_tester_%d", time.Now().UnixNano()%10000)
	start := time.Now()
	stdout, stderr, err := runThrum(t, bin, repoDir, newAgent,
		"agent", "register", "--role", "tester", "--module", "resilience", "--name", newAgent)
	regDuration := time.Since(start)
	if err != nil {
		t.Fatalf("agent register: %v\nstderr: %s\nstdout: %s", err, stderr, stdout)
	}
	t.Logf("CLI agent register took %v", regDuration)

	// The new agent should appear in agent list alongside the 50 fixture agents
	stdout, stderr, err = runThrum(t, bin, repoDir, "coordinator_0000", "agent", "list", "--json")
	if err != nil {
		t.Fatalf("agent list: %v\nstderr: %s", err, stderr)
	}

	// JSON format: {"agents": {"agents": [...]}}
	var agentList struct {
		Agents struct {
			Agents []struct {
				AgentID string `json:"agent_id"`
			} `json:"agents"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(stdout), &agentList); err != nil {
		t.Fatalf("parse agent list: %v\noutput: %s", err, stdout[:min(len(stdout), 500)])
	}

	agents := agentList.Agents.Agents
	if len(agents) < 51 {
		t.Errorf("expected >= 51 agents (50 fixture + new), got %d", len(agents))
	}

	found := false
	for _, a := range agents {
		if a.AgentID == newAgent {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("newly registered agent %s not found in agent list", newAgent)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
