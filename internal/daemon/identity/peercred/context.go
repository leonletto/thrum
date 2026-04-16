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
