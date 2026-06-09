package sync

import (
	"net/url"
	"strings"
)

// deriveProbeURL converts an origin remote URL into an anonymous HTTPS URL
// suitable for an unauthenticated readability probe. SSH/scp origins are
// rewritten to https://host/path because there is no anonymous SSH read.
// Returns ("", false) when there is no network host (local-path/file remote),
// which the caller treats as not-probeable.
func deriveProbeURL(remoteURL string) (string, bool) {
	u := strings.TrimSpace(remoteURL)
	if u == "" {
		return "", false
	}
	// scp-like: [user@]host:path  (no "://", and a ':' before any '/')
	if !strings.Contains(u, "://") {
		at := strings.LastIndex(u, "@")
		hostpath := u
		if at != -1 {
			hostpath = u[at+1:]
		}
		colon := strings.Index(hostpath, ":")
		if colon == -1 {
			return "", false // bare local path
		}
		host := hostpath[:colon]
		path := strings.TrimPrefix(hostpath[colon+1:], "/")
		if host == "" || path == "" {
			return "", false
		}
		return "https://" + strings.ToLower(host) + "/" + path, true
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Hostname() == "" {
		return "", false
	}
	path := strings.TrimPrefix(parsed.Path, "/")
	if path == "" {
		return "", false
	}
	return "https://" + strings.ToLower(parsed.Hostname()) + "/" + path, true
}
