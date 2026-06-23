package state_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	gosync "sync"
	"testing"
	"time"

	. "github.com/leonletto/thrum/internal/sync/state"
)

// ownerResolver returns the daemonID that owns the given agentID.
// In tests, we build a simple map-backed stub.
func makeOwnerResolver(m map[string]string) func(agentID string) (string, error) {
	return func(agentID string) (string, error) {
		if id, ok := m[agentID]; ok {
			return id, nil
		}
		// unknown agent → return empty string; caller checks match
		return "", nil
	}
}

// branchResolver stub returns a fixed branch string.
func stubBranchResolver(branch string) func(ctx context.Context, worktree string) string {
	return func(_ context.Context, _ string) string {
		return branch
	}
}

// newAgentSnap returns a minimal AgentStateSnapshot for testing.
// daemonID is intentionally accepted (rather than dropped) to keep
// callsites self-documenting about which daemon is asserted to be the
// owner in the surrounding test — the snapshot itself doesn't carry
// the field; ownership is resolved at write time via the injected
// resolver func.
func newAgentSnap(agentID, _, worktree string) AgentStateSnapshot {
	return AgentStateSnapshot{
		AgentID:    agentID,
		Name:       "test-agent",
		Role:       "researcher",
		Module:     "sync",
		Display:    "Test Agent",
		Hostname:   "testhost",
		Worktree:   worktree,
		Branch:     "", // will be resolved by branchResolver
		Kind:       "agent",
		LastSeenAt: time.Now().UTC().Truncate(time.Second),
		Version:    1,
	}
}

// newBridgeSnap returns a minimal BridgeGroupStateSnapshot for testing.
func newBridgeSnap(groupID string) BridgeGroupStateSnapshot {
	return BridgeGroupStateSnapshot{
		GroupID:     groupID,
		Kind:        "bridge_group",
		BridgeKind:  "telegram",
		OwnerDaemon: "daemon-owner",
		Members:     []string{"agt_01", "agt_02"},
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
		LastSeenAt:  time.Now().UTC().Truncate(time.Second),
		Version:     1,
	}
}

// --- T-state-1: WriteAgent owner success and ErrNotOwner ---

func TestWriter_WriteAgent_OwnerSucceeds(t *testing.T) {
	syncDir := t.TempDir()
	daemonID := "daemon-abc"
	agentID := "agt_01"

	w := NewWriter(
		syncDir,
		daemonID,
		makeOwnerResolver(map[string]string{agentID: daemonID}),
		stubBranchResolver("main"),
	)

	snap := newAgentSnap(agentID, daemonID, "/some/worktree")
	if err := w.WriteAgent(context.Background(), snap); err != nil {
		t.Fatalf("WriteAgent failed: %v", err)
	}

	// Verify file exists and is valid JSON with expected fields.
	path := filepath.Join(syncDir, "state", "agents", agentID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state file not created: %v", err)
	}
	var got AgentStateSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("state file not valid JSON: %v", err)
	}
	if got.AgentID != agentID {
		t.Errorf("agent_id = %q, want %q", got.AgentID, agentID)
	}
	if got.Name != snap.Name {
		t.Errorf("name = %q, want %q", got.Name, snap.Name)
	}
}

func TestWriter_WriteAgent_NonOwnerReturnsErrNotOwner(t *testing.T) {
	syncDir := t.TempDir()
	callerDaemon := "daemon-caller"
	agentOwnerDaemon := "daemon-other"
	agentID := "agt_99"

	w := NewWriter(
		syncDir,
		callerDaemon,
		makeOwnerResolver(map[string]string{agentID: agentOwnerDaemon}),
		stubBranchResolver("main"),
	)

	snap := newAgentSnap(agentID, agentOwnerDaemon, "/worktree")
	err := w.WriteAgent(context.Background(), snap)
	if err == nil {
		t.Fatal("expected ErrNotOwner, got nil")
	}
	if err != ErrNotOwner {
		t.Errorf("expected ErrNotOwner, got %v", err)
	}

	// File must NOT exist.
	path := filepath.Join(syncDir, "state", "agents", agentID+".json")
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("state file must not be created when caller is not owner")
	}
}

// --- T-state-2: DeleteAgent idempotent on missing file ---

