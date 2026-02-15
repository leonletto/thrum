# Resilience Testing Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a production-realistic test fixture generator and comprehensive resilience test suite for Thrum.

**Architecture:** A Go generator (`internal/testgen/`) creates a deterministic `.thrum/` directory with 50 agents, 10K messages, and full JSONL+SQLite state. Test files in `tests/resilience/` exercise CLI, RPC, concurrency, recovery, and multi-daemon scenarios against this fixture. Build-tag-gated so normal `go test ./...` skips them.

**Tech Stack:** Go testing, `database/sql` + `modernc.org/sqlite`, `archive/tar` + `compress/gzip`, existing `internal/schema`, `internal/daemon/state`, `internal/daemon/rpc`, `internal/identity`, `internal/types`, `internal/projection` packages.

**Design doc:** `docs/plans/2026-02-15-resilience-testing-design.md`

---

### Task 1: Create testgen package scaffold

**Files:**
- Create: `internal/testgen/generator.go`
- Create: `internal/testgen/cmd/main.go`

**Step 1: Create the generator package with core types and Generate function signature**

```go
// internal/testgen/generator.go
package testgen

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/types"
)

// Config controls the generated fixture volumes.
type Config struct {
	Seed          int64
	NumAgents     int // default 50
	NumMessages   int // default 10000
	NumSessions   int // default 100
	NumGroups     int // default 20
	NumEvents     int // default 500
}

// DefaultConfig returns production-realistic defaults.
func DefaultConfig() Config {
	return Config{
		Seed:        42,
		NumAgents:   50,
		NumMessages: 10000,
		NumSessions: 100,
		NumGroups:   20,
		NumEvents:   500,
	}
}

// agentInfo holds generated agent data.
type agentInfo struct {
	AgentID  string
	Name     string
	Kind     string
	Role     string
	Module   string
	Display  string
	Hostname string
}

// sessionInfo holds generated session data.
type sessionInfo struct {
	SessionID string
	AgentID   string
	StartedAt time.Time
	EndedAt   *time.Time
}

// Generate creates a complete .thrum directory at outputDir.
func Generate(outputDir string, cfg Config) error {
	// Implementation in subsequent steps
	return nil
}

// CompressToTarGz compresses a directory into a .tar.gz file.
func CompressToTarGz(sourceDir, outputPath string) error {
	// Implementation in subsequent steps
	return nil
}
```

**Step 2: Create the CLI entry point**

```go
// internal/testgen/cmd/main.go
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/leonletto/thrum/internal/testgen"
)

func main() {
	output := flag.String("output", "", "Output path for .tar.gz fixture")
	seed := flag.Int64("seed", 42, "Random seed for deterministic generation")
	flag.Parse()

	if *output == "" {
		fmt.Fprintln(os.Stderr, "Usage: testgen -output <path.tar.gz> [-seed N]")
		os.Exit(1)
	}

	// Generate to temp directory
	tmpDir, err := os.MkdirTemp("", "thrum-testgen-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	thrumDir := filepath.Join(tmpDir, ".thrum")
	cfg := testgen.DefaultConfig()
	cfg.Seed = *seed

	fmt.Fprintf(os.Stderr, "Generating fixture (seed=%d)...\n", cfg.Seed)
	if err := testgen.Generate(thrumDir, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Compressing to %s...\n", *output)
	if err := testgen.CompressToTarGz(thrumDir, *output); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Done.\n")
}
```

**Step 3: Verify it compiles**

Run: `cd /Users/leon/dev/opensource/thrum && go build ./internal/testgen/cmd/`
Expected: No errors

**Step 4: Commit**

```bash
git add internal/testgen/generator.go internal/testgen/cmd/main.go
git commit -m "feat: scaffold testgen package for resilience fixture generation"
```

---

### Task 2: Implement agent and session generation

**Files:**
- Modify: `internal/testgen/generator.go`

**Step 1: Implement agent generation**

Add these functions to `generator.go`:

