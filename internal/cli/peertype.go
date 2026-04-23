package cli

import (
	"errors"
	"fmt"
	"strings"
)

// PeerType is the explicit transport / operation choice for `thrum peer add`
// and `thrum peer join`. Per Leon (xir.27, 2026-04-19), `--type` is
// MANDATORY: there is no default. Missing-flag is a hard error that lists
// all four values with a one-line "when to use" each — the canonical
// instance of the Phase 2 CLI-hint pattern.
type PeerType string

const (
	// PeerTypeTailscale dials the peer over a tsnet listener using the
	// Tailscale CGNAT IP from the peercode. Requires THRUM_TS_AUTHKEY on
	// both sides for first-time setup.
	PeerTypeTailscale PeerType = "tailscale"

	// PeerTypeLocal dials the peer at 127.0.0.1:<wsPort>. No tsnet, no
	// LAN exposure. Used for same-host sibling repo bridges
	// (topology A in the Remote Agent Test Plan).
	PeerTypeLocal PeerType = "local"

	// PeerTypeNetwork dials the peer over the local LAN. The user supplies
	// --address <ip>; the daemon resolves which NIC owns the IP and emits
	// the corresponding peercode. No tsnet bring-up.
	PeerTypeNetwork PeerType = "network"

	// PeerTypeRepair re-verifies and reconciles an EXISTING peer entry
	// using the secrets stored in peers.json. Refuses to create new
	// pairings; the target peer must already be paired via one of the
	// other types. Valid only on `peer join` (rejected on `peer add`).
	PeerTypeRepair PeerType = "repair"
)

// ErrPeerTypeMissing is returned when --type is empty. Callers should
// surface MissingTypeMessage to the user verbatim — it is the canonical
// CLI-hint instance for the xir.27 design.
var ErrPeerTypeMissing = errors.New("--type is required")

// ErrPeerTypeUnknown is returned when --type is set but does not match
// any of the four supported values.
var ErrPeerTypeUnknown = errors.New("--type value is not recognized")

// MissingTypeMessage is the exact text printed when --type is missing.
// Lists all four values with a one-line "when to use" each. Phase 2
// CLI-hint pattern: error teaches the right next step.
const MissingTypeMessage = `--type is required. Choose one:

  --type tailscale   Cross-host via Tailscale (requires THRUM_TS_AUTHKEY).
                     Use when the other daemon is on a different machine
                     reachable through your tailnet.

  --type local       Same-host sibling repos.
                     Use when both daemons run on the same machine and
                     you want loopback-only messaging (no tsnet, no LAN).

  --type network     Same-LAN, no Tailscale. Requires --address <ip>.
                     Use when both daemons are on the same network
                     segment and you want direct TCP without tsnet.

  --type repair      Re-verify an existing peer entry.
                     Use when peers.json has drifted (e.g. after a
                     daemon_id rotation) and you want to reconcile via
                     stored secrets without a full re-pair. Valid only
                     on 'peer join'.`

// ParsePeerType validates a user-supplied --type string. Returns
// ErrPeerTypeMissing when raw is empty (so the caller can print
// MissingTypeMessage), or ErrPeerTypeUnknown when the value does not
// match any defined PeerType.
func ParsePeerType(raw string) (PeerType, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrPeerTypeMissing
	}
	pt := PeerType(strings.ToLower(trimmed))
	switch pt {
	case PeerTypeTailscale, PeerTypeLocal, PeerTypeNetwork, PeerTypeRepair:
		return pt, nil
	}
	return "", fmt.Errorf("%w: %q", ErrPeerTypeUnknown, raw)
}

// IsValidPeerType reports whether the given string parses to a known
// PeerType. Convenience for callers that only need a yes/no decision.
func IsValidPeerType(raw string) bool {
	_, err := ParsePeerType(raw)
	return err == nil
}
