package reconcile

import (
	"context"
	"errors"
	"testing"
	"time"
)

// contextWithShortTimeout returns a 500ms-bounded context for WS dial
// smoke tests. Centralized so we can tune duration in one place if CI
// timing becomes noisy.
func contextWithShortTimeout(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 500*time.Millisecond)
}

func TestCategorizeErr_CoversKnownCategories(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrCategory
	}{
		{"nil→ok", nil, CatOK},
		{"unreachable→unreachable", ErrUnreachable, CatUnreachable},
		{"token-rejected→token-rejected", ErrTokenRejected, CatTokenRejected},
		{"wrapped-unreachable→unreachable", wrap(ErrUnreachable), CatUnreachable},
		{"wrapped-token→token-rejected", wrap(ErrTokenRejected), CatTokenRejected},
		{"arbitrary→other", errors.New("random transient"), CatOther},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CategorizeErr(tc.err)
			if got != tc.want {
				t.Errorf("CategorizeErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func wrap(err error) error {
	return &wrappedErr{inner: err}
}

type wrappedErr struct{ inner error }

func (w *wrappedErr) Error() string { return "wrapped: " + w.inner.Error() }
func (w *wrappedErr) Unwrap() error { return w.inner }

// TestWSDial_UnreachableAddressCategorizesCorrectly is a narrow smoke test:
// WSDial against a guaranteed-dead port surfaces CatUnreachable via the
// wrapped ErrUnreachable. The full WS+peer.repair roundtrip is covered by
// the in-process integration tests.
func TestWSDial_UnreachableAddressCategorizesCorrectly(t *testing.T) {
	ctx, cancel := contextWithShortTimeout(t)
	defer cancel()
	_, err := WSDial(ctx, "127.0.0.1:1", "tok", DialerIdentity{DaemonID: "self"})
	if CategorizeErr(err) != CatUnreachable {
		t.Errorf("expected CatUnreachable for port 1; got category=%v err=%v", CategorizeErr(err), err)
	}
}
