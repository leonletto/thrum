package guard

import "testing"

func TestConfig_DefaultsAllStrict(t *testing.T) {
	c := DefaultConfig()
	if c.CrossWorktree != ModeStrict {
		t.Errorf("cross_worktree: want strict, got %q", c.CrossWorktree)
	}
	if c.DeadPIDAutoReclaim != ModeStrict {
		t.Errorf("dead_pid_auto_reclaim: want strict, got %q", c.DeadPIDAutoReclaim)
	}
	if c.QuickstartSelfRename != ModeStrict {
		t.Errorf("quickstart_self_rename: want strict, got %q", c.QuickstartSelfRename)
	}
	if c.QuickstartNameCollision != ModeStrict {
		t.Errorf("quickstart_name_collision: want strict, got %q", c.QuickstartNameCollision)
	}
	if c.NonGitBootstrap != ModeStrict {
		t.Errorf("non_git_bootstrap: want strict, got %q", c.NonGitBootstrap)
	}
	if c.UnauthenticatedRPC != ModeStrict {
		t.Errorf("unauthenticated_rpc: want strict, got %q", c.UnauthenticatedRPC)
	}
	if c.DaemonWriterLiveness != ModeStrict {
		t.Errorf("daemon_writer_liveness: want strict, got %q", c.DaemonWriterLiveness)
	}
	if c.PrimeOwnership != ModeStrict {
		t.Errorf("prime_ownership: want strict, got %q", c.PrimeOwnership)
	}
}
