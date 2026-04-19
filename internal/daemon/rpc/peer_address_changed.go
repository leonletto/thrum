package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

// AddressChangedFunc is called when a peer notifies us of an address change.
// PeerToken identifies the peer; newIP and newPort are the new network location.
type AddressChangedFunc func(peerToken, newIP, newPort string) error

// SubnetGuardFunc is the xir.29 same-subnet check. Receives the peer's
// transport and the cached (old) and proposed-new addresses. Returns
// nil to accept the change; any non-nil error blocks the update and
// surfaces to the caller with the --type repair hint.
//
// The function is responsible for its own subnet-comparison logic; the
// handler treats it as an opaque decision. Transport=="local" should
// return nil unconditionally (loopback is strictly stronger than
// same-subnet — see I6 review finding).
type SubnetGuardFunc func(transport, oldAddr, newAddr string) error

// PeerLookupFunc returns the cached address + transport for a peer
// identified by token. Used by the handler to feed SubnetGuardFunc.
// Returns ("", "", err) when the peer is unknown — the handler treats
// that as "cannot evaluate" and falls through to accept.
type PeerLookupFunc func(peerToken string) (oldAddr, transport string, err error)

// PeerAddressChangedHandler handles the peer.address_changed RPC.
//
// Behavior (in order):
//  1. Param validation.
//  2. If both guard and lookupPeer are non-nil, fetch (oldAddr, transport),
//     invoke guard(transport, oldAddr, newAddr); non-nil guard error
//     rejects the call with the --type repair hint.
//  3. If guard is set but lookupPeer is nil: guard receives empty
//     oldAddr+transport; guard implementation MUST treat empty
//     oldAddr as "cannot evaluate, accept" to preserve first-boot
//     behavior (no cached address yet). See xir.29 plan M11.
//  4. Invoke updateFn with the new IP/port.
type PeerAddressChangedHandler struct {
	updateFn   AddressChangedFunc
	guard      SubnetGuardFunc
	lookupPeer PeerLookupFunc
}

// NewPeerAddressChangedHandler creates a handler without the xir.29
// subnet guard. Backwards-compatible with pre-xir.29 behavior: every
// peer.address_changed call updates the cached address unconditionally.
func NewPeerAddressChangedHandler(fn AddressChangedFunc) *PeerAddressChangedHandler {
	return &PeerAddressChangedHandler{updateFn: fn}
}

// NewPeerAddressChangedHandlerWithGuard creates a handler that runs the
// xir.29 same-subnet guard before calling updateFn. A non-nil guard
// error rejects the request with an error that names
// `thrum peer join --type repair` as the next step for the user.
//
// lookupPeer may be nil; in that case the guard receives empty
// oldAddr/transport (see Handle godoc for the accept-by-default
// semantics).
func NewPeerAddressChangedHandlerWithGuard(
	fn AddressChangedFunc,
	guard SubnetGuardFunc,
	lookupPeer PeerLookupFunc,
) *PeerAddressChangedHandler {
	return &PeerAddressChangedHandler{updateFn: fn, guard: guard, lookupPeer: lookupPeer}
}

// Handle parses the peer.address_changed params and — after the
// optional xir.29 subnet guard — invokes the update function.
func (h *PeerAddressChangedHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	var req struct {
		PeerToken string `json:"peer_token"`
		NewIP     string `json:"new_ip"`
		NewPort   string `json:"new_port"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if req.PeerToken == "" || req.NewIP == "" || req.NewPort == "" {
		return nil, fmt.Errorf("peer_token, new_ip, and new_port are required")
	}

	if h.guard != nil {
		var oldAddr, transport string
		if h.lookupPeer != nil {
			if a, tr, err := h.lookupPeer(req.PeerToken); err == nil {
				oldAddr = a
				transport = tr
			}
		}
		newAddr := req.NewIP + ":" + req.NewPort
		if err := h.guard(transport, oldAddr, newAddr); err != nil {
			return nil, fmt.Errorf("cross-subnet peer.address_changed rejected: %w; "+
				"run 'thrum peer join --type repair <name>' to re-pair on the new network", err)
		}
	}

	if err := h.updateFn(req.PeerToken, req.NewIP, req.NewPort); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
