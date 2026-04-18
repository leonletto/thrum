package guard

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
)

// QuickstartContext is the shared input for the G1a (self-rename
// refusal) and G1b (name-collision) guards. Assembled by the CLI's
// quickstart command from the parsed flags, the caller's ancestor
// chain, and the repo's .thrum/identities/ directory.
type QuickstartContext struct {
	// Mode controls enforcement. G1a reads
	// Config.QuickstartSelfRename; G1b reads
	// Config.QuickstartNameCollision. Callers pass the resolved
	// mode for the specific guard they are invoking.
	Mode Mode

	// IdentitiesDir is the absolute path to .thrum/identities for
	// the current repo (or a test temp dir).
	IdentitiesDir string

	// Chain is the caller's ancestor PID chain. Used by G1a to
	// detect "my own PID owns an existing identity here."
	Chain []int

	// RequestedName is the --name the caller asked quickstart to
	// register as. Used by G1b to detect a name collision against
	// a live squatter.
	RequestedName string

	// Force is true when the caller passed --force. G1a honours
	// this to rename the existing owned file to <name>.json.deleted
	// instead of refusing.
	Force bool

	// IsPIDAlive is an injectable liveness probe used by G1b to
	// distinguish a dead squatter (reclaimable) from a live one
	// (hard error).
	IsPIDAlive func(int) bool

	// WarnLogger receives structured events when a guard fires in
	// ModeWarn. Nil warn logger silently swallows.
	WarnLogger *slog.Logger
}

// G1a refuses quickstart when the caller already owns an existing
// identity in IdentitiesDir — i.e. one of the *.json files has an
// agent_pid that appears in the caller's ancestor chain. Force
// promotes the existing identity to <path>.deleted and returns nil so
// the quickstart can continue with a fresh registration.
func G1a(qc *QuickstartContext) error {
	if qc.Mode == ModeOff {
		return nil
	}
	owned, ownedFile, err := findOwnedIdentity(qc.IdentitiesDir, qc.Chain)
	if err != nil {
		return fmt.Errorf("scan identities: %w", err)
	}
	if owned == "" {
		return nil
	}
	if qc.Force {
		if err := os.Rename(ownedFile, ownedFile+".deleted"); err != nil {
			return fmt.Errorf("rename to .deleted: %w", err)
		}
		return nil
	}
	e := &Error{
		Guard:         "quickstart_self_rename",
		Reason:        "caller_already_owns_identity",
		CallerPID:     chainHead(qc.Chain),
		ExpectedAgent: owned,
		Remediation:   "use --force to rename the existing identity to .deleted and register fresh",
	}
	if qc.Mode == ModeWarn {
		if qc.WarnLogger != nil {
			qc.WarnLogger.Warn("identity_guard_fire",
				"guard", e.Guard,
				"reason", e.Reason,
				"expected_agent", e.ExpectedAgent,
				"caller_pid", e.CallerPID,
			)
		}
		return nil
	}
	return e
}

// findOwnedIdentity scans *.json files in dir, loads each, and returns
// the name + path of the first file whose AgentPID appears in chain.
// Empty name + nil error means "no owned identity in this dir," which
// is the healthy quickstart path.
//
// .deleted sidekicks are skipped — they represent identities the
// caller has already renamed away via a previous --force and must not
// be considered owned.
func findOwnedIdentity(dir string, chain []int) (name, path string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil
		}
		return "", "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".json") {
			continue
		}
		full := filepath.Join(dir, n)
		id, lerr := loadIdentityPID(full)
		if lerr != nil {
			// A malformed file mustn't mask a real ownership
			// match elsewhere; skip silently.
			continue
		}
		if id.AgentPID != 0 && ChainContains(chain, id.AgentPID) {
			return id.Agent.Name, full, nil
		}
	}
	return "", "", nil
}

// loadIdentityPID reads just the fields G1/G4 care about from an
// identity file. A full config.Load would pull in env-var resolution
// and error on missing repo context — neither of which applies to a
// bare on-disk scan from a guard callsite.
func loadIdentityPID(path string) (config.IdentityFile, error) {
	// #nosec G304 -- path is always a file discovered by scanning
	// the quickstart's own IdentitiesDir, not an outside-supplied
	// path.
	b, err := os.ReadFile(path)
	if err != nil {
		return config.IdentityFile{}, err
	}
	var id config.IdentityFile
	if err := json.Unmarshal(b, &id); err != nil {
		return config.IdentityFile{}, err
	}
	return id, nil
}
