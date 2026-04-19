package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

// xir.27 sub-3: --type a-sync peer configuration.
//
// An a-sync peer is a directory entry backed by a shared git remote rather
// than a live WebSocket handshake. Adding one configures the repo's 'origin'
// remote (if unset) and stamps a PeerInfo row with Transport="a-sync" and
// ASyncRemote=<url>. The existing internal/sync/ loop does the rest: it
// operates on origin's a-sync branch and is unchanged by this feature.
//
// Security surface:
//   - Embedded credentials (https://user:token@host/...) MUST NOT leak into
//     error messages or logs. All error paths funnel through sanitizeURLForLog.
//   - Only a small whitelist of URL schemes is accepted.
//   - file:// is rejected: it bypasses git's authenticated-remote model and
//     breaks the invariant that a-sync peers reach each other through a host.

// allowedASyncSchemes lists the URL schemes accepted by --type a-sync.
var allowedASyncSchemes = map[string]bool{
	"https": true,
	"http":  true, // enterprise git hosts; users should prefer https
	"ssh":   true,
	"git":   true,
}

// sshShorthandRE matches SCP-style "user@host:path" git URLs. These have no
// URL scheme and are the most common SSH form in the wild.
var sshShorthandRE = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9._/~-]+$`)

// ValidateASyncRemoteURL reports whether raw is a permitted git remote URL
// for --type a-sync. Accepts both full URLs (https/ssh/git schemes) and
// SCP-style SSH shorthand (user@host:path). Rejects file://, empty strings,
// and any other scheme. Returned errors are safe to surface — they do not
// echo user credentials.
func ValidateASyncRemoteURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("--remote is required for --type a-sync")
	}

	// SCP-style shorthand has no scheme; treat as SSH.
	if !strings.Contains(trimmed, "://") {
		if sshShorthandRE.MatchString(trimmed) {
			return nil
		}
		return fmt.Errorf("--remote: not a recognized git URL (use https://, ssh://, git://, or user@host:path)")
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("--remote: invalid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if !allowedASyncSchemes[scheme] {
		return fmt.Errorf("--remote: scheme %q not allowed; use https, http, ssh, or git", scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("--remote: URL is missing a host")
	}
	return nil
}

// sanitizeURLForLog strips any embedded userinfo (user:password) from a URL so
// it is safe to include in error messages and logs. Non-URL strings (SCP
// shorthand, malformed URLs) are returned unchanged — those forms do not
// carry inline credentials.
func sanitizeURLForLog(raw string) string {
	if !strings.Contains(raw, "://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

// ASyncPeerDaemonID returns a deterministic placeholder daemon ID for a
// directory-only a-sync peer entry. Real daemon IDs are ULIDs; this prefixed
// form keeps a-sync entries distinguishable so future discovery (reading
// daemon.identity events off the shared a-sync branch) can either refine the
// entry or keep the placeholder if discovery fails. Format: "async:<16 hex>".
func ASyncPeerDaemonID(remoteURL string) string {
	canonical := strings.TrimSpace(remoteURL)
	sum := sha256.Sum256([]byte(canonical))
	return "async:" + hex.EncodeToString(sum[:8])
}

// ASyncPeerName derives a short, deterministic peer name from the remote URL.
// Used when a user does not supply an explicit name at peer-add time.
// The returned name is lowercase and URL-character-stripped; it is not
// guaranteed to be pretty, but it is stable across runs.
func ASyncPeerName(remoteURL string) string {
	s := sanitizeURLForLog(strings.TrimSpace(remoteURL))
	// Strip scheme.
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Strip SSH userinfo (user@host:...).
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	// Reduce to alphanumerics, collapse runs of separators.
	var b strings.Builder
	lastSep := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSep = false
		case r == '-' || r == '_':
			if !lastSep {
				b.WriteRune(r)
				lastSep = true
			}
		case r == '.' || r == '/' || r == ':':
			if !lastSep {
				b.WriteByte('-')
				lastSep = true
			}
		}
	}
	name := strings.TrimRight(b.String(), "-_")
	name = strings.ToLower(name)
	// Always prefix so the name is obviously transport-scoped.
	if name == "" {
		return "async-peer"
	}
	return "async-" + name
}

// ConfigureASyncRemote ensures the repository at repoPath has an 'origin'
// remote matching the supplied URL. Behavior:
//   - 'origin' not set → `git remote add origin <url>`.
//   - 'origin' set to the same URL → no-op (idempotent).
//   - 'origin' set to a different URL → error; user must remove the existing
//     remote or pick a consistent URL.
//
// Returned errors are safe to surface: embedded credentials are stripped
// before appearing in any message.
func ConfigureASyncRemote(ctx context.Context, repoPath, remoteURL string) error {
	if err := ValidateASyncRemoteURL(remoteURL); err != nil {
		return err
	}

	target := strings.TrimSpace(remoteURL)
	out, err := safecmd.Git(ctx, repoPath, "remote", "get-url", "origin")
	if err == nil {
		existing := strings.TrimSpace(string(out))
		if existing == target {
			return nil
		}
		return fmt.Errorf(
			"--type a-sync: 'origin' remote already points at %s; refusing to reconfigure to %s (remove the existing remote first if this is intentional)",
			sanitizeURLForLog(existing), sanitizeURLForLog(target),
		)
	}

	// 'origin' not configured — add it. Do not surface safecmd's wrapped
	// error directly: it includes the raw args, which would echo credentials.
	if _, err := safecmd.Git(ctx, repoPath, "remote", "add", "origin", target); err != nil {
		return fmt.Errorf("configure a-sync remote %s: git remote add origin failed", sanitizeURLForLog(target))
	}
	return nil
}

// VerifyASyncRemoteReachable runs `git ls-remote origin` in repoPath and
// returns nil if the remote is reachable with the caller's ambient git
// credentials. This is the same authentication path git normally uses
// (ssh-agent, ~/.git-credentials, credential helpers); no new credential
// handling is introduced by thrum.
//
// Errors are sanitized to avoid leaking the remote URL — if a user invokes
// peer add with an URL that contains an inline token, the underlying git
// error may echo it; we wrap and strip.
func VerifyASyncRemoteReachable(ctx context.Context, repoPath, remoteURL string) error {
	if _, err := safecmd.GitLong(ctx, repoPath, "ls-remote", "--exit-code", "origin"); err != nil {
		// Do not surface err (may embed args + URL). Return a sanitized message.
		return fmt.Errorf("a-sync remote %s unreachable: check git credentials and network", sanitizeURLForLog(remoteURL))
	}
	return nil
}
