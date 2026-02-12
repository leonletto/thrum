package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	// Use Crockford's base32 alphabet (no padding, case-insensitive).
	crockfordBase32 = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

	// AgentNameRegex defines valid agent names: lowercase alphanumeric + underscores.
	agentNameRegex = regexp.MustCompile(`^[a-z0-9_]+$`)

	// ReservedNames are names that cannot be used for agents.
	reservedNames = map[string]bool{
		"daemon":    true,
		"system":    true,
		"thrum":     true,
		"all":       true,
		"broadcast": true,
	}
)

// GenerateRepoID generates a deterministic repository ID from a Git origin URL.
// Format: "r_" + base32(sha256(normalized_origin_url))[:12].
func GenerateRepoID(originURL string) (string, error) {
	normalized, err := normalizeGitURL(originURL)
	if err != nil {
		return "", fmt.Errorf("normalize URL: %w", err)
	}

	hash := sha256.Sum256([]byte(normalized))
	encoded := crockfordBase32.EncodeToString(hash[:])

	// Take first 12 characters
	if len(encoded) < 12 {
		return "", fmt.Errorf("encoded hash too short: %d", len(encoded))
	}

	return "r_" + encoded[:12], nil
}

// normalizeGitURL normalizes a Git origin URL for consistent hashing.
// Handles: https://github.com/user/repo.git, git@github.com:user/repo.git, etc.
func normalizeGitURL(rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("empty URL")
	}

	// Handle git@host:path format
	if strings.HasPrefix(rawURL, "git@") {
		parts := strings.SplitN(rawURL, ":", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid git@ URL format: %s", rawURL)
		}
		host := strings.TrimPrefix(parts[0], "git@")
		path := strings.TrimSuffix(parts[1], ".git")
		return fmt.Sprintf("https://%s/%s", host, path), nil
	}

	// Handle https:// or http:// URLs
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}

	// Normalize to https, remove .git suffix
	path := strings.TrimSuffix(u.Path, ".git")
	return fmt.Sprintf("https://%s%s", strings.ToLower(u.Host), path), nil
}

// GenerateAgentID generates an agent ID.
// If name is provided, uses it directly (e.g., "furiosa").
// Otherwise, generates a deterministic ID: role + "_" + base32(sha256(repo_id + "|" + role + "|" + module))[:10].
func GenerateAgentID(repoID, role, module, name string) string {
	if name != "" {
		return name
	}

	// Fallback to hash-based format.
	// Lowercase the hash so the ID passes ValidateAgentName (lowercase a-z, 0-9, _).
	input := fmt.Sprintf("%s|%s|%s", repoID, role, module)
	hash := sha256.Sum256([]byte(input))
	encoded := strings.ToLower(crockfordBase32.EncodeToString(hash[:]))

	return fmt.Sprintf("%s_%s", role, encoded[:10])
}

// GenerateUserID generates a user ID from a username.
// Format: "user:" + username
// No hashing - usernames are human-readable identifiers.
func GenerateUserID(username string) string {
	return "user:" + username
}

// GenerateSessionID generates a unique session ID using ULID.
// Format: "ses_" + ulid().
func GenerateSessionID() string {
	return "ses_" + generateULID()
}

// GenerateSessionToken generates a unique session token using ULID.
// Format: "tok_" + ulid()
// Used for WebSocket reconnection.
func GenerateSessionToken() string {
	return "tok_" + generateULID()
}

// GenerateMessageID generates a unique message ID using ULID.
// Format: "msg_" + ulid().
func GenerateMessageID() string {
	return "msg_" + generateULID()
}

// GenerateThreadID generates a unique thread ID using ULID.
// Format: "thr_" + ulid().
func GenerateThreadID() string {
	return "thr_" + generateULID()
}

// GenerateEventID generates a unique event ID using ULID.
// Format: "evt_" + ulid()
// Used for event deduplication in JSONL merge operations.
func GenerateEventID() string {
	return "evt_" + generateULID()
}

// GenerateGroupID generates a unique group ID using ULID.
// Format: "grp_" + ulid().
func GenerateGroupID() string {
	return "grp_" + generateULID()
}

var (
	ulidMu      sync.Mutex
	ulidEntropy = ulid.Monotonic(rand.Reader, 0)
)

// generateULID generates a ULID string.
func generateULID() string {
	ulidMu.Lock()
	defer ulidMu.Unlock()
	id := ulid.MustNew(ulid.Timestamp(time.Now()), ulidEntropy)
	return id.String()
}

// ParseULID parses a ULID string and returns the timestamp.
func ParseULID(s string) (time.Time, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse ULID: %w", err)
	}
	ms := id.Time()
	if ms/1000 > uint64(math.MaxInt64) {
		return time.Time{}, fmt.Errorf("ULID timestamp %d exceeds int64 range", ms)
	}
	return time.Unix(int64(ms/1000), int64(ms%1000)*1e6), nil //nolint:gosec // overflow checked above
}

