package reconcile

import (
	"errors"
	"testing"
)

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
