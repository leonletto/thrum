package testgen

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/types"
)

// Config controls the generated fixture volumes.
type Config struct {
	Seed        int64
	NumAgents   int // default 50
	NumMessages int // default 10000
	NumSessions int // default 100
	NumGroups   int // default 20
	NumEvents   int // default 500
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

var roles = []string{"coordinator", "implementer", "reviewer", "planner", "tester"}
var modules = []string{"main", "daemon", "relay", "sync", "cli", "ui", "auth", "groups"}

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

// generateAgents creates deterministic agent data.
func generateAgents(_ *rand.Rand, n int) []agentInfo {
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
		// First 10 sessions stay active (for test agents); 80% of the rest are ended
		if i >= 10 && rng.Float64() < 0.8 {
			end := start.Add(duration)
			s.EndedAt = &end
		}
		sessions = append(sessions, s)
	}
	return sessions
}

// generateMessages creates deterministic message data.
func generateMessages(rng *rand.Rand, agents []agentInfo, sessions []sessionInfo, n int, baseTime time.Time) []messageInfo {
	// Build a map of agent -> sessions for faster lookup
	agentSessions := make(map[string][]sessionInfo)
	for _, s := range sessions {
		agentSessions[s.AgentID] = append(agentSessions[s.AgentID], s)
	}

	messages := make([]messageInfo, 0, n)
	for i := 0; i < n; i++ {
		agent := agents[rng.Intn(len(agents))]

		// Find a session for this agent
		var session sessionInfo
		if ss, ok := agentSessions[agent.AgentID]; ok && len(ss) > 0 {
			session = ss[rng.Intn(len(ss))]
		} else {
			session = sessions[i%len(sessions)]
		}

		timestamp := baseTime.Add(time.Duration(i) * 30 * time.Second)
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

		// 60% broadcast, 30% directed, 10% module-scoped
		r := rng.Float64()
		switch {
		case r < 0.6:
			// broadcast - no scopes
		case r < 0.9:
			target := agents[rng.Intn(len(agents))]
			msg.Scopes = []types.Scope{{Type: "agent", Value: target.AgentID}}
		default:
			msg.Scopes = []types.Scope{{Type: "module", Value: modules[rng.Intn(len(modules))]}}
		}

		messages = append(messages, msg)
	}
	return messages
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

// Generate creates a complete .thrum directory at outputDir.
func Generate(outputDir string, cfg Config) error {
	rng := rand.New(rand.NewSource(cfg.Seed)) //nolint:gosec // intentional use of weak random for deterministic test data
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

func writeConfigJSON(outputDir string) error {
	cfg := map[string]any{
		"local_only": true,
		"version":    1,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outputDir, "config.json"), data, 0600)
}

func writeIdentityFiles(outputDir string, agents []agentInfo) error {
	for _, a := range agents {
		identity := config.IdentityFile{
			Version: 2,
			RepoID:  "test-resilience",
			Agent: config.AgentConfig{
				Kind:    a.Kind,
				Name:    a.Name,
				Role:    a.Role,
				Module:  a.Module,
				Display: a.Display,
			},
			Worktree:    "test",
			ContextFile: fmt.Sprintf("context/%s.md", a.Name),
			UpdatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		data, err := json.MarshalIndent(identity, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal identity %s: %w", a.Name, err)
		}
		path := filepath.Join(outputDir, "identities", a.Name+".json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			return fmt.Errorf("write identity %s: %w", a.Name, err)
		}
	}
	return nil
}

func writeContextFiles(outputDir string, agents []agentInfo) error {
	for _, a := range agents {
		content := fmt.Sprintf("# %s Session Context\n\n"+
			"**Role:** %s\n"+
			"**Module:** %s\n\n"+
			"## Current Work\n\n"+
			"Working on %s module tasks. Recent activity includes code review,\n"+
			"implementation of new features, and bug fixes.\n\n"+
			"## Notes\n\n"+
			"- Coordinating with team on %s integration\n"+
			"- Test coverage at 85%%\n"+
			"- Next milestone: v1.0 release\n",
			a.Display, a.Role, a.Module, a.Module, a.Module)
		path := filepath.Join(outputDir, "context", a.Name+".md")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			return fmt.Errorf("write context %s: %w", a.Name, err)
		}
	}
	return nil
}

func writeJSONLEvents(outputDir string, agents []agentInfo, sessions []sessionInfo, messages []messageInfo, groups []groupInfo, baseTime time.Time) error {
	// events.jsonl — agent register, session start/end, group create/member.add
	eventsFile, err := os.Create(filepath.Join(outputDir, "events.jsonl")) //nolint:gosec // controlled path in test data generation
	if err != nil {
		return fmt.Errorf("create events.jsonl: %w", err)
	}
	defer func() { _ = eventsFile.Close() }()
	eventsEnc := json.NewEncoder(eventsFile)

	eventSeq := 0

	// Agent register events
	for i, a := range agents {
		event := map[string]any{
			"type":      "agent.register",
			"timestamp": baseTime.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			"event_id":  fmt.Sprintf("evt_%08d", eventSeq),
			"v":         1,
			"agent_id":  a.AgentID,
			"kind":      a.Kind,
			"role":      a.Role,
			"module":    a.Module,
			"display":   a.Display,
			"hostname":  a.Hostname,
		}
		if err := eventsEnc.Encode(event); err != nil {
			return fmt.Errorf("write agent register event: %w", err)
		}
		eventSeq++
	}

	// Session start/end events
	for _, s := range sessions {
		startEvent := map[string]any{
			"type":       "session.start",
			"timestamp":  s.StartedAt.Format(time.RFC3339),
			"event_id":   fmt.Sprintf("evt_%08d", eventSeq),
			"v":          1,
			"session_id": s.SessionID,
			"agent_id":   s.AgentID,
		}
		if err := eventsEnc.Encode(startEvent); err != nil {
			return fmt.Errorf("write session start event: %w", err)
		}
		eventSeq++

		if s.EndedAt != nil {
			endEvent := map[string]any{
				"type":       "session.end",
				"timestamp":  s.EndedAt.Format(time.RFC3339),
				"event_id":   fmt.Sprintf("evt_%08d", eventSeq),
				"v":          1,
				"session_id": s.SessionID,
				"agent_id":   s.AgentID,
				"reason":     "normal",
			}
			if err := eventsEnc.Encode(endEvent); err != nil {
				return fmt.Errorf("write session end event: %w", err)
			}
			eventSeq++
		}
	}

	// Group create and member.add events
	for _, g := range groups {
		createEvent := map[string]any{
			"type":        "group.create",
			"timestamp":   baseTime.Add(1 * time.Hour).Format(time.RFC3339),
			"event_id":    fmt.Sprintf("evt_%08d", eventSeq),
			"v":           1,
			"group_id":    g.GroupID,
			"name":        g.Name,
			"description": g.Description,
			"created_by":  g.CreatedBy,
		}
		if err := eventsEnc.Encode(createEvent); err != nil {
			return fmt.Errorf("write group create event: %w", err)
		}
		eventSeq++

		for _, m := range g.Members {
			addEvent := map[string]any{
				"type":         "group.member.add",
				"timestamp":    baseTime.Add(1 * time.Hour).Format(time.RFC3339),
				"event_id":     fmt.Sprintf("evt_%08d", eventSeq),
				"v":            1,
				"group_id":     g.GroupID,
				"member_type":  m.MemberType,
				"member_value": m.MemberValue,
				"added_by":     g.CreatedBy,
			}
			if err := eventsEnc.Encode(addEvent); err != nil {
				return fmt.Errorf("write group member add event: %w", err)
			}
			eventSeq++
		}
	}

	// Messages — sharded to messages/{agent_name}.jsonl
	messageWriters := make(map[string]*json.Encoder)
	messageFiles := make(map[string]*os.File)
	defer func() {
		for _, f := range messageFiles {
			_ = f.Close()
		}
	}()

	for _, msg := range messages {
		enc, ok := messageWriters[msg.AgentID]
		if !ok {
			f, err := os.Create(filepath.Join(outputDir, "messages", msg.AgentID+".jsonl")) //nolint:gosec // controlled path in test data generation
			if err != nil {
				return fmt.Errorf("create message file for %s: %w", msg.AgentID, err)
			}
			messageFiles[msg.AgentID] = f
			enc = json.NewEncoder(f)
			messageWriters[msg.AgentID] = enc
		}

		event := map[string]any{
			"type":       "message.create",
			"timestamp":  msg.Timestamp.Format(time.RFC3339),
			"event_id":   fmt.Sprintf("evt_%08d", eventSeq),
			"v":          1,
			"message_id": msg.MessageID,
			"agent_id":   msg.AgentID,
			"session_id": msg.SessionID,
			"body": map[string]any{
				"format":  msg.Format,
				"content": msg.Content,
			},
		}
		if len(msg.Scopes) > 0 {
			event["scopes"] = msg.Scopes
		}
		if err := enc.Encode(event); err != nil {
			return fmt.Errorf("write message event: %w", err)
		}
		eventSeq++
	}

	return nil
}

func populateSQLite(outputDir string, agents []agentInfo, sessions []sessionInfo, messages []messageInfo, groups []groupInfo, rng *rand.Rand, baseTime time.Time) error {
	dbPath := filepath.Join(outputDir, "var", "messages.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		return fmt.Errorf("init db: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Insert agents
	agentStmt, err := tx.Prepare("INSERT INTO agents (agent_id, kind, role, module, display, hostname, registered_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare agent insert: %w", err)
	}
	defer func() { _ = agentStmt.Close() }()
	for i, a := range agents {
		ts := baseTime.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		if _, err := agentStmt.Exec(a.AgentID, a.Kind, a.Role, a.Module, a.Display, a.Hostname, ts, ts); err != nil {
			return fmt.Errorf("insert agent %s: %w", a.AgentID, err)
		}
	}

	// Insert sessions
	sessionStmt, err := tx.Prepare("INSERT INTO sessions (session_id, agent_id, started_at, ended_at, last_seen_at) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare session insert: %w", err)
	}
	defer func() { _ = sessionStmt.Close() }()
	for _, s := range sessions {
		startTS := s.StartedAt.Format(time.RFC3339)
		var endTS *string
		if s.EndedAt != nil {
			e := s.EndedAt.Format(time.RFC3339)
			endTS = &e
		}
		if _, err := sessionStmt.Exec(s.SessionID, s.AgentID, startTS, endTS, startTS); err != nil {
			return fmt.Errorf("insert session %s: %w", s.SessionID, err)
		}
	}

	// Insert messages
	msgStmt, err := tx.Prepare("INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare message insert: %w", err)
	}
	defer func() { _ = msgStmt.Close() }()

	scopeStmt, err := tx.Prepare("INSERT INTO message_scopes (message_id, scope_type, scope_value) VALUES (?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare scope insert: %w", err)
	}
	defer func() { _ = scopeStmt.Close() }()

	for _, m := range messages {
		ts := m.Timestamp.Format(time.RFC3339)
		if _, err := msgStmt.Exec(m.MessageID, m.AgentID, m.SessionID, ts, m.Format, m.Content); err != nil {
			return fmt.Errorf("insert message %s: %w", m.MessageID, err)
		}
		for _, s := range m.Scopes {
			if _, err := scopeStmt.Exec(m.MessageID, s.Type, s.Value); err != nil {
				return fmt.Errorf("insert scope for %s: %w", m.MessageID, err)
			}
		}
	}

	// Insert groups and members
	groupStmt, err := tx.Prepare("INSERT INTO groups (group_id, name, description, created_by, created_at) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare group insert: %w", err)
	}
	defer func() { _ = groupStmt.Close() }()

	memberStmt, err := tx.Prepare("INSERT INTO group_members (group_id, member_type, member_value, added_by, added_at) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare member insert: %w", err)
	}
	defer func() { _ = memberStmt.Close() }()

	groupTS := baseTime.Add(1 * time.Hour).Format(time.RFC3339)
	for _, g := range groups {
		if _, err := groupStmt.Exec(g.GroupID, g.Name, g.Description, g.CreatedBy, groupTS); err != nil {
			return fmt.Errorf("insert group %s: %w", g.Name, err)
		}
		for _, m := range g.Members {
			if _, err := memberStmt.Exec(g.GroupID, m.MemberType, m.MemberValue, g.CreatedBy, groupTS); err != nil {
				return fmt.Errorf("insert group member for %s: %w", g.Name, err)
			}
		}
	}

	// Insert message reads (~70% of messages read by random agents)
	readStmt, err := tx.Prepare("INSERT OR IGNORE INTO message_reads (message_id, session_id, agent_id, read_at) VALUES (?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare read insert: %w", err)
	}
	defer func() { _ = readStmt.Close() }()

	// Build agent->sessions map for assigning reads to sessions
	agentSessions := make(map[string][]sessionInfo)
	for _, s := range sessions {
		agentSessions[s.AgentID] = append(agentSessions[s.AgentID], s)
	}

	for _, m := range messages {
		if rng.Float64() < 0.7 {
			reader := agents[rng.Intn(len(agents))]
			// Pick a session for this reader
			var sessionID string
			if ss, ok := agentSessions[reader.AgentID]; ok && len(ss) > 0 {
				sessionID = ss[rng.Intn(len(ss))].SessionID
			} else {
				sessionID = sessions[rng.Intn(len(sessions))].SessionID
			}
			readTS := m.Timestamp.Add(time.Duration(1+rng.Intn(60)) * time.Minute).Format(time.RFC3339)
			if _, err := readStmt.Exec(m.MessageID, sessionID, reader.AgentID, readTS); err != nil {
				return fmt.Errorf("insert read for %s: %w", m.MessageID, err)
			}
		}
	}

	// Insert events (agent lifecycle)
	eventStmt, err := tx.Prepare("INSERT INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare event insert: %w", err)
	}
	defer func() { _ = eventStmt.Close() }()

	seq := 0
	for i, a := range agents {
		ts := baseTime.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		eventJSON, _ := json.Marshal(map[string]any{
			"type": "agent.register", "timestamp": ts, "event_id": fmt.Sprintf("evt_%08d", seq),
			"agent_id": a.AgentID, "kind": a.Kind, "role": a.Role, "module": a.Module,
		})
		if _, err := eventStmt.Exec(fmt.Sprintf("evt_%08d", seq), seq, "agent.register", ts, "test-daemon", string(eventJSON)); err != nil {
			return fmt.Errorf("insert agent event: %w", err)
		}
		seq++
	}
	for _, s := range sessions {
		ts := s.StartedAt.Format(time.RFC3339)
		eventJSON, _ := json.Marshal(map[string]any{
			"type": "session.start", "timestamp": ts, "event_id": fmt.Sprintf("evt_%08d", seq),
			"session_id": s.SessionID, "agent_id": s.AgentID,
		})
		if _, err := eventStmt.Exec(fmt.Sprintf("evt_%08d", seq), seq, "session.start", ts, "test-daemon", string(eventJSON)); err != nil {
			return fmt.Errorf("insert session start event: %w", err)
		}
		seq++
		if s.EndedAt != nil {
			endTS := s.EndedAt.Format(time.RFC3339)
			eventJSON, _ := json.Marshal(map[string]any{
				"type": "session.end", "timestamp": endTS, "event_id": fmt.Sprintf("evt_%08d", seq),
				"session_id": s.SessionID, "agent_id": s.AgentID, "reason": "normal",
			})
			if _, err := eventStmt.Exec(fmt.Sprintf("evt_%08d", seq), seq, "session.end", endTS, "test-daemon", string(eventJSON)); err != nil {
				return fmt.Errorf("insert session end event: %w", err)
			}
			seq++
		}
	}

	return tx.Commit()
}

// CompressToTarGz compresses a directory into a .tar.gz file.
func CompressToTarGz(sourceDir, outputPath string) error {
	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0750); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	outFile, err := os.Create(outputPath) //nolint:gosec // controlled path in test data generation
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer func() { _ = outFile.Close() }()

	gzWriter := gzip.NewWriter(outFile)
	defer func() { _ = gzWriter.Close() }()

	tarWriter := tar.NewWriter(gzWriter)
	defer func() { _ = tarWriter.Close() }()

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

		f, err := os.Open(path) //nolint:gosec // controlled path in test data generation
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		_, err = io.Copy(tarWriter, f)
		return err
	})
}
