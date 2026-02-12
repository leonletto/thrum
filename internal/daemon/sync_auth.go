package daemon

import (
	"fmt"
	"log"
	"strings"
)

// SyncAuthorizer handles authorization of sync connections.
type SyncAuthorizer struct {
	allowedPeers  map[string]bool // hostname -> allowed
	requiredTags  []string        // e.g., ["tag:thrum-daemon"]
	allowedDomain string          // e.g., "@company.com"
	requireAuth   bool            // if false, all peers allowed (for testing)
}

// SyncAuthConfig holds configuration for sync authorization.
type SyncAuthConfig struct {
	AllowedPeers  []string `json:"allowed_peers" yaml:"allowed_peers"`
	RequiredTags  []string `json:"required_tags" yaml:"required_tags"`
	AllowedDomain string   `json:"allowed_domain" yaml:"allowed_domain"`
	RequireAuth   bool     `json:"require_auth" yaml:"require_auth"`
}

// NewSyncAuthorizer creates a new sync authorizer from config.
func NewSyncAuthorizer(cfg SyncAuthConfig) *SyncAuthorizer {
	a := &SyncAuthorizer{
		allowedPeers:  make(map[string]bool),
		requiredTags:  cfg.RequiredTags,
		allowedDomain: cfg.AllowedDomain,
		requireAuth:   cfg.RequireAuth,
	}

	// Convert allowed peers list to map for O(1) lookup
	for _, peer := range cfg.AllowedPeers {
		a.allowedPeers[peer] = true
	}

	return a
}

// AuthorizePeer checks if a peer with the given identity is authorized.
// peerHostname is the Tailscale hostname (from WhoIs).
// peerTags is the list of ACL tags (from WhoIs).
// peerLoginName is the user login (from WhoIs), e.g., "alice@company.com".
// Returns nil if authorized, error with reason if not.
func (a *SyncAuthorizer) AuthorizePeer(peerHostname string, peerTags []string, peerLoginName string) error {
	// If auth is not required, allow all peers (for dev/testing)
	if !a.requireAuth {
		log.Printf("[sync_auth] Authorization not required, allowing peer %s", peerHostname)
		return nil
	}

	// Track which checks are configured and their results
	var failedChecks []string

	// Check 1: Allowed peers list
	if len(a.allowedPeers) > 0 {
		if !a.allowedPeers[peerHostname] {
			failedChecks = append(failedChecks, fmt.Sprintf("hostname %q not in allowed peers list", peerHostname))
		}
	}

	// Check 2: Required tags
	if len(a.requiredTags) > 0 {
		hasRequiredTag := false
		for _, requiredTag := range a.requiredTags {
			for _, peerTag := range peerTags {
				if peerTag == requiredTag {
					hasRequiredTag = true
					break
				}
			}
			if hasRequiredTag {
				break
			}
		}
		if !hasRequiredTag {
			failedChecks = append(failedChecks, fmt.Sprintf("peer does not have any of the required tags %v (has: %v)", a.requiredTags, peerTags))
		}
	}

	// Check 3: Allowed domain
	if a.allowedDomain != "" {
		if !strings.HasSuffix(peerLoginName, a.allowedDomain) {
			failedChecks = append(failedChecks, fmt.Sprintf("login name %q does not end with allowed domain %q", peerLoginName, a.allowedDomain))
		}
	}

	// If any configured checks failed, deny access
	if len(failedChecks) > 0 {
		reason := strings.Join(failedChecks, "; ")
		log.Printf("[sync_auth] DENIED peer %s (login: %s, tags: %v): %s", peerHostname, peerLoginName, peerTags, reason)
		return fmt.Errorf("authorization failed: %s", reason)
	}

	// All configured checks passed
	log.Printf("[sync_auth] ALLOWED peer %s (login: %s, tags: %v)", peerHostname, peerLoginName, peerTags)
	return nil
}
