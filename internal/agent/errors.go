// Package agent defines the Agent struct + AgentRegistry interface that
// B-B1 consumers (NudgeHandler, Respawner, ack-* RPC handlers) use to
// read and mutate per-agent registration + runtime state.
//
// The package is intentionally minimal — types + interface in registry.go,
// the canonical SQLite implementation in registry_sqlite.go, and the
// sentinel error here. Stays free of daemon-internal imports so any
// daemon-side package can depend on it.
package agent

import "errors"

// ErrAgentNotFound is returned by AgentRegistry.Lookup when no agents-
// table row matches the requested name. Callers MUST check via
// errors.Is rather than string-matching — the SQLite implementation
// wraps it with context, so a bare equality check on the error value
// would miss wrapped instances.
var ErrAgentNotFound = errors.New("agent: not registered")
