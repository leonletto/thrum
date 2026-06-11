package permission

import (
	"context"
	"testing"
)

// TestResolveSupervisorsFiltered_Locality is the thrum-x3fnh core: supervisor
// fan-out must reach ONLY agents local to this daemon (the agents table holds
// synced REMOTE agents — ListActiveAgentsByRole has no hostname filter — and
// relaying a permission/tool_confirmation nudge to a remote coordinator who
// holds neither the pane nor the nudge row is the all-night fleet-wide fanout
// storm). user: entries are exempt (no identity files — agents-table only,
// delivered via UI/bridges). Verified on BOTH the named and role paths.
func TestResolveSupervisorsFiltered_Locality(t *testing.T) {
	fake := &fakeStateQuery{
		active: map[string]bool{
			"coordinator_local":  true,
			"coordinator_remote": true,
			"user:leon-letto":    true,
		},
		byRole: map[string][]string{
			"coordinator": {"coordinator_local", "coordinator_remote"},
		},
	}
	// Only the local coordinator has a local identity file.
	isLocal := func(name string) bool { return name == "coordinator_local" }

	t.Run("role path drops remote, keeps local", func(t *testing.T) {
		got, err := resolveSupervisorsFiltered(context.Background(), fake, []string{"coordinator"}, isLocal, "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		assertExactly(t, got, "@coordinator_local")
	})

	t.Run("named path drops remote, keeps local", func(t *testing.T) {
		got, err := resolveSupervisorsFiltered(context.Background(),
			fake, []string{"@coordinator_remote", "@coordinator_local"}, isLocal, "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		assertExactly(t, got, "@coordinator_local")
	})

	t.Run("user: entry is exempt from the local check", func(t *testing.T) {
		got, err := resolveSupervisorsFiltered(context.Background(),
			fake, []string{"@user:leon-letto", "@coordinator_remote"}, isLocal, "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		assertExactly(t, got, "@user:leon-letto")
	})

	t.Run("nil isLocal allows all (legacy/unit-test path)", func(t *testing.T) {
		got, err := resolveSupervisorsFiltered(context.Background(), fake, []string{"coordinator"}, nil, "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		assertExactly(t, got, "@coordinator_local", "@coordinator_remote")
	})
}

// TestResolveSupervisorsFiltered_OwnerExclusion pins the modal-owner
// self-exclusion (thrum-x3fnh, the 09:48Z self-referential datapoint): a
// modal-blocked agent cannot read its own inbox, so addressing the relay to
// itself is useless by construction. Excluded on both the named and role paths.
func TestResolveSupervisorsFiltered_OwnerExclusion(t *testing.T) {
	fake := &fakeStateQuery{
		active: map[string]bool{"coordinator_main": true, "coordinator_other": true},
		byRole: map[string][]string{"coordinator": {"coordinator_main", "coordinator_other"}},
	}
	allLocal := func(string) bool { return true }

	t.Run("role path excludes the owner", func(t *testing.T) {
		got, err := resolveSupervisorsFiltered(context.Background(), fake, []string{"coordinator"}, allLocal, "coordinator_main")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		assertExactly(t, got, "@coordinator_other")
	})

	t.Run("named path excludes the owner", func(t *testing.T) {
		got, err := resolveSupervisorsFiltered(context.Background(),
			fake, []string{"@coordinator_main", "@coordinator_other"}, allLocal, "coordinator_main")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		assertExactly(t, got, "@coordinator_other")
	})
}

func assertExactly(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want exactly %v", got, want)
	}
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("missing %q in %v", w, got)
		}
	}
}