```go
var roles = []string{"coordinator", "implementer", "reviewer", "planner", "tester"}
var modules = []string{"main", "daemon", "relay", "sync", "cli", "ui", "auth", "groups"}

// generateAgents creates deterministic agent data.
func generateAgents(rng *rand.Rand, n int) []agentInfo {
	agents := make([]agentInfo, 0, n)
	for i := 0; i < n; i++ {
		role := roles[i%len(roles)]
		module := modules[i%len(modules)]
		suffix := fmt.Sprintf("%04d", i)
		name := fmt.Sprintf("%s_%s", role, suffix)
		agents = append(agents, agentInfo{
			AgentID:  name,
			Name:     name,
			Kind:     "agent",
			Role:     role,
			Module:   module,
			Display:  fmt.Sprintf("Agent %d (%s)", i, role),
			Hostname: fmt.Sprintf("host-%d.local", i%5),
		})
	}
	return agents
}

// generateSessions creates deterministic session data.
func generateSessions(rng *rand.Rand, agents []agentInfo, n int, baseTime time.Time) []sessionInfo {
	sessions := make([]sessionInfo, 0, n)
	for i := 0; i < n; i++ {
		agent := agents[i%len(agents)]
		startOffset := time.Duration(rng.Intn(72)) * time.Hour // spread over 3 days
		start := baseTime.Add(startOffset)
		duration := time.Duration(5+rng.Intn(475)) * time.Minute // 5min to 8hrs

		s := sessionInfo{
			SessionID: fmt.Sprintf("ses_%06d", i),
			AgentID:   agent.AgentID,
			StartedAt: start,
		}
		// 80% of sessions are ended
		if rng.Float64() < 0.8 {
			end := start.Add(duration)
			s.EndedAt = &end
		}
		sessions = append(sessions, s)
	}
	return sessions
}
```

**Step 2: Write tests for the generators**

Create `internal/testgen/generator_test.go`:

```go
package testgen

import (
	"math/rand"
	"testing"
	"time"
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
```

**Step 3: Run tests**

Run: `go test ./internal/testgen/ -v -run TestGenerate`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/testgen/
git commit -m "feat: implement agent and session generation for testgen"
```

---

### Task 3: Implement message generation

**Files:**
- Modify: `internal/testgen/generator.go`

**Step 1: Add message generation**

```go
// messageInfo holds generated message data.
type messageInfo struct {
	MessageID string
	AgentID   string
	SessionID string
	Timestamp time.Time
	Content   string
	Format    string
	Scopes    []types.Scope
}

var messageTemplates = []string{
	"Starting work on %s module",
	"Completed task in %s - all tests passing",
	"Need review on changes to %s",
	"Found a bug in %s, investigating",
	"Deploying %s changes to staging",
	"Status update: %s refactoring 80%% complete",
	"Question about %s architecture",
	"Blocking issue in %s resolved",
	"PR ready for %s feature",
	"Meeting notes for %s planning",
}

// generateMessages creates deterministic message data.
func generateMessages(rng *rand.Rand, agents []agentInfo, sessions []sessionInfo, n int, baseTime time.Time) []messageInfo {
	messages := make([]messageInfo, 0, n)
	for i := 0; i < n; i++ {
		agent := agents[rng.Intn(len(agents))]
		// Find a session for this agent
		var session sessionInfo
		for _, s := range sessions {
			if s.AgentID == agent.AgentID {
				session = s
				break
			}
		}
		if session.SessionID == "" {
			session = sessions[i%len(sessions)]
		}

		timestamp := baseTime.Add(time.Duration(i) * 30 * time.Second) // ~30s between messages
		template := messageTemplates[rng.Intn(len(messageTemplates))]
		content := fmt.Sprintf(template, agent.Module)

		msg := messageInfo{
			MessageID: fmt.Sprintf("msg_%08d", i),
			AgentID:   agent.AgentID,
			SessionID: session.SessionID,
			Timestamp: timestamp,
			Content:   content,
			Format:    "markdown",
		}

		// 60% broadcast (no scopes), 30% directed (agent scope), 10% module-scoped
		r := rng.Float64()
		switch {
		case r < 0.6:
			// broadcast - no scopes
		case r < 0.9:
			// directed to random agent
			target := agents[rng.Intn(len(agents))]
			msg.Scopes = []types.Scope{{Type: "agent", Value: target.AgentID}}
		default:
			// module-scoped
			msg.Scopes = []types.Scope{{Type: "module", Value: modules[rng.Intn(len(modules))]}}
		}

		messages = append(messages, msg)
	}
	return messages
}
```

**Step 2: Add test for message generation**

Add to `generator_test.go`:

```go
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
```

**Step 3: Run tests**

Run: `go test ./internal/testgen/ -v -run TestGenerate`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/testgen/
git commit -m "feat: implement message generation for testgen"
```

