//go:build !unix

package guard

import "errors"

// ErrUnsupportedPlatform surfaces AtomicWrite's non-unix failure mode.
// Kept behind the build tag because unix builds have no need for it
// and would otherwise trip dead-code linters.
var ErrUnsupportedPlatform = errors.New("atomic identity writes require a unix platform (fcntl/flock)")

// AtomicWrite is not supported on non-unix build targets — thrum's
// identity-file discipline depends on BSD flock(2) advisory locking,
// which has no portable equivalent on Windows. Shipping a best-effort
// fallback (plain tmpfile + rename without a lock) would permit
// concurrent writers to corrupt identity files, so callers receive
// ErrUnsupportedPlatform and the failure surfaces loudly.
func AtomicWrite(_ string, _ []byte) error {
	return ErrUnsupportedPlatform
}
