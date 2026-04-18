package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

// writeIdentityFile is the shared factory for test fixtures. It writes
// a minimally valid .thrum/identities/<name>.json into dir with the
// supplied PID + runtime, filling in plausible defaults for the
// remaining fields so loaders that insist on required keys don't
// reject the fixture.
func writeIdentityFile(t *testing.T, dir, name string, pid int, runtime string) string {
	t.Helper()
	idFile := config.IdentityFile{
		Version: 1,
		RepoID:  "test-repo",
		Agent: config.AgentConfig{
			Name:    name,
			Role:    "implementer",
			Module:  "test",
			Display: name,
		},
		Worktree:         dir,
		AgentPID:         pid,
		PreferredRuntime: runtime,
		Runtime:          runtime,
		UpdatedAt:        time.Now(),
	}
	b, err := json.MarshalIndent(idFile, "", "  ")
	if err != nil {
		t.Fatalf("marshal test identity: %v", err)
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write test identity: %v", err)
	}
	return path
}

// loadIdentityForTest reads an identity file back for assertion. Keeps
// direct json.Unmarshal out of the per-test code.
func loadIdentityForTest(t *testing.T, path string) config.IdentityFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read identity: %v", err)
	}
	var id config.IdentityFile
	if err := json.Unmarshal(b, &id); err != nil {
		t.Fatalf("unmarshal identity: %v", err)
	}
	return id
}
