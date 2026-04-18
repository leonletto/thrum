//go:build !unix

package peercred

import "net"

// stubResolver is a no-op Resolver for non-unix platforms (Windows, etc.).
// Unix-domain sockets with SO_PEERCRED semantics are not available on these
// platforms, so every connection is treated as anonymous.
type stubResolver struct{}

// NewResolver returns a stub Resolver that always returns ErrAnonymous.
// This satisfies the build on non-unix platforms (e.g. Windows CI).
func NewResolver(_ AgentLister) Resolver {
	return &stubResolver{}
}

func (r *stubResolver) Resolve(_ net.Conn) (*ResolvedIdentity, error) {
	return nil, ErrAnonymous
}

// PIDFromConn is a stub on non-unix platforms where kernel peer credentials
// are unavailable. Always returns (0, ErrAnonymous) so callers treat the
// connection as anonymous.
func PIDFromConn(_ net.Conn) (int, error) {
	return 0, ErrAnonymous
}
