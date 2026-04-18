//go:build !unix

package guard

import "errors"

// ErrUnsupportedPlatform surfaces AtomicWrite's non-unix failure mode.
// Kept behind the build tag because unix builds have no need for it
// and would otherwise trip dead-code linters.
var ErrUnsupportedPlatform = errors.New("atomic identity writes require a unix platform (fcntl/flock)")

// AtomicWrite is not supported on non-unix build targets — thrum's
// identity-file discipline depends on fcntl advisory locking, which has
// no portable equivalent on Windows. Shipping a best-effort fallback
// would silently weaken Rule #4‴; callers instead receive
// ErrUnsupportedPlatform so the failure surfaces loudly.
func AtomicWrite(_ string, _ []byte) error {
	return ErrUnsupportedPlatform
}