// ULIDTimestamp extracts the timestamp from a ULID string.
func ULIDTimestamp(s string) (time.Time, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse ULID: %w", err)
	}

	ms := id.Time()
	if ms/1000 > uint64(math.MaxInt64) {
		return time.Time{}, fmt.Errorf("ULID timestamp %d exceeds int64 range", ms)
	}
	sec := int64(ms / 1000)      //nolint:gosec // overflow checked above
	nsec := int64(ms%1000) * 1e6 //nolint:gosec // ms%1000 is always < 1000

	return time.Unix(sec, nsec), nil
}

// ValidateAgentName validates an agent name according to the naming rules.
// Names must be safe for: file paths, @mention targets, JSONL field values, git tracking.
//
// Rules:
//   - Allowed characters: lowercase letters (a-z), digits (0-9), underscores (_)
//   - Rejected: hyphens, dots, spaces, path separators, uppercase, special characters
//   - Reserved names: daemon, system, thrum, all, broadcast
//   - Cannot be empty
//
// Returns nil if valid, error with explanation if invalid.
func ValidateAgentName(name string) error {
	if name == "" {
		return fmt.Errorf("agent name cannot be empty")
	}

	// Check if name is reserved
	if reservedNames[name] {
		return fmt.Errorf("agent name '%s' is reserved and cannot be used", name)
	}

	// Check character set
	if !agentNameRegex.MatchString(name) {
		return fmt.Errorf("agent name '%s' contains invalid characters; only lowercase letters (a-z), digits (0-9), and underscores (_) are allowed", name)
	}

	return nil
}

// ParseAgentID parses an agent ID and extracts the role and hash components.
// Handles three formats for backward compatibility:
//   - Legacy: "agent:role:hash" -> returns (role, hash)
//   - Unnamed: "role_hash" -> returns (role, hash)
//   - Named: "name" -> returns ("", "")
//
// To distinguish unnamed "role_hash" from named "name_with_underscores":
// - Hashes are base32 encoded (Crockford alphabet: uppercase letters + digits only)
// - Named agents can have lowercase letters.
func ParseAgentID(agentID string) (role, hash string) {
	// Legacy format: agent:role:hash
	if strings.HasPrefix(agentID, "agent:") {
		parts := strings.Split(agentID, ":")
		if len(parts) >= 3 {
			return parts[1], parts[2]
		}
		return "", ""
	}

	// Check for underscore - could be unnamed format or named agent with underscores
	if strings.Contains(agentID, "_") {
		parts := strings.SplitN(agentID, "_", 2)
		if len(parts) == 2 {
			// Hash is always 10 characters of uppercase base32 (Crockford alphabet)
			// If the part after underscore is exactly 10 chars and all uppercase/digits,
			// it's the unnamed format. Otherwise, it's a named agent.
			potentialHash := parts[1]
			if len(potentialHash) == 10 && isBase32Hash(potentialHash) {
				return parts[0], parts[1]
			}
		}
	}

	// Named format: just the name (or name with underscores that don't match hash pattern)
	return "", ""
}

// isBase32Hash checks if a string looks like a Crockford base32 hash
// (uppercase letters and digits only, from the Crockford alphabet).
func isBase32Hash(s string) bool {
	// Crockford base32 alphabet: 0-9, A-Z except I, L, O, U
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// AgentIDToName converts an agent ID to a filename-safe name.
// Handles backward compatibility by converting legacy format to current unnamed format:
//   - Legacy "agent:coordinator:1B9K33T6RK" -> "coordinator_1B9K33T6RK"
//   - Unnamed "coordinator_1B9K33T6RK" -> "coordinator_1B9K33T6RK"
//   - Named "furiosa" -> "furiosa"
func AgentIDToName(agentID string) string {
	// Strip "agent:" prefix and replace remaining colons with underscores
	// This converts legacy format to current unnamed format
	name := strings.TrimPrefix(agentID, "agent:")
	return strings.ReplaceAll(name, ":", "_")
}

// ExtractDisplayName extracts a display name from an agent ID for UI presentation.
// Returns the name prefixed with @ for mention-style display:
//   - Legacy "agent:coordinator:1B9K33T6RK" -> "@coordinator"
//   - Unnamed "implementer_35HV62T9B9" -> "@implementer"
//   - Named "furiosa" -> "@furiosa"
func ExtractDisplayName(agentID string) string {
	role, _ := ParseAgentID(agentID)

	// If we got a role, return it
	if role != "" {
		return "@" + role
	}

	// For named agents, return the name
	return "@" + agentID
}
