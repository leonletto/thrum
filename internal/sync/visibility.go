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

// canonicalRemoteIdentity reduces any remote URL form to a stable
// "<host>/owner/repo" identity used IDENTICALLY for both DetectedRemote (what
// the daemon writes) and PublicExposureOverride (what the user writes). A
// mismatch would silently keep a-sync off for someone who opted in, so this is
// the single source of truth for both. Lowercases host, strips userinfo, port,
// a trailing ".git", and leading/trailing slashes. Returns "" for non-network
// (local-path) remotes.
func canonicalRemoteIdentity(remoteURL string) string {
	probe, ok := deriveProbeURL(remoteURL)
	if !ok {
		return ""
	}
	// probe is always https://host/path (host already lowercased, no userinfo).
	rest := strings.TrimPrefix(probe, "https://")
	// Strip an optional :port on the host segment.
	if slash := strings.Index(rest, "/"); slash != -1 {
		host := rest[:slash]
		path := rest[slash:]
		if colon := strings.Index(host, ":"); colon != -1 {
			host = host[:colon]
		}
		rest = host + path
	}
	rest = strings.TrimSuffix(rest, "/")
	rest = strings.TrimSuffix(rest, ".git")
	return rest
}

// Visibility is the classified anonymous-readability of a remote.
type Visibility string

const (
	VisPublic       Visibility = "public"
	VisPrivate      Visibility = "private"
	VisUndetectable Visibility = "undetectable"
)

// deniedMarkers indicate the host was REACHED but refused/hid the repo: not
// anonymously readable ⇒ private ⇒ safe to sync. Covers 401 (GitLab) and 404
// (GitHub/Bitbucket/Codeberg/Azure) and the prompt-disabled path uniformly.
var deniedMarkers = []string{
	"could not read username",
	"authentication failed",
	"terminal prompts disabled",
	"repository not found",
	"not found",
	"access denied",
	"http 401",
	"http 403",
	"http 404",
	"permission denied",
}

// unreachableMarkers indicate transport failure (no definitive answer) ⇒
// undetectable ⇒ fail-closed.
var unreachableMarkers = []string{
	"could not resolve host",
	"connection refused",
	"connection timed out",
	"failed to connect",
	"ssl",
	"tls",
	"timed out",
	"network is unreachable",
}

// classifyVisibility maps an anonymous ls-remote result to a Visibility by
// reachability. Success with output ⇒ public (positive proof, the only leak
// direction). A definitive denial ⇒ private. Transport failure / ambiguous ⇒
// undetectable (never assume private on an unclear error).
func classifyVisibility(out []byte, err error) Visibility {
	if err == nil {
		if strings.TrimSpace(string(out)) != "" {
			return VisPublic
		}
		// Reachable, empty repo, anonymously readable ⇒ still public.
		return VisPublic
	}
	low := strings.ToLower(string(out))
	for _, m := range unreachableMarkers {
		if strings.Contains(low, m) {
			return VisUndetectable
		}
	}
	for _, m := range deniedMarkers {
		if strings.Contains(low, m) {
			return VisPrivate
		}
	}
	// Reached-but-unrecognized (e.g. 5xx) ⇒ do NOT assume private.
	return VisUndetectable
}

// ClassifyVisibility is the exported wrapper the daemon boot path uses (the
// boot path lives in package main / cmd and cannot call the unexported
// classifyVisibility). Keep classifyVisibility unexported for in-package tests.
func ClassifyVisibility(out []byte, err error) Visibility {
	return classifyVisibility(out, err)
}
