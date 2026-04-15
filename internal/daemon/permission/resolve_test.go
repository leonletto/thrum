package permission

import (
	"context"
	"sort"
	"testing"
)

type fakeStateQuery struct {
	active map[string]bool
	byRole map[string][]string
}

func (f *fakeStateQuery) IsAgentActive(ctx context.Context, name string) (bool, error) {
	return f.active[name], nil
}

func (f *fakeStateQuery) ListActiveAgentsByRole(ctx context.Context, role string) ([]string, error) {
	return f.byRole[role], nil
}

func TestResolveSupervisors_DefaultCoordinator(t *testing.T) {
	fake := &fakeStateQuery{
		byRole: map[string][]string{
			"coordinator": {"coordinator_main"},
		},
	}
	got, err := resolveSupervisorsWithQuery(context.Background(), fake, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0] != "@coordinator_main" {
		t.Errorf("got %v, want [@coordinator_main]", got)
	}
}

func TestResolveSupervisors_RoleBroadcastMultiple(t *testing.T) {
	fake := &fakeStateQuery{
		byRole: map[string][]string{
			"coordinator": {"coordinator_main", "coordinator_secondary"},
		},
	}
	got, err := resolveSupervisorsWithQuery(context.Background(), fake, []string{"coordinator"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	sort.Strings(got)
	want := []string{"@coordinator_main", "@coordinator_secondary"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveSupervisors_NamedAgent(t *testing.T) {
	fake := &fakeStateQuery{
		active: map[string]bool{"user:leon-letto": true},
	}
	got, err := resolveSupervisorsWithQuery(context.Background(), fake, []string{"@user:leon-letto"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0] != "@user:leon-letto" {
		t.Errorf("got %v, want [@user:leon-letto]", got)
	}
}

func TestResolveSupervisors_Mixed(t *testing.T) {
	fake := &fakeStateQuery{
		active: map[string]bool{"user:leon-letto": true},
		byRole: map[string][]string{"coordinator": {"coordinator_main"}},
	}
	got, err := resolveSupervisorsWithQuery(context.Background(), fake,
		[]string{"coordinator", "@user:leon-letto"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %v, want 2 entries", got)
	}
}

func TestResolveSupervisors_DeadNamedAgentFiltered(t *testing.T) {
	fake := &fakeStateQuery{
		active: map[string]bool{}, // nobody active
	}
	got, err := resolveSupervisorsWithQuery(context.Background(), fake, []string{"@coordinator_main"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("dead agent should be filtered, got %v", got)
	}
}

func TestResolveSupervisors_EmptyRoleMatchProducesNoResults(t *testing.T) {
	fake := &fakeStateQuery{
		byRole: map[string][]string{}, // no agents with this role
	}
	got, err := resolveSupervisorsWithQuery(context.Background(), fake, []string{"orchestrator"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("unknown role should produce no results, got %v", got)
	}
}

func TestResolveSupervisors_Deduplicated(t *testing.T) {
	fake := &fakeStateQuery{
		active: map[string]bool{"coordinator_main": true},
		byRole: map[string][]string{"coordinator": {"coordinator_main"}},
	}
	got, err := resolveSupervisorsWithQuery(context.Background(), fake,
		[]string{"coordinator", "@coordinator_main"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0] != "@coordinator_main" {
		t.Errorf("expected dedup to [@coordinator_main], got %v", got)
	}
}

func TestResolveSupervisors_StateInterfaceSatisfaction(t *testing.T) {
	// Compile-time assertion only: this test exists to make the
	// interface satisfaction explicit in the test file. The actual
	// assertion happens at package load time via the _ = (*state.State)(nil)
	// line in resolve.go.
	var _ stateQuery = (*fakeStateQuery)(nil)
}
