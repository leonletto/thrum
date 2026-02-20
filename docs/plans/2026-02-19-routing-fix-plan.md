# SendMessage Routing Fix Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix MCP message routing so messages are delivered deterministically to the correct recipients, matching the CLI's behavior.

**Architecture:** The CLI already uses `buildForAgentClause` (a 3-part OR covering mentions, groups, and legacy broadcasts) which works correctly. The fix aligns the MCP send and receive paths to use the same identity and filtering mechanisms. Reply routing is fixed to include the original sender. No schema changes needed.

**Tech Stack:** Go, SQLite, JSON-RPC daemon, MCP server

**Research:** See `dev-docs/routing-research/findings_*.md` for detailed code traces.

---

### Task 1: Fix `parseMentionRole` — Rename to `parseMention` and Preserve Names

The current `parseMentionRole` always extracts the role component, discarding agent names. It should pass through the cleaned string (just strip `@`), since `HandleSend` already handles the group-vs-mention decision.

**Files:**
- Modify: `internal/mcp/tools.go:294-307`
- Modify: `internal/mcp/tools_test.go:8-31`

**Step 1: Update the test to expect name-preserving behavior**

In `internal/mcp/tools_test.go`, rename the test and update expectations:

```go
func TestParseMention(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"@ops", "ops"},
		{"@reviewer", "reviewer"},
		{"@impl_api", "impl_api"},           // NEW: name preserved
		{"ops", "ops"},
		{"reviewer", "reviewer"},
		{"impl_api", "impl_api"},            // NEW: name preserved
		{"agent:ops:abc123", "ops"},          // legacy format: extract role
		{"agent:reviewer:xyz", "reviewer"},
		{"agent:", ""},
		{"@", ""},
		{"@everyone", "everyone"},            // NEW: group name preserved
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseMention(tt.input)
			if got != tt.expected {
				t.Errorf("parseMention(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/mcp/ -run TestParseMention -v`
Expected: FAIL — `parseMention` undefined (still named `parseMentionRole`)

**Step 3: Rename function and update callers**

In `internal/mcp/tools.go`, rename `parseMentionRole` → `parseMention`. The function body stays the same — it already does the right thing for names (returns them as-is after stripping `@`). The rename clarifies that it's not role-specific.

```go
// parseMention extracts the recipient identifier from various addressing formats.
// - "@impl_api" → "impl_api" (agent name)
// - "@reviewer" → "reviewer" (role or name)
// - "agent:ops:abc123" → "ops" (legacy format extracts role)
// - "ops" → "ops" (bare string passthrough)
func parseMention(to string) string {
	if strings.HasPrefix(to, "@") {
		return to[1:]
	}
	if strings.HasPrefix(to, "agent:") {
		parts := strings.SplitN(to, ":", 3)
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	return to
}
```

Update the caller at line 28: `mentionRole := parseMention(input.To)`

**Step 4: Run test to verify it passes**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/mcp/ -run TestParseMention -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "refactor: rename parseMentionRole to parseMention" -m "Clarifies that the function preserves agent names, not just roles. No behavioral change — the function already passed through names correctly."
```

---

### Task 2: Fix MCP `handleSendMessage` — Add CallerAgentID

The MCP server knows its own identity (`s.agentID`) but doesn't pass it when sending messages. This causes the daemon to fall back to its own config identity, misattributing messages in multi-worktree setups.

**Files:**
- Modify: `internal/mcp/tools.go:31-37` (handleSendMessage)
- Modify: `internal/mcp/tools.go:242-247` (handleBroadcast)

**Step 1: Add CallerAgentID to send request in handleSendMessage**

In `internal/mcp/tools.go`, update the `sendReq` construction at line 31:

```go
	sendReq := rpc.SendRequest{
		Content:       input.Content,
		Format:        "markdown",
		Mentions:      []string{parseMention(input.To)},
		Priority:      input.Priority,
		ReplyTo:       input.ReplyTo,
		CallerAgentID: s.agentID, // NEW: identify sender correctly
	}
```

**Step 2: Add CallerAgentID to broadcast request in handleBroadcast**

In `internal/mcp/tools.go`, update the `sendReq` at line 242:

```go
	sendReq := rpc.SendRequest{
		Content:       input.Content,
		Format:        "markdown",
		Mentions:      []string{"everyone"},
		Priority:      priority,
		CallerAgentID: s.agentID, // NEW: identify sender correctly
	}
```

**Step 3: Run tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/mcp/ -v -count=1`
Expected: PASS (existing tests should still pass; CallerAgentID is additive)

**Step 4: Commit**

```bash
git add internal/mcp/tools.go
git commit -m "fix: include CallerAgentID in MCP send and broadcast requests" -m "MCP server now passes s.agentID as CallerAgentID so daemon attributes messages to the correct sender, fixing misattribution in multi-worktree setups."
```

---

### Task 3: Fix MCP `handleCheckMessages` — Use ForAgent/ForAgentRole