---

### Task 4: Implement group generation

**Files:**
- Modify: `internal/testgen/generator.go`

**Step 1: Add group generation**

```go
// groupInfo holds generated group data.
type groupInfo struct {
	GroupID     string
	Name        string
	Description string
	CreatedBy   string
	Members     []groupMember
}

type groupMember struct {
	MemberType  string // "agent", "role", "group"
	MemberValue string
}

// generateGroups creates deterministic group data with nesting.
func generateGroups(rng *rand.Rand, agents []agentInfo, n int) []groupInfo {
	groups := make([]groupInfo, 0, n)

	// Create role-based groups (5)
	for i, role := range roles {
		g := groupInfo{
			GroupID:     fmt.Sprintf("grp_%04d", i),
			Name:        role + "s",
			Description: fmt.Sprintf("All %s agents", role),
			CreatedBy:   agents[0].AgentID,
			Members:     []groupMember{{MemberType: "role", MemberValue: role}},
		}
		groups = append(groups, g)
	}

	// Create module-based groups (8)
	for i, mod := range modules {
		g := groupInfo{
			GroupID:     fmt.Sprintf("grp_%04d", len(roles)+i),
			Name:        "team-" + mod,
			Description: fmt.Sprintf("Team working on %s", mod),
			CreatedBy:   agents[0].AgentID,
		}
		// Add 3-5 random agents from this module
		for _, a := range agents {
			if a.Module == mod {
				g.Members = append(g.Members, groupMember{MemberType: "agent", MemberValue: a.AgentID})
			}
		}
		groups = append(groups, g)
	}

	// Create nested groups (3)
	for i := 0; i < 3 && len(groups) < n; i++ {
		g := groupInfo{
			GroupID:     fmt.Sprintf("grp_%04d", len(roles)+len(modules)+i),
			Name:        fmt.Sprintf("super-team-%d", i),
			Description: fmt.Sprintf("Nested group %d containing other groups", i),
			CreatedBy:   agents[0].AgentID,
			Members: []groupMember{
				{MemberType: "group", MemberValue: groups[rng.Intn(len(roles))].Name},
				{MemberType: "group", MemberValue: groups[len(roles)+rng.Intn(len(modules))].Name},
			},
		}
		groups = append(groups, g)
	}

	// Fill remaining with ad-hoc agent groups
	for len(groups) < n {
		idx := len(groups)
		g := groupInfo{
			GroupID:     fmt.Sprintf("grp_%04d", idx),
			Name:        fmt.Sprintf("adhoc-%d", idx),
			Description: "Ad-hoc project group",
			CreatedBy:   agents[rng.Intn(len(agents))].AgentID,
		}
		// Add 2-6 random agents
		memberCount := 2 + rng.Intn(5)
		seen := make(map[string]bool)
		for j := 0; j < memberCount; j++ {
			a := agents[rng.Intn(len(agents))]
			if !seen[a.AgentID] {
				g.Members = append(g.Members, groupMember{MemberType: "agent", MemberValue: a.AgentID})
				seen[a.AgentID] = true
			}
		}
		groups = append(groups, g)
	}

	return groups
}
```

**Step 2: Run tests**

Run: `go test ./internal/testgen/ -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/testgen/
git commit -m "feat: implement group generation with nesting for testgen"
```

---

### Task 5: Implement full Generate function (JSONL + SQLite + identities)

