package main

import (
	"github.com/leonletto/thrum/internal/cli"
)

// primeRPCCaller is the subset of *cli.Client used by the
// snapshot-wiring helper. Defined as an interface so tests can
// inject a stub client without spinning up the full daemon —
// the Q-Spec-1 adaptation moved load-bearing risk into this thin
// wire-up, so the wire-up must be testable in isolation.
type primeRPCCaller interface {
	Call(method string, params any, result any) error
}

// wireSessionArchiveResponse invokes the daemon's session.archive
// RPC and populates result.RestartSnapshot + result.SessionDiscoveryHint
// from the response. Mutates result in place.
//
// Behavior matrix:
//
//	result == nil or result.Identity == nil:
//	  no-op (no agent to archive for)
//	client.Call fails:
//	  no-op; daemon logs the underlying error via slog. Prime
//	  output continues without snapshot context.
//	response.Content == nil:
//	  no snapshot existed; result.RestartSnapshot stays "".
//	response.DiscoveryHint == nil:
//	  agent has no past sessions yet; result.SessionDiscoveryHint
//	  stays "".
//	both Content + DiscoveryHint populated:
//	  result fields assigned from the response.
//
// This function is the Q-Spec-1 adaptation surface per coordinator
// decision in msg_01KRVRG4VPCEC1HEQSVCKKANV6: the spec assumed a
// daemon-side BuildPrimeContext that doesn't exist; the actual
// prime is CLI-orchestrated. This thin CLI wire is where the
// daemon-archived snapshot enters the prime output stream.
//
// Brainstormer-third pass on thrum-6qmf.15 flagged a test gap
// here — TestWireSessionArchiveResponse_* in
// prime_session_archive_test.go closes that gap.
func wireSessionArchiveResponse(client primeRPCCaller, result *cli.PrimeContext) {
	if result == nil || result.Identity == nil {
		return
	}
	var archiveResp struct {
		ArchivedPath  *string `json:"archived_path"`
		BigPicture    *string `json:"big_picture"`
		Content       *string `json:"content"`
		DiscoveryHint *string `json:"discovery_hint"`
	}
	archiveReq := map[string]string{"agent_id": result.Identity.AgentID}
	if err := client.Call("session.archive", archiveReq, &archiveResp); err != nil {
		// Non-fatal: prime continues without snapshot context.
		// The daemon-side handler logs the underlying error via
		// slog, surfacing through the cli sloghint bridge into
		// --json output's hints array.
		return
	}
	if archiveResp.Content != nil {
		result.RestartSnapshot = *archiveResp.Content
	}
	if archiveResp.DiscoveryHint != nil {
		result.SessionDiscoveryHint = *archiveResp.DiscoveryHint
	}
}