This is the core routing fix. The MCP `check_messages` currently uses `MentionRole` (role-only filter) which misses name-directed messages, broadcasts, and group messages. It should use `ForAgent`/`ForAgentRole` which triggers the comprehensive `buildForAgentClause`.

**Files:**
- Modify: `internal/mcp/tools.go:83-90`

**Step 1: Replace MentionRole with ForAgent/ForAgentRole**

In `internal/mcp/tools.go`, replace lines 83-90:

```go
	// Step 1: List unread messages for this agent (by ID and role)
	listReq := rpc.ListMessagesRequest{
		ForAgent:      s.agentID,     // matches by agent name/ID
		ForAgentRole:  s.agentRole,   // matches by role
		CallerAgentID: s.agentID,     // for is_read computation
		UnreadForAgent: s.agentID,    // only unread
		ExcludeSelf:   true,          // don't show own messages
		PageSize:      limit,
		SortBy:        "created_at",
		SortOrder:     "asc",
	}
```

**Step 2: Run tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/mcp/ -v -count=1`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/mcp/tools.go
git commit -m "fix: MCP check_messages uses ForAgent/ForAgentRole instead of MentionRole" -m "This triggers buildForAgentClause which correctly matches name-directed messages, group messages, and broadcasts. Previously only role-based mentions were matched."
```

---

### Task 4: Fix MCP Mark-Read — Add CallerAgentID

Both `handleCheckMessages` and `waiter.fetchAndMark` call `message.markRead` without `CallerAgentID`, causing read-state to be recorded under the wrong agent in multi-worktree setups.

**Files:**
- Modify: `internal/mcp/tools.go:140` (check_messages mark-read)
- Modify: `internal/mcp/waiter.go:366` (waiter mark-read)

**Step 1: Fix mark-read in handleCheckMessages**

In `internal/mcp/tools.go`, replace line 140:

```go
	markReq := rpc.MarkReadRequest{
		MessageIDs:    messageIDs,
		CallerAgentID: s.agentID, // NEW: mark read under correct agent
	}
```

**Step 2: Fix mark-read in waiter.fetchAndMark**

The waiter needs access to the agent ID. Check if `w.agentID` already exists on the Waiter struct.

In `internal/mcp/waiter.go`, at line 366 replace:

```go
	_ = markClient.Call("message.markRead", rpc.MarkReadRequest{
		MessageIDs:    []string{messageID},
		CallerAgentID: w.agentID, // NEW: mark read under correct agent
	}, nil)
```

If `w.agentID` doesn't exist on the Waiter struct, add it. Check the Waiter struct definition and `NewWaiter` constructor — the agent ID should already be available since the waiter is constructed by the MCP server which has `s.agentID`.

**Step 3: Run tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/mcp/ -v -count=1`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/waiter.go
git commit -m "fix: include CallerAgentID in MCP mark-read requests" -m "Read-state is now recorded under the correct agent identity instead of the daemon's config identity. Fixes mark-read in both check_messages and waiter paths."
```

---

### Task 5: Fix Reply Routing — Include Original Sender

When replying, the current code copies the parent's mention refs (role-based audience). The original sender is never added as a recipient, so they may not see the reply.

**Files:**
- Modify: `internal/cli/message.go:191-232`
- Modify: `internal/cli/message_test.go`

**Step 1: Write a failing test for reply-to-sender**

In `internal/cli/message_test.go`, add a test that verifies the reply includes the parent's sender in mentions:

```go
func TestReplyIncludesSender(t *testing.T) {
	// Parent message: sent by "coordinator" with mention:implementer
	parentMessage := map[string]any{
		"message": map[string]any{
			"message_id": "msg_parent",
			"author":     map[string]string{"agent_id": "coordinator", "session_id": "ses_1"},
			"body":       map[string]any{"format": "markdown", "content": "Please review"},
			"scopes":     []any{},
			"refs":       []any{map[string]string{"type": "mention", "value": "implementer"}},
			"metadata":   map[string]string{},
			"created_at": "2026-02-19T10:00:00Z",
		},
	}

	// Track what send request the reply produces
	var sentParams map[string]any

	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			switch request["method"] {
			case "message.get":
				response := map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result":  parentMessage,
				}
				_ = encoder.Encode(response)
			case "message.send":
				sentParams = request["params"].(map[string]any)
				response := map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"message_id": "msg_reply",
						"created_at": "2026-02-19T10:01:00Z",
					},
				}
				_ = encoder.Encode(response)
			}
		}
	})

	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	_, err = Reply(client, ReplyOptions{
		MessageID:     "msg_parent",
		Content:       "Done",
		CallerAgentID: "impl_api",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify mentions include both the original audience AND the sender
	mentions, ok := sentParams["mentions"].([]any)
	if !ok {
		t.Fatal("expected mentions in send params")
	}

	mentionStrs := make([]string, len(mentions))
	for i, m := range mentions {
		mentionStrs[i] = m.(string)
	}

	// Must contain "implementer" (original audience) AND "coordinator" (sender)
	hasImplementer := false
	hasCoordinator := false
	for _, m := range mentionStrs {
		if m == "implementer" || m == "@implementer" {
			hasImplementer = true
		}
		if m == "coordinator" || m == "@coordinator" {
			hasCoordinator = true
		}
	}
	if !hasImplementer {
		t.Errorf("reply mentions missing original audience 'implementer': %v", mentionStrs)
	}
	if !hasCoordinator {
		t.Errorf("reply mentions missing original sender 'coordinator': %v", mentionStrs)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run TestReplyIncludesSender -v`
