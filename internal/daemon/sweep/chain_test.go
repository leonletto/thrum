package sweep

import (
	"context"
	"reflect"
	"testing"
)

func TestChain_ConfigWins(t *testing.T) {
	cfg := ChainConfig{
		AlertChain:          []string{"@coord", "leon@example.com"},
		SupervisorAgentName: "coordinator",
	}
	r := NewChainResolver(cfg)
	got, err := r.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"@coord", "leon@example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestChain_FallbackToSupervisor(t *testing.T) {
	cfg := ChainConfig{
		SupervisorAgentName: "coordinator_main",
	}
	r := NewChainResolver(cfg)
	got, _ := r.Resolve(context.Background())
	want := []string{"@coordinator_main"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestChain_NoConfigAndNoSupervisorOverride_UsesDefault(t *testing.T) {
	// escalation.supervisor_agent_name has canonical default "coordinator";
	// resolver returns ["@coordinator"] when nothing else is configured.
	r := NewChainResolver(ChainConfig{}) // both fields zero
	got, _ := r.Resolve(context.Background())
	want := []string{"@coordinator"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected default supervisor fallback, got %v", got)
	}
}

func TestChain_SupervisorWithAtPrefix_NotDoubled(t *testing.T) {
	// Caller may pass "@coordinator" (with leading @) — resolver should
	// not produce "@@coordinator".
	cfg := ChainConfig{SupervisorAgentName: "@coordinator_main"}
	r := NewChainResolver(cfg)
	got, _ := r.Resolve(context.Background())
	want := []string{"@coordinator_main"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("@-prefix should not be doubled: got %v want %v", got, want)
	}
}

func TestChain_SupervisorWithoutAtPrefix_GetsAt(t *testing.T) {
	// Bare "coordinator" → emit "@coordinator" (resolver normalizes).
	cfg := ChainConfig{SupervisorAgentName: "billing_supervisor"}
	r := NewChainResolver(cfg)
	got, _ := r.Resolve(context.Background())
	want := []string{"@billing_supervisor"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestChain_NeverEmpty(t *testing.T) {
	// Every plausible config combination yields at least one recipient.
	// Critical invariant: a sweep-minted reminder with empty
	// target_chain would fail the polymorphism validator at mint, so
	// chain MUST be non-empty by construction.
	cases := []ChainConfig{
		{},
		{SupervisorAgentName: ""},
		{SupervisorAgentName: "x"},
		{AlertChain: []string{"@a"}},
		{AlertChain: []string{"@a", "user@b.com"}},
	}
	for _, c := range cases {
		r := NewChainResolver(c)
		got, err := r.Resolve(context.Background())
		if err != nil {
			t.Errorf("config %+v: unexpected error %v", c, err)
		}
		if len(got) == 0 {
			t.Errorf("config %+v: chain must be non-empty (canonical §4.4 invariant)", c)
		}
	}
}

func TestChain_ConfigSliceIsolation(t *testing.T) {
	// Mutating the returned slice MUST NOT mutate the config's
	// underlying AlertChain. Operators may re-resolve repeatedly via
	// the same ChainResolver instance; cross-call slice aliasing
	// would yield silent action-at-a-distance bugs.
	cfg := ChainConfig{AlertChain: []string{"@a", "@b"}}
	r := NewChainResolver(cfg)
	first, _ := r.Resolve(context.Background())
	first[0] = "@mutated"
	second, _ := r.Resolve(context.Background())
	if second[0] != "@a" {
		t.Errorf("Resolve returned aliased slice; config got mutated to %v", second)
	}
	// Also verify the config struct's backing array wasn't touched.
	if cfg.AlertChain[0] != "@a" {
		t.Errorf("config.AlertChain was mutated: %v", cfg.AlertChain)
	}
}
