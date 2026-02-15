package testgen

import (
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/schema"
)

func TestGenerateAgents(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	agents := generateAgents(rng, 50)

	if len(agents) != 50 {
		t.Fatalf("expected 50 agents, got %d", len(agents))
	}

	// Verify determinism
	rng2 := rand.New(rand.NewSource(42))
	agents2 := generateAgents(rng2, 50)
	for i := range agents {
		if agents[i].AgentID != agents2[i].AgentID {
			t.Errorf("agent %d not deterministic: %s vs %s", i, agents[i].AgentID, agents2[i].AgentID)
		}
	}

	// Verify role distribution
	roleCounts := make(map[string]int)
	for _, a := range agents {
		roleCounts[a.Role]++
	}
	for _, role := range roles {
		if roleCounts[role] == 0 {
			t.Errorf("role %s has no agents", role)
		}
	}
}

func TestGenerateSessions(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	agents := generateAgents(rng, 50)
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sessions := generateSessions(rng, agents, 100, baseTime)

	if len(sessions) != 100 {
		t.Fatalf("expected 100 sessions, got %d", len(sessions))
	}

	// Verify some sessions are ended
	var endedCount int
	for _, s := range sessions {
		if s.EndedAt != nil {
			endedCount++
		}
	}
	if endedCount == 0 || endedCount == 100 {
		t.Errorf("expected mix of ended/active sessions, got %d ended", endedCount)
	}
}

func TestGenerateMessages(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	agents := generateAgents(rng, 50)
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sessions := generateSessions(rng, agents, 100, baseTime)
	messages := generateMessages(rng, agents, sessions, 10000, baseTime)

	if len(messages) != 10000 {
		t.Fatalf("expected 10000 messages, got %d", len(messages))
	}

	// Verify scope distribution
	var broadcast, directed, moduleScoped int
	for _, m := range messages {
		switch len(m.Scopes) {
		case 0:
			broadcast++
		default:
			if m.Scopes[0].Type == "agent" {
				directed++
			} else {
				moduleScoped++
			}
		}
	}

	// Should be roughly 60/30/10
	if broadcast < 5000 || broadcast > 7000 {
		t.Errorf("unexpected broadcast count: %d (expected ~6000)", broadcast)
	}
	t.Logf("Distribution: broadcast=%d directed=%d module=%d", broadcast, directed, moduleScoped)
}

func TestGenerateGroups(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	agents := generateAgents(rng, 50)
	groups := generateGroups(rng, agents, 20)

	if len(groups) != 20 {
		t.Fatalf("expected 20 groups, got %d", len(groups))
	}

	// Verify we have role-based, module-based, and nested groups
	var roleGroups, moduleGroups, nestedGroups int
	for _, g := range groups {
		for _, m := range g.Members {
			switch m.MemberType {
			case "role":
				roleGroups++
			case "group":
				nestedGroups++
			}
		}
		if len(g.Members) > 0 && g.Members[0].MemberType == "agent" {
			moduleGroups++
		}
	}
	if roleGroups == 0 {
		t.Error("expected some role-based groups")
	}
	if nestedGroups == 0 {
		t.Error("expected some nested groups")
	}
	t.Logf("Group types: role-based members=%d, module/agent groups=%d, nested group members=%d", roleGroups, moduleGroups, nestedGroups)
}

func TestGenerateEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Seed = 42

	if err := Generate(tmpDir, cfg); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify directory structure
	for _, subdir := range []string{"var", "identities", "context", "messages"} {
		path := tmpDir + "/" + subdir
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("missing directory %s: %v", subdir, err)
		} else if !info.IsDir() {
			t.Errorf("%s is not a directory", subdir)
		}
	}

	// Verify identity files count
	identities, err := os.ReadDir(tmpDir + "/identities")
	if err != nil {
		t.Fatalf("read identities dir: %v", err)
	}
	if len(identities) != 50 {
		t.Errorf("expected 50 identity files, got %d", len(identities))
	}

	// Verify SQLite counts
	db, err := schema.OpenDB(tmpDir + "/var/messages.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	counts := map[string]int{
		"agents":   0,
		"sessions": 0,
		"messages": 0,
		"groups":   0,
	}
	for table := range counts {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Errorf("count %s: %v", table, err)
		}
		counts[table] = count
	}

	if counts["agents"] != 50 {
		t.Errorf("expected 50 agents, got %d", counts["agents"])
	}
	if counts["sessions"] != 100 {
		t.Errorf("expected 100 sessions, got %d", counts["sessions"])
	}
	if counts["messages"] != 10000 {
		t.Errorf("expected 10000 messages, got %d", counts["messages"])
	}
	if counts["groups"] != 20 {
		t.Errorf("expected 20 groups, got %d", counts["groups"])
	}

	t.Logf("SQLite counts: %+v", counts)
}

func TestCompressToTarGz(t *testing.T) {
	// Generate fixture
	srcDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.NumAgents = 5
	cfg.NumMessages = 100
	cfg.NumSessions = 10
	cfg.NumGroups = 3

	if err := Generate(srcDir, cfg); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Compress
	outputPath := t.TempDir() + "/fixture.tar.gz"
	if err := CompressToTarGz(srcDir, outputPath); err != nil {
		t.Fatalf("CompressToTarGz failed: %v", err)
	}

	// Verify file exists and has content
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("output file missing: %v", err)
	}
	if info.Size() == 0 {
		t.Error("output file is empty")
	}
	t.Logf("Compressed fixture size: %d bytes", info.Size())
}