**Files:**
- Modify: `internal/testgen/generator.go`

**Step 1: Implement the Generate function**

This is the core function that ties everything together. It needs to:
1. Create directory structure
2. Generate all data
3. Write identity JSON files
4. Write context markdown files
5. Write JSONL event files (sharded)
6. Initialize SQLite and populate all tables
7. Write config.json

Key API usage:
- `schema.OpenDB(dbPath)` + `schema.InitDB(db)` for database
- Identity files follow `config.IdentityFile` format (version 2)
- JSONL uses `json.NewEncoder` writing one event per line
- Messages sharded to `messages/{agent_name}.jsonl`
- Non-message events go to `events.jsonl`
- SQLite populated via direct SQL INSERT (not `state.WriteEvent` to avoid JSONL double-write)

The Generate function should:

```go
func Generate(outputDir string, cfg Config) error {
	rng := rand.New(rand.NewSource(cfg.Seed))
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create directory structure
	dirs := []string{
		outputDir,
		filepath.Join(outputDir, "var"),
		filepath.Join(outputDir, "identities"),
		filepath.Join(outputDir, "context"),
		filepath.Join(outputDir, "messages"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0750); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	// Generate data
	agents := generateAgents(rng, cfg.NumAgents)
	sessions := generateSessions(rng, agents, cfg.NumSessions, baseTime)
	messages := generateMessages(rng, agents, sessions, cfg.NumMessages, baseTime)
	groups := generateGroups(rng, agents, cfg.NumGroups)

	// 1. Write config.json
	if err := writeConfigJSON(outputDir); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// 2. Write identity files
	if err := writeIdentityFiles(outputDir, agents); err != nil {
		return fmt.Errorf("write identities: %w", err)
	}

	// 3. Write context files
	if err := writeContextFiles(outputDir, agents); err != nil {
		return fmt.Errorf("write contexts: %w", err)
	}

	// 4. Write JSONL event files
	if err := writeJSONLEvents(outputDir, agents, sessions, messages, groups, baseTime); err != nil {
		return fmt.Errorf("write JSONL: %w", err)
	}

	// 5. Initialize and populate SQLite
	if err := populateSQLite(outputDir, agents, sessions, messages, groups, rng, baseTime); err != nil {
		return fmt.Errorf("populate SQLite: %w", err)
	}

	return nil
}
```

Each helper function (`writeConfigJSON`, `writeIdentityFiles`, `writeContextFiles`, `writeJSONLEvents`, `populateSQLite`) should be implemented as separate private functions.