Expected: FAIL — reply does not include "coordinator"

**Step 3: Fix Reply() to include original sender**

In `internal/cli/message.go`, after extracting mentions from the parent's refs (line 217), add the parent's sender:

```go
	// 3. Add original sender so the reply routes back to them
	senderID := parent.Author.AgentID
	if senderID != "" && senderID != opts.CallerAgentID {
		// Don't add self as mention (would be filtered by ExcludeSelf anyway)
		alreadyMentioned := false
		for _, m := range mentions {
			if m == senderID {
				alreadyMentioned = true
				break
			}
		}
		if !alreadyMentioned {
			mentions = append(mentions, senderID)
		}
	}
```

Place this after the existing mention extraction loop (after line 217) and before the group scope loop (line 219).

**Step 4: Run test to verify it passes**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run TestReplyIncludesSender -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/cli/message.go internal/cli/message_test.go
git commit -m "fix: reply includes original sender in audience" -m "When replying, the original message sender is now added to the reply's mentions so they receive the reply. Previously only the parent's role-based audience was copied, which could miss the specific sender."
```

---

### Task 6: Fix Reply Group Scope Reconstruction

The reply code constructs `"@group:reviewers"` when the parent had a group scope. This should be `"@reviewers"` so `IsGroup()` can find the group.

**Files:**
- Modify: `internal/cli/message.go:219-224`
- Modify: `internal/cli/message_test.go`

**Step 1: Write a failing test for group reply**

Add to `internal/cli/message_test.go`:

```go
func TestReplyGroupScopeReconstruction(t *testing.T) {
	// Parent message with group scope (sent to @reviewers group)
	parentMessage := map[string]any{
		"message": map[string]any{
			"message_id": "msg_parent",
			"author":     map[string]string{"agent_id": "coordinator", "session_id": "ses_1"},
			"body":       map[string]any{"format": "markdown", "content": "Review this"},
			"scopes":     []any{map[string]string{"type": "group", "value": "reviewers"}},
			"refs":       []any{map[string]string{"type": "group", "value": "reviewers"}},
			"metadata":   map[string]string{},
			"created_at": "2026-02-19T10:00:00Z",
		},
	}

	var sentParams map[string]any

	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			switch request["method"] {
			case "message.get":
				response := map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result":  parentMessage,
				}
				_ = encoder.Encode(response)
			case "message.send":
				sentParams = request["params"].(map[string]any)
				response := map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"message_id": "msg_reply",
						"created_at": "2026-02-19T10:01:00Z",
					},
				}
				_ = encoder.Encode(response)
			}
		}
	})

	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	_, err = Reply(client, ReplyOptions{
		MessageID:     "msg_parent",
		Content:       "Reviewed",
		CallerAgentID: "reviewer_1",
	})
	if err != nil {
		t.Fatal(err)
	}

	mentions, ok := sentParams["mentions"].([]any)
	if !ok {
		t.Fatal("expected mentions in send params")
	}

	// The group mention should be "@reviewers", NOT "@group:reviewers"
	for _, m := range mentions {
		ms := m.(string)
		if strings.Contains(ms, "group:") {
			t.Errorf("malformed group mention found: %q (should be '@reviewers', not '@group:reviewers')", ms)
		}
	}

	// Should contain "@reviewers" or "reviewers"
	hasReviewers := false
	for _, m := range mentions {
		ms := m.(string)
		if ms == "@reviewers" || ms == "reviewers" {
			hasReviewers = true
		}
	}
	if !hasReviewers {
		t.Errorf("reply mentions missing group 'reviewers': %v", mentions)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run TestReplyGroupScopeReconstruction -v`
Expected: FAIL — finds malformed `"@group:reviewers"`

**Step 3: Fix the group scope reconstruction**

In `internal/cli/message.go`, replace line 222:

Old:
```go
			mentions = append(mentions, "@"+scope.Type+":"+scope.Value)
```

New:
```go
			mentions = append(mentions, "@"+scope.Value)
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run TestReplyGroupScopeReconstruction -v`
Expected: PASS

**Step 5: Run all message tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run TestReply -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/cli/message.go internal/cli/message_test.go
git commit -m "fix: reply group scope reconstruction uses correct format" -m "Reply to group messages now sends '@reviewers' instead of '@group:reviewers'. The old format was not recognized by IsGroup() and created dead mention refs."
```

---

### Task 7: Fix MCP `list_agents` — Show Agent ID in Name Field

The `list_agents` MCP tool uses `a.Display` for the `Name` field, which is usually empty. Agent callers need the actual agent ID to send messages.

**Files:**
- Modify: `internal/mcp/tools.go:201-208`

**Step 1: Fix Name field to use agent ID**

In `internal/mcp/tools.go`, update the agent info construction (line 201-208):

```go
		name := a.Display
		if name == "" {
			name = a.AgentID
		}

		agents = append(agents, AgentInfo{
			Name:       name,
			Role:       a.Role,
			Module:     a.Module,
			Status:     status,
			LastSeenAt: a.LastSeenAt,
		})
```

**Step 2: Run tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/mcp/ -v -count=1`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/mcp/tools.go
git commit -m "fix: list_agents shows agent ID when display name is empty" -m "Agents need to know the actual agent_id to send directed messages. The Name field now falls back to agent_id instead of showing empty."
```

---

### Task 8: Add `priority` Field to MCP `MessageInfo`

The `MessageInfo` struct in MCP types has a `Priority` field but `handleCheckMessages` never populates it.

**Files:**
- Modify: `internal/mcp/tools.go:114-120`

**Step 1: Add priority to MessageInfo construction**

In `internal/mcp/tools.go`, update the message conversion loop (lines 114-120):

```go
		messages = append(messages, MessageInfo{
			MessageID: msg.MessageID,
			From:      msg.AgentID,
			Content:   msg.Body.Content,
			Priority:  msg.Priority,
			Timestamp: msg.CreatedAt,
		})
```

Check if `msg.Priority` exists on the list response message struct. If not, skip this task.

**Step 2: Run tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/mcp/ -v -count=1`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/mcp/tools.go
git commit -m "fix: include priority field in MCP check_messages response"
```

---

### Task 9: Integration Test — MCP Routing Parity with CLI

Write an integration test that verifies MCP check_messages returns the same messages as CLI inbox for all routing scenarios.

**Files:**
- Modify: `internal/mcp/integration_test.go` (or create `internal/mcp/routing_test.go`)

**Step 1: Write integration test**

The test should use the existing integration test infrastructure (check `integration_test.go` for patterns). It should:

1. Start a daemon
2. Register multiple agents (named agent `impl_api` with role `implementer`, named agent `coordinator` with role `coordinator`)
3. Send messages via various routing paths:
   - `--to @implementer` (role-based)
   - `--to @impl_api` (name-based)
   - `--to @everyone` (broadcast)
4. Verify MCP `check_messages` for `impl_api` returns all 3 messages
5. Verify `coordinator`'s inbox only shows the broadcast

Read the existing `integration_test.go` first to understand the test harness pattern, then write the test following that pattern.

**Step 2: Run integration test**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/mcp/ -run TestMCPRoutingParity -v -count=1`
Expected: PASS (after tasks 1-4 are completed)

**Step 3: Commit**

```bash
git add internal/mcp/routing_test.go
git commit -m "test: add MCP routing parity integration test" -m "Verifies MCP check_messages correctly receives name-directed, role-directed, and broadcast messages, matching CLI inbox behavior."
```

---

### Task 10: Full Test Suite Verification

Run the complete test suite to verify no regressions.

**Step 1: Run all tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./... -count=1 2>&1 | tail -30`
Expected: All packages PASS

**Step 2: Run linter**

Run: `cd /Users/leon/dev/opensource/thrum && make lint`
Expected: No errors

**Step 3: Build**

Run: `cd /Users/leon/dev/opensource/thrum && make build`
Expected: Clean build

**Step 4: Fix any failures**

If tests fail, fix them before proceeding.

**Step 5: Commit any remaining fixes**

```bash
git add -A
git commit -m "fix: resolve test/lint issues from routing fix"
```

---

### Task 11: Recipient Validation and Delivery Status Feedback

Sending to `@nonexistent` silently succeeds with `status: "delivered"` — the message is stored but no one ever receives it. This is a P0 because agents waste time debugging silent message loss. The fix validates each mention against the agents table and groups, returning an error when recipients can't be resolved.

**Files:**
- Modify: `internal/daemon/rpc/message.go` (SendResponse struct + HandleSend mention loop)
- Modify: `internal/cli/send.go` (SendResult struct)
- Modify: `internal/mcp/types.go` (SendMessageOutput struct)
- Modify: `internal/mcp/tools.go` (handleSendMessage + handleBroadcast)
- Modify: `cmd/thrum/main.go` (send command display)

**Step 1: Write failing test for unknown recipient**

Create a test in a new file or add to existing message handler tests. The test should send a message to `@nonexistent` and verify the daemon returns an error with a descriptive message listing the unknown recipient.

```go
// Test that sending to unknown recipient returns error
func TestHandleSend_UnknownRecipient(t *testing.T) {
    // ... setup daemon with no agents registered as "nonexistent" ...
    // Send to @nonexistent
    // Expect: error response containing "unknown recipient" and "@nonexistent"
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/daemon/rpc/ -run TestHandleSend_UnknownRecipient -v`
Expected: FAIL — currently returns success

**Step 3: Update `SendResponse` struct**

In `internal/daemon/rpc/message.go`, add fields to `SendResponse`:

```go
type SendResponse struct {
    MessageID  string   `json:"message_id"`
    ThreadID   string   `json:"thread_id,omitempty"`
    CreatedAt  string   `json:"created_at"`
    ResolvedTo int      `json:"resolved_to"`         // count of resolved mentions
    Warnings   []string `json:"warnings,omitempty"`   // informational (e.g., role has 4 agents)
}
```

**Step 4: Add validation to `HandleSend` mention loop**

In `HandleSend`, replace the `else` branch in the mention loop (lines 282-285). After the `IsGroup` check, validate each non-group mention:

```go
refs := req.Refs
scopes := req.Scopes
resolvedTo := 0
var warnings []string
var unknownRecipients []string

for _, mention := range req.Mentions {
    role := mention
    if len(role) > 0 && role[0] == '@' {
        role = role[1:]
    }

    // Check if this mention is a group
    isGroup, err := h.groupResolver.IsGroup(ctx, role)
    if err != nil {
        return nil, fmt.Errorf("check group %q: %w", role, err)
    }

    if isGroup {
        scopes = append(scopes, types.Scope{Type: "group", Value: role})
        refs = append(refs, types.Ref{Type: "group", Value: role})
        resolvedTo++
        continue
    }

    // Not a group — check if any agent is registered with this ID/name or role
    var count int
    if err := h.state.DB().QueryRowContext(ctx,
        `SELECT COUNT(*) FROM agents WHERE agent_id = ? OR role = ?`,
        role, role,
    ).Scan(&count); err != nil {
        return nil, fmt.Errorf("validate mention %q: %w", role, err)
    }

    if count > 0 {
        refs = append(refs, types.Ref{Type: "mention", Value: role})
        resolvedTo++
    } else {
        unknownRecipients = append(unknownRecipients, "@"+role)
    }
}

// Error if ANY mention could not be resolved
if len(unknownRecipients) > 0 {
    return nil, fmt.Errorf("unknown recipients: %s — no matching agent, role, or group found",
        strings.Join(unknownRecipients, ", "))
}
```

The message is NOT stored when recipients are unknown — this is a hard error, not a soft warning. The sender gets immediate feedback and can fix the address and resend.

**Step 5: Update `SendResult` in CLI**

In `internal/cli/send.go`:

```go
type SendResult struct {
    MessageID  string   `json:"message_id"`
    ThreadID   string   `json:"thread_id,omitempty"`
    CreatedAt  string   `json:"created_at"`
    ResolvedTo int      `json:"resolved_to"`
    Warnings   []string `json:"warnings,omitempty"`
}
```

**Step 6: Update MCP `SendMessageOutput`**

In `internal/mcp/types.go`, replace `RecipientStatus` with more useful fields:

```go
type SendMessageOutput struct {
    Status     string   `json:"status" jsonschema:"Delivery status: delivered or error"`
    MessageID  string   `json:"message_id" jsonschema:"ID of the sent message"`
    ResolvedTo int      `json:"resolved_to" jsonschema:"Number of mentions resolved to a known agent or group"`
    Warnings   []string `json:"warnings,omitempty" jsonschema:"Informational warnings"`
}
```

**Step 7: Update MCP `handleSendMessage` to surface errors**

In `internal/mcp/tools.go`, the existing error handling already propagates daemon errors. The `client.Call` returns an error when `HandleSend` returns one. Update the success path:

```go
return nil, SendMessageOutput{
    Status:     "delivered",
    MessageID:  sendResp.MessageID,
    ResolvedTo: sendResp.ResolvedTo,
    Warnings:   sendResp.Warnings,
}, nil
```

The error path (unknown recipients) is already handled — `client.Call` returns an error, which `handleSendMessage` wraps and returns as a tool error to the MCP client.

**Step 8: Update CLI display to show warnings**

In `cmd/thrum/main.go`, after the send success output:

```go
for _, w := range result.Warnings {
    fmt.Fprintf(os.Stderr, "  warning: %s\n", w)
}
```

**Step 9: Run test to verify it passes**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/daemon/rpc/ -run TestHandleSend_UnknownRecipient -v`
Expected: PASS

**Step 10: Run all tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./... -count=1 2>&1 | tail -30`
Expected: PASS (some existing tests may need updating if they send to unregistered agents)

**Step 11: Commit**

```bash
git add internal/daemon/rpc/message.go internal/cli/send.go internal/mcp/types.go internal/mcp/tools.go cmd/thrum/main.go
git commit -m "fix: reject messages to unknown recipients with informative error" -m "HandleSend now validates each mention against the agents table and groups. Unknown recipients cause a hard error listing the unresolvable addresses. The message is NOT stored — sender must fix the address and resend. Replaces the always-'delivered' status with actual validation feedback."
```

---

### Task 12: Fix MCP Waiter — Subscribe to Group/Broadcast Messages

The MCP waiter subscribes only to `mention_role` (role-based mentions). It never wakes up for `@everyone` broadcasts or group-scoped messages because those are stored as group scopes, not mention refs. The subscription infrastructure already supports scope subscriptions — the waiter just needs to register one.

**Files:**
- Modify: `internal/mcp/waiter.go` (setup function)

**Step 1: Add group scope subscription to setup()**

In `internal/mcp/waiter.go`, after the existing mention subscription (after line 123), add:

```go
	// 3. Subscribe to @everyone group scope so broadcasts wake the waiter.
	everyoneParams := map[string]any{
		"scope": map[string]any{
			"type":  "group",
			"value": "everyone",
		},
	}
	if w.agentID != "" {
		everyoneParams["caller_agent_id"] = w.agentID
	}
	_, err = w.wsRPC("subscribe", everyoneParams)
	if err != nil {
		if !isAlreadyExistsError(err) {
			return fmt.Errorf("subscribe everyone: %w", err)
		}
	}
```

**Step 2: Run tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/mcp/ -v -count=1`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/mcp/waiter.go
git commit -m "fix: MCP waiter subscribes to @everyone group scope" -m "The waiter now registers a scope subscription for the everyone group in addition to the existing mention subscription. This ensures broadcasts and group messages trigger WebSocket push notifications to MCP agents."
```

---

### Task 13: Remove `--all` from `thrum wait` — Always Filter by Agent Identity

`thrum wait --all` is a footgun: the `--all` flag is actually a no-op for filtering (it only affects `afterTime` defaulting), and `waitCmd` never passes `for_agent`/`for_agent_role` to the daemon. This means ALL agents see ALL messages, causing confusion. Fix: always filter by agent identity, remove `--all`, default `afterTime` to "now" unconditionally.

**Files:**
- Modify: `internal/cli/wait.go` (WaitOptions struct, Wait function)
- Modify: `internal/cli/wait_test.go` (update test)
- Modify: `cmd/thrum/main.go` (waitCmd — remove flag, add identity, update description)
- Modify: `internal/cli/prime.go:327` (remove `--all` from generated command)
- Modify: `internal/context/context.go:102` (remove `--all` from context string)
- Modify: `internal/cli/templates/shared/CLAUDE.md.tmpl` (remove `--all`)
- Modify: `internal/cli/templates/auggie/rules.md.tmpl` (remove `--all`)
- Modify: `internal/cli/templates/codex/AGENTS.md.tmpl` (remove `--all`)
- Modify: `internal/cli/templates/cursor/cursorrules.tmpl` (remove `--all`)
- Modify: `internal/cli/templates/gemini/instructions.md.tmpl` (remove `--all`)
- Modify: `CLAUDE.md` (remove `--all` from message-listener pattern)
- Modify: `claude-plugin/agents/message-listener.md` (remove `--all`)
- Modify: `claude-plugin/commands/wait.md` (remove `--all`)
- Modify: `claude-plugin/skills/thrum/resources/LISTENER_PATTERN.md` (remove `--all`)
- Modify: `website/docs/claude-code-plugin.md` (remove `--all`)
- Modify: `website/docs/agent-configs.md` (remove `--all`)
- Modify: `website/docs/agent-coordination.md` (remove `--all`)
- Modify: `website/docs/context.md` (remove `--all`)
- Modify: `website/docs/multi-agent.md` (remove `--all`)
- Run: `./scripts/sync-docs.sh` (sync website/docs to docs/)

**Step 1: Update `WaitOptions` struct**

In `internal/cli/wait.go`, replace the `All` field:

```go
type WaitOptions struct {
    Timeout       time.Duration
    Scope         string
    Mention       string
    ForAgent      string    // Filter to messages for this agent
    ForAgentRole  string    // Filter to messages for this agent's role
    After         time.Time
    CallerAgentID string
}
```

**Step 2: Update `Wait()` to pass agent filters**

In the polling params construction in `Wait()`, add:

```go
if opts.ForAgent != "" {
    listParams["for_agent"] = opts.ForAgent
}
if opts.ForAgentRole != "" {
    listParams["for_agent_role"] = opts.ForAgentRole
}
```

Remove any references to `opts.All`.

**Step 3: Update `waitCmd` in main.go**

- Remove the `--all` flag definition
- Remove `allMsgs` variable and its usage
- Add `agentRole` resolution alongside existing `agentID` resolution
- Default `afterTime` to `time.Now()` unconditionally when `--after` is not specified
- Pass `ForAgent: agentID, ForAgentRole: agentRole` in `WaitOptions`
- Update the command `Long` description

**Step 4: Update `wait_test.go`**

Rename `TestWait_WithAllFlag` → `TestWait_AgentFiltered`. Update to verify `for_agent` and `for_agent_role` params are sent in the RPC request.

**Step 5: Update all template and doc files**

Search-and-replace across all files listed above:
- `thrum wait --all --timeout` → `thrum wait --timeout`
- `thrum wait --all` → `thrum wait`
- Remove any `--all` documentation bullets/rows

**Step 6: Sync docs**

Run: `cd /Users/leon/dev/opensource/thrum && ./scripts/sync-docs.sh`

**Step 7: Run all tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./... -count=1 2>&1 | tail -30`
Expected: PASS

**Step 8: Commit**

```bash
git add internal/cli/wait.go internal/cli/wait_test.go cmd/thrum/main.go \
  internal/cli/prime.go internal/context/context.go \
  internal/cli/templates/ CLAUDE.md claude-plugin/ \
  website/docs/ docs/
git commit -m "fix: remove --all from thrum wait, always filter by agent identity" -m "thrum wait now always filters messages to the calling agent (direct mentions, group messages, broadcasts). The --all flag was a no-op for filtering and caused all agents to wake for every message. Updated all templates, docs, and plugin files."
```

---

### Task 14: Name-Only Routing — Remove Role from Inbox Filter, Auto-Create Role Groups, Enforce Name≠Role

Role-based routing (`@implementer` reaching all agents with that role) is an implicit, invisible fan-out. Groups already handle multi-agent addressing explicitly. This task removes role from the inbox filter and moves role-based addressing into the group system where it's visible, manageable, and debuggable.

**Three changes:**

1. **Remove role from inbox matching** — `buildForAgentValues` returns only `[agentID]`, not `[agentID, agentRole]`. Messages are delivered based on agent name/ID and group membership only.

2. **Auto-create role groups on registration** — When an agent registers with role=implementer, auto-create a group named `"implementer"` (if it doesn't exist) with member `{type:"role", value:"implementer"}`. Now `@implementer` resolves as a group send through the existing group system. The group is visible in `thrum group list` and manageable via `thrum group member add/remove`.

3. **Enforce agent name ≠ role** — An agent cannot register with a name that matches any role (its own or existing), and a role cannot match any existing agent name. This prevents ambiguity where `@implementer` could mean both "the agent named implementer" and "the role group implementer."

**Files:**
- Modify: `internal/daemon/rpc/message.go:1036-1049` (buildForAgentValues)
- Modify: `internal/daemon/rpc/agent.go` (HandleRegister — add role group creation + name≠role validation)
- Modify: `internal/daemon/rpc/agent_test.go` (new tests)
- Modify: `internal/cli/inbox.go` (remove ForAgentRole from inbox params where it was used for routing)

**Step 1: Write failing test — role string no longer matches in inbox**

```go
func TestBuildForAgentValues_NameOnly(t *testing.T) {
    values := buildForAgentValues("impl_api", "implementer")
    // Should contain ONLY the agent ID, not the role
    if len(values) != 1 {
        t.Errorf("expected 1 value, got %d: %v", len(values), values)
    }
    if values[0] != "impl_api" {
        t.Errorf("expected 'impl_api', got %q", values[0])
    }
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/daemon/rpc/ -run TestBuildForAgentValues_NameOnly -v`
Expected: FAIL — currently returns `["impl_api", "implementer"]`

**Step 3: Remove role from `buildForAgentValues`**

In `internal/daemon/rpc/message.go`, simplify `buildForAgentValues`:

```go
func buildForAgentValues(forAgent, forAgentRole string) []string {
    if forAgent != "" {
        return []string{forAgent}
    }
    return nil
}
```

The `forAgentRole` parameter is kept in the signature for now (callers still pass it) but is no longer used for mention matching. Group-based routing (Part 2 of `buildForAgentClause`) still uses the role for group membership queries — that's the correct path for role-based fan-out.

**Step 4: Run test to verify it passes**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/daemon/rpc/ -run TestBuildForAgentValues_NameOnly -v`
Expected: PASS

**Step 5: Write failing test — registration creates role group**

```go
func TestHandleRegister_CreatesRoleGroup(t *testing.T) {
    // Register agent impl_api with role=implementer
    // Verify group "implementer" exists with member_type='role', member_value='implementer'
}
```

**Step 6: Add role group auto-creation to HandleRegister**

In `internal/daemon/rpc/agent.go`, after successful agent insertion in `registerAgent()`, add:

```go
// Auto-create role group if it doesn't exist
isGroup, _ := h.groupResolver.IsGroup(ctx, req.Role)
if !isGroup {
    // Create the role group
    _, err := h.state.DB().ExecContext(ctx,
        `INSERT INTO groups (group_id, name, description, created_at)
         VALUES (?, ?, ?, ?)`,
        "grp_role_"+req.Role, req.Role,
        fmt.Sprintf("Auto-created group for role '%s'", req.Role),
        time.Now().UTC().Format(time.RFC3339Nano),
    )
    if err == nil {
        // Add role-based membership
        _, _ = h.state.DB().ExecContext(ctx,
            `INSERT INTO group_members (group_id, member_type, member_value)
             VALUES (?, 'role', ?)`,
            "grp_role_"+req.Role, req.Role,
        )
    }
}
```

**Step 7: Write failing test — name≠role validation**

```go
func TestHandleRegister_RejectsNameEqualsRole(t *testing.T) {
    // Register agent with name="implementer" role="implementer"
    // Expect: error "agent name 'implementer' conflicts with role 'implementer'"

    // Register agent with name="coordinator" role="worker"
    // Then try to register agent with name="worker" role="tester"
    // Expect: error "agent name 'worker' conflicts with existing role 'worker'"

    // Register agent with name="alice" role="planner"
    // Then try to register agent with name="bob" role="alice"
    // Expect: error "role 'alice' conflicts with existing agent name 'alice'"
}
```

**Step 8: Add name≠role validation to HandleRegister**

In `internal/daemon/rpc/agent.go`, in `HandleRegister` before the existing conflict checks, add:

```go
// Validate: agent name must not collide with any role (including its own)
if req.Name != "" {
    // Check 1: name == own role
    if req.Name == req.Role {
        return nil, fmt.Errorf("agent name %q cannot be the same as its role — use a distinct name (e.g., '%s_main')", req.Name, req.Role)
    }

    // Check 2: name matches an existing role in the agents table
    var roleCount int
    _ = h.state.DB().QueryRowContext(ctx,
        `SELECT COUNT(*) FROM agents WHERE role = ?`, req.Name,
    ).Scan(&roleCount)
    if roleCount > 0 {
        return nil, fmt.Errorf("agent name %q conflicts with existing role '%s' — choose a different name", req.Name, req.Name)
    }
}

// Check 3: role matches an existing agent name/ID
var nameCount int
_ = h.state.DB().QueryRowContext(ctx,
    `SELECT COUNT(*) FROM agents WHERE agent_id = ?`, req.Role,
).Scan(&nameCount)
if nameCount > 0 {
    return nil, fmt.Errorf("role %q conflicts with existing agent name '%s' — choose a different role", req.Role, req.Role)
}
```

**Step 9: Run all tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/daemon/rpc/ -v -count=1`
Expected: PASS (some existing tests may need updating if they register with name==role)

**Step 10: Update Task 3 (check_messages) — ForAgentRole still needed for group membership**

Note: `ForAgentRole` is still passed in `ListMessagesRequest` and used by `buildForAgentClause` Part 2 (group membership subquery) — this is correct. The role is used to check `group_members WHERE member_type='role' AND member_value=?`, not for direct mention matching. No change needed to Task 3.

**Step 11: Commit**

```bash
git add internal/daemon/rpc/message.go internal/daemon/rpc/agent.go \
  internal/daemon/rpc/agent_test.go internal/cli/inbox.go
git commit -m "feat: name-only routing with auto role groups and name≠role validation" -m "Role strings are no longer used for direct mention matching in the inbox filter. Instead, role-based addressing works through auto-created role groups (visible in 'thrum group list'). Agent names cannot collide with role strings to prevent addressing ambiguity."
```

---

## Bug Summary

| # | Bug | Severity | Task |
|---|-----|----------|------|
| 1 | MCP `check_messages` misses name-directed messages | Critical | 3 |
| 2 | MCP `check_messages` misses broadcasts and group messages | Critical | 3 |
| 3 | Send to unknown recipient silently succeeds | Critical | 11 |
| 4 | Role-based routing is implicit and ambiguous | Critical | 14 |
| 5 | MCP send missing `CallerAgentID` (wrong sender attribution) | High | 2 |
| 6 | MCP mark-read uses wrong identity (wrong read-state) | High | 4 |
| 7 | Reply doesn't route back to sender | High | 5 |
| 8 | MCP waiter doesn't wake for broadcasts/groups | High | 12 |
| 9 | `thrum wait --all` is a footgun (no per-agent filtering) | High | 13 |
| 10 | Reply group scope reconstruction malformed | Medium | 6 |
| 11 | Always returns "delivered" status regardless of routing | Medium | 11 |
| 12 | MCP `check_messages` doesn't exclude self messages | Low | 3 |
| 13 | `list_agents` shows empty name | Low | 7 |
| 14 | `check_messages` omits priority field | Low | 8 |

## Files Modified

| File | Tasks |
|------|-------|
| `internal/mcp/tools.go` | 1, 2, 3, 4, 7, 8, 11 |
| `internal/mcp/tools_test.go` | 1 |
| `internal/mcp/types.go` | 11 |
| `internal/mcp/waiter.go` | 4, 12 |
| `internal/cli/message.go` | 5, 6 |
| `internal/cli/message_test.go` | 5, 6 |
| `internal/cli/send.go` | 11 |
| `internal/cli/wait.go` | 13 |
| `internal/cli/wait_test.go` | 13 |
| `internal/cli/prime.go` | 13 |
| `internal/context/context.go` | 13 |
| `internal/daemon/rpc/message.go` | 11, 14 |
| `internal/daemon/rpc/agent.go` | 14 |
| `internal/daemon/rpc/agent_test.go` | 14 |
| `internal/cli/inbox.go` | 14 |
| `cmd/thrum/main.go` | 11, 13 |
| `internal/mcp/routing_test.go` (new) | 9 |
| `CLAUDE.md` | 13 |
| `internal/cli/templates/` (5 files) | 13 |
| `website/docs/` (5 files) | 13 |
| `claude-plugin/` (3 files) | 13 |
