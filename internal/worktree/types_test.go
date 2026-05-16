package worktree

import "testing"

func TestCreateOpts_ZeroValue(t *testing.T) {
	var opts CreateOpts
	if opts.RepoPath != "" {
		t.Errorf("zero RepoPath: got %q, want \"\"", opts.RepoPath)
	}
	if opts.Persistent {
		t.Error("zero Persistent: got true, want false")
	}
	if opts.WakeTimestamp != 0 {
		t.Errorf("zero WakeTimestamp: got %d, want 0", opts.WakeTimestamp)
	}
}

func TestCreateResult_ZeroValue(t *testing.T) {
	var r CreateResult
	if r.Reused {
		t.Error("zero Reused: got true, want false")
	}
}

func TestDestroyOpts_ZeroValue(t *testing.T) {
	var opts DestroyOpts
	if opts.Force {
		t.Error("zero Force: got true, want false")
	}
}
