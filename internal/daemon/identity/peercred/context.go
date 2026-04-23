package peercred

import "context"

// identityCtxKey is an unexported type used as a context.Value key. Because
// it is unexported and reference-compared, no other package can forge or
// overwrite the stored identity — the only way a value is present is via
// WithIdentity below, which is only called from the daemon's trusted
// connection-accept loop.
type identityCtxKey struct{}

// WithIdentity returns a new context carrying the given ResolvedIdentity.
// Passing a nil identity is allowed (callers use this to explicitly mark a
// context as "resolution attempted, no match" — i.e. anonymous).
func WithIdentity(ctx context.Context, id *ResolvedIdentity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// FromContext returns the ResolvedIdentity previously stored via WithIdentity,
// or (nil, false) if no identity was injected into this context.
//
// The boolean distinguishes:
//   - (id, true)    — resolved to a registered agent; id.AgentID is trusted
//   - (nil, true)   — peercred ran but returned ErrAnonymous (no match)
//   - (nil, false)  — peercred never ran (e.g. non-unix-socket transport, or
//     a test that didn't inject; the handler should fall back to legacy
//     behavior if it existed, or treat as anonymous by policy)
func FromContext(ctx context.Context) (*ResolvedIdentity, bool) {
	v := ctx.Value(identityCtxKey{})
	if v == nil {
		return nil, false
	}
	id, ok := v.(*ResolvedIdentity)
	return id, ok
}

// connectingPIDCtxKey is a separate unexported key from identityCtxKey so
// the connecting-process PID can be injected independently of identity
// resolution. The PID must remain available to handlers even when
// peercred.Resolve returned ErrAnonymous — guard checks (Rule #4‴) use the
// PID directly to walk the ancestor chain, without trusting any
// client-asserted agent_id.
type connectingPIDCtxKey struct{}

// WithConnectingPID returns a new context carrying the kernel-verified PID
// of the connecting process. The server extracts this PID via SO_PEERCRED
// (Linux) / LOCAL_PEERPID (macOS) before attempting identity resolution,
// so the PID is available to handlers regardless of whether the process
// was matched to a registered agent.
func WithConnectingPID(ctx context.Context, pid int) context.Context {
	return context.WithValue(ctx, connectingPIDCtxKey{}, pid)
}

// ConnectingPIDFromContext returns the connecting-process PID previously
// stored via WithConnectingPID, or (0, false) if none was injected.
// Handlers MUST treat ok=false as "no kernel-verified PID available" and
// fall back to per-method policy (e.g. reject mutating RPCs, allow
// read-only bootstrap calls).
func ConnectingPIDFromContext(ctx context.Context) (int, bool) {
	v := ctx.Value(connectingPIDCtxKey{})
	if v == nil {
		return 0, false
	}
	pid, ok := v.(int)
	return pid, ok
}
