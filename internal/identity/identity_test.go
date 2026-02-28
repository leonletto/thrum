package identity_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/identity"
)

func TestGenerateRepoID(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string // Expected prefix and length
	}{
		{
			name: "HTTPS URL",
			url:  "https://github.com/user/repo.git",
			want: "r_",
		},
		{
			name: "HTTPS URL without .git",
			url:  "https://github.com/user/repo",
			want: "r_",
		},
		{
			name: "git@ URL",
			url:  "git@github.com:user/repo.git",
			want: "r_",
		},
		{
			name: "git@ URL without .git",
			url:  "git@github.com:user/repo",
			want: "r_",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := identity.GenerateRepoID(tt.url)
			if err != nil {
				t.Fatalf("GenerateRepoID() error = %v", err)
			}

			if !strings.HasPrefix(got, tt.want) {
				t.Errorf("GenerateRepoID() = %v, want prefix %v", got, tt.want)
			}

			// Should be "r_" + 12 characters = 14 total
			if len(got) != 14 {
				t.Errorf("GenerateRepoID() length = %d, want 14", len(got))
			}
		})
	}
}

func TestGenerateRepoID_Deterministic(t *testing.T) {
	url1 := "https://github.com/user/repo.git"
	url2 := "git@github.com:user/repo.git"
	url3 := "https://github.com/user/repo"

	id1, _ := identity.GenerateRepoID(url1)
	id2, _ := identity.GenerateRepoID(url2)
	id3, _ := identity.GenerateRepoID(url3)

	// All should produce the same ID (normalized)
	if id1 != id2 {
		t.Errorf("IDs should match: %s != %s", id1, id2)
	}
	if id1 != id3 {
		t.Errorf("IDs should match: %s != %s", id1, id3)
	}

	// Generate same URL twice - should be deterministic
	id4, _ := identity.GenerateRepoID(url1)
	if id1 != id4 {
		t.Errorf("Same URL should produce same ID: %s != %s", id1, id4)
	}
}

func TestGenerateAgentID(t *testing.T) {
	repoID := "r_ABC123DEF456"
	role := "implementer"
	module := "auth"

	// Test hash-based format (no name provided)
	id := identity.GenerateAgentID(repoID, role, module, "")

	// Should have format: role_hash (e.g., implementer_35HV62T9B9)
	if !strings.HasPrefix(id, role+"_") {
		t.Errorf("ID should start with '%s_', got %s", role, id)
	}

	parts := strings.Split(id, "_")
	if len(parts) != 2 {
		t.Errorf("ID should have 2 parts, got %d: %s", len(parts), id)
	}

	if parts[0] != role {
		t.Errorf("Role should be %s, got %s", role, parts[0])
	}

	// Hash part should be 10 characters
	if len(parts[1]) != 10 {
		t.Errorf("Hash should be 10 characters, got %d: %s", len(parts[1]), parts[1])
	}

	// Test named format
	namedID := identity.GenerateAgentID(repoID, role, module, "furiosa")
	if namedID != "furiosa" {
		t.Errorf("Named ID should be 'furiosa', got %s", namedID)
	}
}

func TestGenerateAgentID_Deterministic(t *testing.T) {
	repoID := "r_TEST123"
	role := "planner"
	module := "frontend"

	id1 := identity.GenerateAgentID(repoID, role, module, "")
	id2 := identity.GenerateAgentID(repoID, role, module, "")

	if id1 != id2 {
		t.Errorf("Same inputs should produce same ID: %s != %s", id1, id2)
	}

	// Different module should produce different ID
	id3 := identity.GenerateAgentID(repoID, role, "backend", "")
	if id1 == id3 {
		t.Errorf("Different module should produce different ID")
	}
}

func TestGenerateSessionID(t *testing.T) {
	id := identity.GenerateSessionID()

	if !strings.HasPrefix(id, "ses_") {
		t.Errorf("Session ID should start with 'ses_', got %s", id)
	}

	// ULID is 26 characters, so total should be 30
	if len(id) != 30 {
		t.Errorf("Session ID length should be 30, got %d: %s", len(id), id)
	}
}

func TestGenerateSessionID_Unique(t *testing.T) {
	id1 := identity.GenerateSessionID()
	id2 := identity.GenerateSessionID()

	if id1 == id2 {
		t.Errorf("Session IDs should be unique: %s == %s", id1, id2)
	}
}

func TestGenerateMessageID(t *testing.T) {
	id := identity.GenerateMessageID()

	if !strings.HasPrefix(id, "msg_") {
		t.Errorf("Message ID should start with 'msg_', got %s", id)
	}

	if len(id) != 30 {
		t.Errorf("Message ID length should be 30, got %d: %s", len(id), id)
	}
}

func TestGenerateThreadID(t *testing.T) {
	id := identity.GenerateThreadID()

	if !strings.HasPrefix(id, "thr_") {
		t.Errorf("Thread ID should start with 'thr_', got %s", id)
	}

	if len(id) != 30 {
		t.Errorf("Thread ID length should be 30, got %d: %s", len(id), id)
	}
}

func TestULIDTimestamp(t *testing.T) {
	// Generate a fresh ULID
	id := identity.GenerateSessionID()
	ulidPart := strings.TrimPrefix(id, "ses_")

	// Extract timestamp
	ts, err := identity.ULIDTimestamp(ulidPart)
	if err != nil {
		t.Fatalf("ULIDTimestamp() error = %v", err)
	}

	// Should be very close to now (within 1 second)
	now := time.Now()
	diff := now.Sub(ts)
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Second {
		t.Errorf("ULID timestamp too far from now: %v (diff: %v)", ts, diff)
	}
}