Key details for `populateSQLite`:
- Use `schema.OpenDB()` then `schema.InitDB()` for database setup
- INSERT agents directly: `INSERT INTO agents (agent_id, kind, role, module, display, hostname, registered_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
- INSERT sessions: `INSERT INTO sessions (session_id, agent_id, started_at, ended_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`
- INSERT messages: `INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content) VALUES (?, ?, ?, ?, ?, ?)`
- INSERT message_scopes for scoped messages
- INSERT groups and group_members
- INSERT message_reads for ~70% of messages (random readers)
- INSERT subscriptions (one per active session)

Key details for `writeJSONLEvents`:
- Agent register events → `events.jsonl`
- Session start/end events → `events.jsonl`
- Group create/member.add events → `events.jsonl`
- Message create events → `messages/{agent_name}.jsonl` (sharded by agent)
- Each event needs `event_id`, `timestamp`, `type`, `v: 1` fields

Key details for `writeIdentityFiles`:
- Format: `config.IdentityFile` struct → JSON
- Path: `identities/{agent_name}.json`

Key details for `writeContextFiles`:
- Path: `context/{agent_name}.md`
- Content: realistic session summary markdown

**Step 2: Run the generator end-to-end**

Run: `cd /Users/leon/dev/opensource/thrum && go run ./internal/testgen/cmd -output /tmp/test-fixture.tar.gz -seed 42`
Expected: Completes without error, creates tar.gz file

**Step 3: Verify the generated fixture**

Run:
```bash
mkdir -p /tmp/test-fixture && tar xzf /tmp/test-fixture.tar.gz -C /tmp/test-fixture
ls -la /tmp/test-fixture/.thrum/
ls /tmp/test-fixture/.thrum/identities/ | wc -l   # Should be 50
ls /tmp/test-fixture/.thrum/messages/ | wc -l      # Should be ~50 (one per agent with messages)
sqlite3 /tmp/test-fixture/.thrum/var/messages.db "SELECT COUNT(*) FROM agents"   # 50
sqlite3 /tmp/test-fixture/.thrum/var/messages.db "SELECT COUNT(*) FROM messages" # 10000
sqlite3 /tmp/test-fixture/.thrum/var/messages.db "SELECT COUNT(*) FROM sessions" # 100
sqlite3 /tmp/test-fixture/.thrum/var/messages.db "SELECT COUNT(*) FROM groups"   # 20
```

**Step 4: Commit**

```bash
git add internal/testgen/
git commit -m "feat: implement full testgen Generate function with JSONL, SQLite, and identity files"
```

---

### Task 6: Implement CompressToTarGz and generate checked-in fixture

**Files:**
- Modify: `internal/testgen/generator.go`
- Create: `tests/resilience/testdata/` (directory)
- Create: `tests/resilience/testdata/thrum-fixture.tar.gz`

**Step 1: Implement CompressToTarGz**

```go
func CompressToTarGz(sourceDir, outputPath string) error {
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer outFile.Close()

	gzWriter := gzip.NewWriter(outFile)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	baseDir := filepath.Dir(sourceDir)
	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tarWriter, f)
		return err
	})
}
```

**Step 2: Generate the checked-in fixture**

Run:
```bash
mkdir -p tests/resilience/testdata
go run ./internal/testgen/cmd -output tests/resilience/testdata/thrum-fixture.tar.gz -seed 42
ls -lh tests/resilience/testdata/thrum-fixture.tar.gz
```

**Step 3: Commit**

```bash
git add internal/testgen/ tests/resilience/testdata/thrum-fixture.tar.gz
git commit -m "feat: add tar.gz compression and generate resilience test fixture"
```

---

### Task 7: Create test infrastructure (fixture_test.go)

**Files:**
- Create: `tests/resilience/doc.go`
- Create: `tests/resilience/fixture_test.go`

**Step 1: Create doc.go with build tag and go:generate**

```go
//go:build resilience

// Package resilience contains performance and resilience tests for Thrum.
// Run: go test -tags=resilience ./tests/resilience/ -v -timeout 10m
package resilience

//go:generate go run ../../internal/testgen/cmd -output testdata/thrum-fixture.tar.gz -seed 42
```

**Step 2: Create fixture_test.go with shared helpers**

```go
//go:build resilience

package resilience

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/subscriptions"
)

const fixturePath = "testdata/thrum-fixture.tar.gz"

// setupFixture extracts the fixture to a temp directory and returns the .thrum path.
func setupFixture(t *testing.T) string {
	t.Helper()

	if _, err := os.Stat(fixturePath); os.IsNotExist(err) {
		t.Fatalf("Fixture not found at %s. Run: go generate -tags=resilience ./tests/resilience/...", fixturePath)
	}

	tmpDir := t.TempDir()
	if err := extractTarGz(fixturePath, tmpDir); err != nil {
		t.Fatalf("Failed to extract fixture: %v", err)
	}

	thrumDir := filepath.Join(tmpDir, ".thrum")
	if _, err := os.Stat(thrumDir); os.IsNotExist(err) {
		t.Fatalf("Extracted fixture missing .thrum directory")
	}

	return thrumDir
}

// extractTarGz extracts a .tar.gz file to the destination directory.
func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dst, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
				return err
			}
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		}
	}
	return nil
}