func TestWriter_DeleteAgent_Idempotent(t *testing.T) {
	syncDir := t.TempDir()
	daemonID := "daemon-abc"
	agentID := "agt_missing"

	w := NewWriter(
		syncDir,
		daemonID,
		makeOwnerResolver(map[string]string{agentID: daemonID}),
		stubBranchResolver("main"),
	)

	ctx := context.Background()
	// File doesn't exist — first delete should return nil.
	if err := w.DeleteAgent(ctx, agentID); err != nil {
		t.Fatalf("first DeleteAgent on missing file returned error: %v", err)
	}
	// Second delete still returns nil.
	if err := w.DeleteAgent(ctx, agentID); err != nil {
		t.Fatalf("second DeleteAgent on missing file returned error: %v", err)
	}
}

func TestWriter_DeleteAgent_RemovesExistingFile(t *testing.T) {
	syncDir := t.TempDir()
	daemonID := "daemon-abc"
	agentID := "agt_delete_me"

	w := NewWriter(
		syncDir,
		daemonID,
		makeOwnerResolver(map[string]string{agentID: daemonID}),
		stubBranchResolver("main"),
	)

	ctx := context.Background()
	snap := newAgentSnap(agentID, daemonID, "/wt")
	if err := w.WriteAgent(ctx, snap); err != nil {
		t.Fatalf("WriteAgent: %v", err)
	}
	path := filepath.Join(syncDir, "state", "agents", agentID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist after write: %v", err)
	}

	// DeleteAgent cannot call git rm in a non-git dir; but it must at minimum
	// remove the file from disk. We accept that git rm may fail here in a
	// non-repo tmpdir — the file-removal portion must still succeed.
	// Production syncDir is always a git worktree; this test covers the
	// disk-removal contract only.
	_ = w.DeleteAgent(ctx, agentID) // may return git rm error in tmpdir — ignore
	// The file must be gone regardless of git rm outcome in tmpdir.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file still exists after DeleteAgent")
	}
}

// --- T-state-3: ReadAllAgents returns latest fact per agent_id, ignores non-JSON ---

