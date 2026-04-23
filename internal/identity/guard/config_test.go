package guard

import "testing"

func TestConfig_MergePrecedence(t *testing.T) {
	base := DefaultConfig()                       // all strict
	repo := Config{CrossWorktree: ModeWarn}       // override one
	daemon := Config{UnauthenticatedRPC: ModeOff} // override another

	result := Merge(base, repo, daemon)
	if result.CrossWorktree != ModeWarn {
		t.Errorf("repo override: want warn, got %q", result.CrossWorktree)
	}
	if result.UnauthenticatedRPC != ModeOff {
		t.Errorf("daemon override: want off, got %q", result.UnauthenticatedRPC)
	}
	if result.PrimeOwnership != ModeStrict {
		t.Errorf("unoverridden stays default strict, got %q", result.PrimeOwnership)
	}
}

func TestConfig_MergeDaemonWinsOverRepo(t *testing.T) {
	base := DefaultConfig()
	repo := Config{CrossWorktree: ModeWarn}
	daemon := Config{CrossWorktree: ModeOff}

	result := Merge(base, repo, daemon)
	if result.CrossWorktree != ModeOff {
		t.Errorf("daemon should win over repo: want off, got %q", result.CrossWorktree)
	}
}

func TestConfig_MergeEmptyLayersKeepBase(t *testing.T) {
	base := DefaultConfig()
	result := Merge(base, Config{}, Config{})
	if result != base {
		t.Errorf("empty repo+daemon should preserve base, got %+v", result)
	}
}

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