// startTestDaemon starts a daemon against the fixture's thrumDir.
// Returns the state, server, and socket path. Registers cleanup.
func startTestDaemon(t *testing.T, thrumDir string) (*state.State, *daemon.Server, string) {
	t.Helper()

	socketPath := filepath.Join(thrumDir, "var", "thrum.sock")

	// Remove stale socket if present from fixture
	os.Remove(socketPath)

	// Create state (this opens the pre-populated SQLite DB)
	st, err := state.NewState(thrumDir, thrumDir, "test-resilience")
	if err != nil {
		t.Fatalf("NewState failed: %v", err)
	}

	// Create server and register all handlers
	server := daemon.NewServer(socketPath)

	startTime := time.Now()
	healthHandler := rpc.NewHealthHandler(startTime, "test-resilience", "test-repo")
	server.RegisterHandler("health", healthHandler.Handle)

	agentHandler := rpc.NewAgentHandler(st)
	server.RegisterHandler("agent.register", agentHandler.HandleRegister)
	server.RegisterHandler("agent.list", agentHandler.HandleList)

	sessionHandler := rpc.NewSessionHandler(st)
	server.RegisterHandler("session.start", sessionHandler.HandleStart)
	server.RegisterHandler("session.end", sessionHandler.HandleEnd)
	server.RegisterHandler("session.list", sessionHandler.HandleList)
	server.RegisterHandler("session.heartbeat", sessionHandler.HandleHeartbeat)

	dispatcher := subscriptions.NewDispatcher(st.DB())
	messageHandler := rpc.NewMessageHandlerWithDispatcher(st, dispatcher)
	server.RegisterHandler("message.send", messageHandler.HandleSend)
	server.RegisterHandler("message.list", messageHandler.HandleList)
	server.RegisterHandler("message.markRead", messageHandler.HandleMarkRead)

	groupHandler := rpc.NewGroupHandler(st)
	server.RegisterHandler("group.create", groupHandler.HandleCreate)
	server.RegisterHandler("group.list", groupHandler.HandleList)
	server.RegisterHandler("group.info", groupHandler.HandleInfo)
	server.RegisterHandler("group.members", groupHandler.HandleMembers)

	subscriptionHandler := rpc.NewSubscriptionHandler(st)
	server.RegisterHandler("subscribe", subscriptionHandler.HandleSubscribe)
	server.RegisterHandler("unsubscribe", subscriptionHandler.HandleUnsubscribe)

	teamHandler := rpc.NewTeamHandler(st)
	server.RegisterHandler("team.list", teamHandler.HandleList)

	contextHandler := rpc.NewContextHandler(st)
	server.RegisterHandler("context.show", contextHandler.HandleShow)

	// Start server
	if err := server.Start(context.Background()); err != nil {
		st.Close()
		t.Fatalf("Server start failed: %v", err)
	}

	// Wait for socket ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Cleanup(func() {
		server.Stop()
		st.Close()
	})

	return st, server, socketPath
}

