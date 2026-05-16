package worktree

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelErrors_Reachable(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"ErrPathExists", ErrPathExists},
		{"ErrPersistentBranchMismatch", ErrPersistentBranchMismatch},
		{"ErrInvalidOpts", ErrInvalidOpts},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wrapped := fmt.Errorf("context: %w", c.err)
			if !errors.Is(wrapped, c.err) {
				t.Errorf("errors.Is(wrapped, %s) = false; want true", c.name)
			}
		})
	}
}