func TestParseULID(t *testing.T) {
	id := identity.GenerateMessageID()
	ulidPart := strings.TrimPrefix(id, "msg_")

	ts, err := identity.ParseULID(ulidPart)
	if err != nil {
		t.Fatalf("ParseULID() error = %v", err)
	}

	// Should be very recent
	now := time.Now()
	if ts.After(now) {
		t.Errorf("ULID timestamp in future: %v > %v", ts, now)
	}

	diff := now.Sub(ts)
	if diff > time.Second {
		t.Errorf("ULID timestamp too old: %v (diff: %v)", ts, diff)
	}
}

func TestGenerateULID_ConcurrentUniqueness(t *testing.T) {
	const goroutines = 100

	var wg sync.WaitGroup
	ids := make([]string, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			ids[idx] = identity.GenerateEventID()
		}(i)
	}
	wg.Wait()

	seen := make(map[string]struct{}, goroutines)
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate ULID detected: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestGenerateRepoID_InvalidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"invalid git@ format", "git@invalid"},
		{"empty URL", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := identity.GenerateRepoID(tt.url)
			if err == nil {
				t.Errorf("GenerateRepoID() should error on invalid URL: %s", tt.url)
			}
		})
	}
}

func TestValidateAgentName_Valid(t *testing.T) {
	validNames := []string{
		"furiosa",
		"nux",
		"coordinator_1b9k",
		"implementer_abc123",
		"agent1",
		"test_agent",
		"my_agent_123",
		"a",
		"_",
		"_underscore_start",
		"123_numbers_first",
		"my-agent",
		"feature-backup-restore",
		"impl-team-fix",
		"a-b-c",
	}

	for _, name := range validNames {
		t.Run(name, func(t *testing.T) {
			err := identity.ValidateAgentName(name)
			if err != nil {
				t.Errorf("ValidateAgentName(%q) should be valid, got error: %v", name, err)
			}
		})
	}
}

func TestValidateAgentName_Invalid(t *testing.T) {
	tests := []struct {
		name        string
		agentName   string
		errorSubstr string // substring expected in error message
	}{
		{
			name:        "empty name",
			agentName:   "",
			errorSubstr: "cannot be empty",
		},
		{
			name:        "uppercase letters",
			agentName:   "Furiosa",
			errorSubstr: "invalid characters",
		},
		{
			name:        "all uppercase",
			agentName:   "AGENT",
			errorSubstr: "invalid characters",
		},
		{
			name:        "dot",
			agentName:   "my.agent",
			errorSubstr: "invalid characters",
		},
		{
			name:        "space",
			agentName:   "my agent",
			errorSubstr: "invalid characters",
		},
		{
			name:        "slash",
			agentName:   "my/agent",
			errorSubstr: "invalid characters",
		},
		{
			name:        "backslash",
			agentName:   "my\\agent",
			errorSubstr: "invalid characters",
		},
		{
			name:        "at symbol",
			agentName:   "@agent",
			errorSubstr: "invalid characters",
		},
		{
			name:        "special characters",
			agentName:   "agent!@#",
			errorSubstr: "invalid characters",
		},
		{
			name:        "unicode",
			agentName:   "agentñ",
			errorSubstr: "invalid characters",
		},
		{
			name:        "emoji",
			agentName:   "agent🚀",
			errorSubstr: "invalid characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := identity.ValidateAgentName(tt.agentName)
			if err == nil {
				t.Errorf("ValidateAgentName(%q) should be invalid, got no error", tt.agentName)
				return
			}
			if !strings.Contains(err.Error(), tt.errorSubstr) {
				t.Errorf("ValidateAgentName(%q) error should contain %q, got: %v", tt.agentName, tt.errorSubstr, err)
			}
		})
	}
}

func TestValidateAgentName_Reserved(t *testing.T) {
	reservedNames := []string{
		"daemon",
		"system",
		"thrum",
		"all",
		"broadcast",
	}

	for _, name := range reservedNames {
		t.Run(name, func(t *testing.T) {
			err := identity.ValidateAgentName(name)
			if err == nil {
				t.Errorf("ValidateAgentName(%q) should be reserved, got no error", name)
				return
			}
			if !strings.Contains(err.Error(), "reserved") {
				t.Errorf("ValidateAgentName(%q) error should mention 'reserved', got: %v", name, err)
			}
		})
	}
}

func TestSanitizeAgentName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"citationsBranch", "citationsbranch"},
		{"feature/backup-restore", "feature_backup-restore"},
		{"my.branch.name", "my_branch_name"},
		{"Feature/MyBranch", "feature_mybranch"},
		{"ALLCAPS", "allcaps"},
		{"dots.and/slashes", "dots_and_slashes"},
		{"already_valid", "already_valid"},
		{"with spaces", "with_spaces"},
		{"__leading_trailing__", "leading_trailing"},
		{"multi///slashes", "multi_slashes"},
		{"hyphen-ok", "hyphen-ok"},
		{"MixedCase-With-Hyphens", "mixedcase-with-hyphens"},
		{"-leading-hyphen", "leading-hyphen"},
		{"trailing-hyphen-", "trailing-hyphen"},
		{"---three-hyphens---", "three-hyphens"},
		{"///", "main"},
		{"...", "main"},
		{"", "main"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := identity.SanitizeAgentName(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeAgentName(%q) = %q, want %q", tt.input, got, tt.want)
			}
			// Result should always pass validation
			if err := identity.ValidateAgentName(got); err != nil {
				t.Errorf("SanitizeAgentName(%q) result %q fails validation: %v", tt.input, got, err)
			}
		})
	}
}