// rpcCall makes a JSON-RPC call to the daemon and returns the result.
func rpcCall(t *testing.T, socketPath, method string, params any, result any) {
	t.Helper()

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect to daemon: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(10 * time.Second))

	reqID := time.Now().UnixNano()
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  method,
		"params":  params,
	}

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(request); err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	if response.Error != nil {
		t.Fatalf("RPC error %d: %s", response.Error.Code, response.Error.Message)
	}

	if result != nil {
		if err := json.Unmarshal(response.Result, result); err != nil {
			t.Fatalf("Failed to unmarshal result: %v", err)
		}
	}
}
```

**Step 3: Verify it compiles with build tag**

Run: `go test -tags=resilience -list=. ./tests/resilience/ 2>&1 | head -5`
Expected: No compilation errors

**Step 4: Commit**

```bash
git add tests/resilience/doc.go tests/resilience/fixture_test.go
git commit -m "feat: add resilience test infrastructure with fixture extraction and daemon helpers"
```

---

### Task 8: Implement RPC direct tests

**Files:**
- Create: `tests/resilience/rpc_direct_test.go`

**Step 1: Create RPC tests that exercise the fixture at scale**

Tests to implement:
- `TestRPC_FixtureIntegrity` — verify agent/message/session counts in DB
- `TestRPC_SendAllScopeTypes` — broadcast, directed, module-scoped sends
- `TestRPC_InboxPagination` — paginate over 10K messages
- `TestRPC_AgentListFilters` — filter by role, module
- `TestRPC_GroupResolveNested` — nested group resolution
- `TestRPC_MessageReadTracking` — mark read, verify unread counts

Each test calls `setupFixture(t)` + `startTestDaemon(t, thrumDir)` then uses `rpcCall()`.

**Step 2: Run RPC tests**

Run: `go test -tags=resilience ./tests/resilience/ -v -run TestRPC -timeout 5m`
Expected: All PASS

**Step 3: Commit**

```bash
git add tests/resilience/rpc_direct_test.go
git commit -m "feat: add RPC direct resilience tests"
```

---

### Task 9: Implement CLI round-trip tests

**Files:**
- Create: `tests/resilience/cli_roundtrip_test.go`

**Step 1: Create CLI tests**

These tests use `exec.Command` to run the actual `thrum` binary against the test daemon. Need a helper that sets `THRUM_SOCKET` or configures the CLI to use the test socket.

Tests to implement:
- `TestCLI_SendAndInbox` — send via RPC, verify via CLI inbox
- `TestCLI_TeamList` — `thrum team` lists 50 agents
- `TestCLI_AgentContext` — `thrum agent list --context`

Note: The CLI connects via `DefaultSocketPath(repoPath)` which resolves `.thrum/var/thrum.sock`. The test daemon's socket is already at that path, so running CLI commands from the temp directory should work.

**Step 2: Run CLI tests**

Run: `go test -tags=resilience ./tests/resilience/ -v -run TestCLI -timeout 5m`
Expected: All PASS

**Step 3: Commit**

```bash
git add tests/resilience/cli_roundtrip_test.go
git commit -m "feat: add CLI round-trip resilience tests"
```

---

### Task 10: Implement concurrency tests

**Files:**
- Create: `tests/resilience/concurrent_test.go`

**Step 1: Create concurrency tests**

Key patterns:
- Use `sync.WaitGroup` + goroutines to simulate concurrent access
- Use `t.Errorf` (not `t.Fatalf`) inside goroutines (Fatal from non-test goroutine panics)
- Use `atomic` counters for tracking results

Tests to implement:
- `TestConcurrent_10Senders` — 10 goroutines each sending 100 messages
- `TestConcurrent_ReadWriteMix` — 5 writers + 5 readers simultaneous
- `TestConcurrent_InboxUnderLoad` — inbox queries during sends
- `TestConcurrent_SessionLifecycle` — concurrent session start/end

**Step 2: Run concurrency tests**

Run: `go test -tags=resilience ./tests/resilience/ -v -run TestConcurrent -timeout 5m -race`
Expected: All PASS with no race conditions

**Step 3: Commit**

```bash
git add tests/resilience/concurrent_test.go
git commit -m "feat: add concurrency resilience tests"
```

---

### Task 11: Implement recovery tests

**Files:**
- Create: `tests/resilience/recovery_test.go`

**Step 1: Create recovery tests**

Tests to implement:
- `TestRecovery_FixtureRestore` — extract fixture, start daemon, health check
- `TestRecovery_ProjectionConsistency` — rebuild SQLite from JSONL, compare row counts
- `TestRecovery_DaemonRestart` — start, send message, stop, restart, verify message persisted
- `TestRecovery_WALRecovery` — truncate WAL file, verify daemon starts cleanly
- `TestRecovery_CorruptedMessage` — add malformed JSONL line, verify projector skips it

Key pattern for `ProjectionConsistency`:
```go
// 1. Extract fixture → get pre-built DB counts
// 2. Delete the SQLite DB
// 3. Create fresh DB via schema.InitDB()
// 4. Use projection.NewProjector(db).Rebuild(syncDir) to replay JSONL
// 5. Compare row counts: agents, messages, sessions, groups
```

**Step 2: Run recovery tests**

Run: `go test -tags=resilience ./tests/resilience/ -v -run TestRecovery -timeout 5m`
Expected: All PASS

**Step 3: Commit**

```bash
git add tests/resilience/recovery_test.go
git commit -m "feat: add database recovery resilience tests"
```

---

### Task 12: Implement multi-daemon tests

**Files:**
- Create: `tests/resilience/multi_daemon_test.go`

**Step 1: Create multi-daemon tests**

Each test creates 2-3 separate `.thrum` directories, each with its own state and daemon. Tests verify:

- `TestMultiDaemon_DaemonRestart` — start daemon, write data, stop, start fresh daemon on same data, verify state preserved
- `TestMultiDaemon_IndependentDaemons` — two daemons run independently on separate DBs without interference
- `TestMultiDaemon_SharedFixture` — two daemons starting from identical fixtures diverge cleanly

**Step 2: Run multi-daemon tests**

Run: `go test -tags=resilience ./tests/resilience/ -v -run TestMultiDaemon -timeout 5m`
Expected: All PASS

**Step 3: Commit**

```bash
git add tests/resilience/multi_daemon_test.go
git commit -m "feat: add multi-daemon resilience tests"
```

---

### Task 13: Implement benchmarks

**Files:**
- Create: `tests/resilience/benchmark_test.go`

**Step 1: Create benchmark functions**

Key pattern — each benchmark extracts the fixture once in `b.ResetTimer()`:

```go
func BenchmarkSendMessage(b *testing.B) {
	thrumDir := setupFixtureForBench(b)
	st, _, socketPath := startTestDaemonForBench(b, thrumDir)
	_ = st

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Send a message via RPC
	}
}
```

Benchmarks:
- `BenchmarkSendMessage` — single message send
- `BenchmarkInbox10K` — inbox query over 10K messages
- `BenchmarkInboxUnread` — unread-only query
- `BenchmarkAgentList50` — agent list with 50 agents
- `BenchmarkGroupResolve` — nested group resolution
- `BenchmarkConcurrentSend10` — 10 concurrent senders

Note: Need `setupFixtureForBench` variant that works with `*testing.B`.

**Step 2: Run benchmarks**

Run: `go test -tags=resilience ./tests/resilience/ -bench=. -benchmem -count=1 -timeout 10m`
Expected: Benchmarks complete and report ns/op, B/op, allocs/op

**Step 3: Commit**

```bash
git add tests/resilience/benchmark_test.go
git commit -m "feat: add performance benchmarks for resilience suite"
```

---

### Task 14: Add convenience script and final integration

**Files:**
- Create: `scripts/run-resilience-tests.sh`
- Modify: `.gitignore` (if needed)

**Step 1: Create convenience script**

```bash
#!/bin/bash
set -euo pipefail