func TestReader_ReadAllAgents_LatestFactPerAgent(t *testing.T) {
	syncDir := t.TempDir()

	agentsDir := filepath.Join(syncDir, "state", "agents")
	if err := os.MkdirAll(agentsDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	agents := []AgentStateSnapshot{
		{AgentID: "agt_a", Name: "alpha", Kind: "agent", Version: 1, LastSeenAt: time.Now().UTC()},
		{AgentID: "agt_b", Name: "beta", Kind: "agent", Version: 1, LastSeenAt: time.Now().UTC()},
		{AgentID: "agt_c", Name: "gamma", Kind: "agent", Version: 1, LastSeenAt: time.Now().UTC()},
	}
	for _, a := range agents {
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(filepath.Join(agentsDir, a.AgentID+".json"), data, 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Plant a non-JSON file — must be ignored.
	if err := os.WriteFile(filepath.Join(agentsDir, "README"), []byte("not json"), 0600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	// Plant a .gitkeep — must be ignored.
	if err := os.WriteFile(filepath.Join(agentsDir, ".gitkeep"), []byte(""), 0600); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}

	r := NewReader(syncDir)
	got, err := r.ReadAllAgents(context.Background())
	if err != nil {
		t.Fatalf("ReadAllAgents: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 agents, got %d", len(got))
	}
	seen := make(map[string]bool)
	for _, g := range got {
		seen[g.AgentID] = true
	}
	for _, want := range []string{"agt_a", "agt_b", "agt_c"} {
		if !seen[want] {
			t.Errorf("missing agent %q in ReadAllAgents result", want)
		}
	}
}

func TestReader_ReadAllAgents_EmptyDir(t *testing.T) {
	syncDir := t.TempDir()
	// agents dir doesn't exist yet — must return empty slice, not error.
	r := NewReader(syncDir)
	got, err := r.ReadAllAgents(context.Background())
	if err != nil {
		t.Fatalf("ReadAllAgents on missing dir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 agents, got %d", len(got))
	}
}

func TestReader_ReadAgent_Found(t *testing.T) {
	syncDir := t.TempDir()
	agentsDir := filepath.Join(syncDir, "state", "agents")
	if err := os.MkdirAll(agentsDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	snap := AgentStateSnapshot{AgentID: "agt_z", Name: "zeta", Kind: "agent", Version: 1, LastSeenAt: time.Now().UTC()}
	data, _ := json.Marshal(snap)
	_ = os.WriteFile(filepath.Join(agentsDir, "agt_z.json"), data, 0600)

	r := NewReader(syncDir)
	got, err := r.ReadAgent(context.Background(), "agt_z")
	if err != nil {
		t.Fatalf("ReadAgent: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.AgentID != "agt_z" {
		t.Errorf("AgentID = %q, want %q", got.AgentID, "agt_z")
	}
}

func TestReader_ReadAgent_Missing(t *testing.T) {
	syncDir := t.TempDir()
	r := NewReader(syncDir)
	got, err := r.ReadAgent(context.Background(), "agt_notexist")
	if err != nil {
		t.Fatalf("ReadAgent on missing: %v", err)
	}
	if got != nil {
		t.Error("expected nil for missing agent")
	}
}

// --- T-state-4: Branch field populated via injected resolver ---

func TestWriter_WriteAgent_BranchFieldPopulated(t *testing.T) {
	syncDir := t.TempDir()
	daemonID := "daemon-abc"
	agentID := "agt_branch"

	w := NewWriter(
		syncDir,
		daemonID,
		makeOwnerResolver(map[string]string{agentID: daemonID}),
		stubBranchResolver("feat/x"),
	)

	snap := newAgentSnap(agentID, daemonID, "/some/worktree")
	snap.Branch = "" // must be resolved, not passed through
	if err := w.WriteAgent(context.Background(), snap); err != nil {
		t.Fatalf("WriteAgent: %v", err)
	}

	path := filepath.Join(syncDir, "state", "agents", agentID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var got AgentStateSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Branch != "feat/x" {
		t.Errorf("Branch = %q, want %q", got.Branch, "feat/x")
	}
}

// --- T-state-5: Concurrent same-daemon writes, race-free ---

func TestWriter_WriteAgent_ConcurrentSameAgent(t *testing.T) {
	syncDir := t.TempDir()
	daemonID := "daemon-concurrent"
	agentID := "agt_concurrent"

	w := NewWriter(
		syncDir,
		daemonID,
		makeOwnerResolver(map[string]string{agentID: daemonID}),
		stubBranchResolver("main"),
	)

	const n = 20
	var wg gosync.WaitGroup
	wg.Add(n)
	ctx := context.Background()
	for i := 0; i < n; i++ {
		go func(seq int) {
			defer wg.Done()
			snap := newAgentSnap(agentID, daemonID, "/wt")
			snap.Version = seq
			_ = w.WriteAgent(ctx, snap)
		}(i)
	}
	wg.Wait()

	// Final file must be valid JSON — no corruption.
	path := filepath.Join(syncDir, "state", "agents", agentID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after concurrent writes: %v", err)
	}
	var got AgentStateSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("file corrupted after concurrent writes: %v", err)
	}
	if got.AgentID != agentID {
		t.Errorf("AgentID corrupted: got %q, want %q", got.AgentID, agentID)
	}
}

// =============================================================================
// BridgeGroup mirror tests
// =============================================================================

// --- BridgeGroup T-state-1 mirror: WriteBridgeGroup owner success and ErrNotOwner ---

func TestWriter_WriteBridgeGroup_OwnerSucceeds(t *testing.T) {
	syncDir := t.TempDir()
	daemonID := "daemon-bridge"
	groupID := "tg:family-chat"

	w := NewWriter(
		syncDir,
		daemonID,
		makeOwnerResolver(map[string]string{}),
		stubBranchResolver("main"),
	)
	// bridge owner resolver is separate; we inject it via a second NewWriter
	// variant. But per the dispatch: the BridgeGroup owner is OwnerDaemon
	// inside the snapshot — checked against the Writer's daemonID.

	snap := newBridgeSnap(groupID)
	snap.OwnerDaemon = daemonID
	if err := w.WriteBridgeGroup(context.Background(), snap); err != nil {
		t.Fatalf("WriteBridgeGroup failed: %v", err)
	}

	path := filepath.Join(syncDir, "state", "bridge-groups", groupID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("bridge-group file not created: %v", err)
	}
	var got BridgeGroupStateSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("bridge-group file not valid JSON: %v", err)
	}
	if got.GroupID != groupID {
		t.Errorf("group_id = %q, want %q", got.GroupID, groupID)
	}
	if got.BridgeKind != "telegram" {
		t.Errorf("bridge_kind = %q, want %q", got.BridgeKind, "telegram")
	}
}

func TestWriter_WriteBridgeGroup_NonOwnerReturnsErrNotOwner(t *testing.T) {
	syncDir := t.TempDir()
	callerDaemon := "daemon-caller"
	groupID := "tg:work-chat"

	w := NewWriter(
		syncDir,
		callerDaemon,
		makeOwnerResolver(map[string]string{}),
		stubBranchResolver("main"),
	)

	snap := newBridgeSnap(groupID)
	snap.OwnerDaemon = "daemon-other" // different from callerDaemon
	err := w.WriteBridgeGroup(context.Background(), snap)
	if err == nil {
		t.Fatal("expected ErrNotOwner, got nil")
	}
	if err != ErrNotOwner {
		t.Errorf("expected ErrNotOwner, got %v", err)
	}

	path := filepath.Join(syncDir, "state", "bridge-groups", groupID+".json")
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("bridge-group file must not be created when caller is not owner")
	}
}

// --- BridgeGroup T-state-2 mirror: DeleteBridgeGroup idempotent on missing file ---

func TestWriter_DeleteBridgeGroup_Idempotent(t *testing.T) {
	syncDir := t.TempDir()
	daemonID := "daemon-bridge"
	groupID := "tg:missing-group"

	w := NewWriter(
		syncDir,
		daemonID,
		makeOwnerResolver(map[string]string{}),
		stubBranchResolver("main"),
	)

	// Group is owned by this daemon (owner check via snap.OwnerDaemon).
	// For delete, we check ownership by reading the file first; if file doesn't
	// exist, we skip the ownership check and return nil (idempotency).
	ctx := context.Background()
	if err := w.DeleteBridgeGroup(ctx, groupID); err != nil {
		t.Fatalf("first DeleteBridgeGroup on missing file returned error: %v", err)
	}
	if err := w.DeleteBridgeGroup(ctx, groupID); err != nil {
		t.Fatalf("second DeleteBridgeGroup on missing file returned error: %v", err)
	}
}

// --- BridgeGroup T-state-3 mirror: ReadAllBridgeGroups returns all groups, ignores non-JSON ---

func TestReader_ReadAllBridgeGroups_All(t *testing.T) {
	syncDir := t.TempDir()
	bgDir := filepath.Join(syncDir, "state", "bridge-groups")
	if err := os.MkdirAll(bgDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	groups := []BridgeGroupStateSnapshot{
		newBridgeSnap("tg:alpha"),
		newBridgeSnap("tg:beta"),
	}
	for _, g := range groups {
		data, _ := json.Marshal(g)
		name := filepath.Join(bgDir, g.GroupID+".json")
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// Non-JSON file must be ignored.
	_ = os.WriteFile(filepath.Join(bgDir, "not-json.txt"), []byte("nope"), 0600)

	r := NewReader(syncDir)
	got, err := r.ReadAllBridgeGroups(context.Background())
	if err != nil {
		t.Fatalf("ReadAllBridgeGroups: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 groups, got %d", len(got))
	}
}

func TestReader_ReadAllBridgeGroups_EmptyDir(t *testing.T) {
	syncDir := t.TempDir()
	r := NewReader(syncDir)
	got, err := r.ReadAllBridgeGroups(context.Background())
	if err != nil {
		t.Fatalf("ReadAllBridgeGroups on missing dir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 groups, got %d", len(got))
	}
}

// --- BridgeGroup concurrent writes ---

func TestWriter_WriteBridgeGroup_ConcurrentSameDaemon(t *testing.T) {
	syncDir := t.TempDir()
	daemonID := "daemon-bridge"
	groupID := "tg:concurrent"

	w := NewWriter(
		syncDir,
		daemonID,
		makeOwnerResolver(map[string]string{}),
		stubBranchResolver("main"),
	)

	const n = 20
	var wg gosync.WaitGroup
	wg.Add(n)
	ctx := context.Background()
	for i := 0; i < n; i++ {
		go func(seq int) {
			defer wg.Done()
			snap := newBridgeSnap(groupID)
			snap.OwnerDaemon = daemonID
			snap.Version = seq
			_ = w.WriteBridgeGroup(ctx, snap)
		}(i)
	}
	wg.Wait()

	path := filepath.Join(syncDir, "state", "bridge-groups", groupID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after concurrent writes: %v", err)
	}
	var got BridgeGroupStateSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("file corrupted after concurrent writes: %v", err)
	}
	if got.GroupID != groupID {
		t.Errorf("GroupID corrupted: got %q, want %q", got.GroupID, groupID)
	}
}
