package guard

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

// WritePID is the sole primitive for mutating an identity file's
// AgentPID. Every PID-write callsite in the codebase must route
// through here — the wider design rule (spec: "PID-write sites are
// restricted to prime, quickstart, and Rule #4‴ auto-reclaim") only
// holds if there is exactly one function that knows how to perform
// the write atomically.
//
// Semantics:
//   - If path does not exist, a minimal identity file is created
//     containing only AgentPID + UpdatedAt (the first-prime path).
//   - If path exists, the existing file is loaded, AgentPID and
//     UpdatedAt are overwritten, and the full struct is re-marshaled
//     so all other fields round-trip untouched.
//   - The write itself goes through AtomicWrite, so it is fcntl-
//     serialized against other writers and crash-safe via tmpfile +
//     rename.
func WritePID(path string, pid int) error {
	var id config.IdentityFile
	// #nosec G304 -- WritePID is an internal primitive called only
	// by prime / quickstart / Rule #4‴ reclaim with paths derived
	// from the repo's .thrum/identities/ layout.
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &id); err != nil {
			return fmt.Errorf("unmarshal existing identity %s: %w", path, err)
		}
	case os.IsNotExist(err):
		// First-prime: start from a zero-valued IdentityFile. The
		// minimal-valid invariant (caller-supplied Agent / RepoID
		// / Worktree) is enforced elsewhere — this primitive only
		// owns the PID + UpdatedAt fields.
	default:
		return fmt.Errorf("read existing identity %s: %w", path, err)
	}

	id.AgentPID = pid
	id.UpdatedAt = time.Now()

	out, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal identity: %w", err)
	}
	if err := AtomicWrite(path, out); err != nil {
		return fmt.Errorf("atomic write %s: %w", path, err)
	}
	return nil
}