echo "=== Thrum Resilience Test Suite ==="
echo ""

echo "Running resilience tests..."
go test -tags=resilience ./tests/resilience/ -v -timeout 10m -count=1
echo ""

echo "Running benchmarks..."
go test -tags=resilience ./tests/resilience/ -bench=. -benchmem -count=3 -timeout 10m
echo ""

echo "=== Complete ==="
```

**Step 2: Make it executable**

Run: `chmod +x scripts/run-resilience-tests.sh`

**Step 3: Run the full suite**

Run: `./scripts/run-resilience-tests.sh`
Expected: All tests pass, benchmarks complete

**Step 4: Commit**

```bash
git add scripts/run-resilience-tests.sh
git commit -m "feat: add convenience script for resilience test suite"
```

---

### Task 15: Final verification and cleanup

**Step 1: Run full test suite (existing + resilience)**

Run:
```bash
go test ./... -timeout 5m                                           # Existing tests still pass
go test -tags=resilience ./tests/resilience/ -v -timeout 10m        # Resilience tests pass
go test -tags=resilience ./tests/resilience/ -bench=. -benchmem     # Benchmarks work
```
Expected: All pass

**Step 2: Verify fixture determinism**

Run:
```bash
go generate -tags=resilience ./tests/resilience/...
git diff --exit-code tests/resilience/testdata/
```
Expected: No diff (fixture is identical when regenerated with same seed)

**Step 3: Commit final state**

```bash
git add -A
git commit -m "chore: final resilience test suite verification"
```
