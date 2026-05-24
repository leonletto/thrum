package main

import (
	"testing"
)

// TestQuickstartCmd_ArgsConstraint — thrum-9dnh regression. The
// quickstart command's cobra Args constraint must:
//   1. Accept zero positionals (caller uses --name or has none yet)
//   2. Accept one positional (the agent name, lenient form per §4d)
//   3. Reject two or more positionals (no second-name-arg confusion)
//
// Before this constraint, cobra defaulted to ArbitraryArgs and silently
// discarded positionals — the upstream footgun that caused the
// thrum-l9e1 cross-worktree-rebind repro (positional name discarded,
// name-fallback adopted stale identity-file name).
func TestQuickstartCmd_ArgsConstraint(t *testing.T) {
	cmd := quickstartCmd()
	if cmd.Args == nil {
		t.Fatal("quickstartCmd has no Args constraint — cobra defaults to ArbitraryArgs which silently discards positionals (the thrum-9dnh footgun)")
	}

	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"zero positionals", nil, false},
		{"one positional", []string{"alice"}, false},
		{"two positionals rejected", []string{"alice", "bob"}, true},
		{"three positionals rejected", []string{"a", "b", "c"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cmd.Args(cmd, tc.args)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Errorf("Args(%v) err=%v; wantErr=%v", tc.args, err, tc.wantErr)
			}
		})
	}
}
