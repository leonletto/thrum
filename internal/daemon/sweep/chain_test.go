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

// --- ValidateChainConfig (brainstormer review fix: prevent dispatcher
// infinite-loop on email-only chains while EmailQueue is unwired) ---

func TestValidateChainConfig_PassesWhenEmailDeliveryWired(t *testing.T) {
	cfg := ChainConfig{AlertChain: []string{"leon@example.com", "ops@example.com"}}
	if err := ValidateChainConfig(cfg, true); err != nil {
		t.Errorf("post-D-B1 with email delivery wired: should pass; got %v", err)
	}
}

func TestValidateChainConfig_PassesOnEmptyChain(t *testing.T) {
	// Empty AlertChain → resolver falls back to single supervisor;
	// never email-only.
	if err := ValidateChainConfig(ChainConfig{}, false); err != nil {
		t.Errorf("empty AlertChain should pass (fallback to supervisor); got %v", err)
	}
}

func TestValidateChainConfig_PassesOnMixedChain(t *testing.T) {
	cfg := ChainConfig{AlertChain: []string{"@coordinator_main", "leon@example.com"}}
	if err := ValidateChainConfig(cfg, false); err != nil {
		t.Errorf("mixed chain (at least one @agent) should pass even without email; got %v", err)
	}
}

func TestValidateChainConfig_PassesOnAgentOnlyChain(t *testing.T) {
	cfg := ChainConfig{AlertChain: []string{"@coord1", "@coord2"}}
	if err := ValidateChainConfig(cfg, false); err != nil {
		t.Errorf("agent-only chain should pass; got %v", err)
	}
}

func TestValidateChainConfig_RejectsEmailOnlyWithoutDelivery(t *testing.T) {
	cfg := ChainConfig{AlertChain: []string{"leon@example.com"}}
	err := ValidateChainConfig(cfg, false)
	if err == nil {
		t.Fatal("email-only chain without delivery should reject (prevents dispatcher infinite-loop)")
	}
	// Error message should help operators understand the fix.
	for _, want := range []string{"only email", "supervisor_agent_name", "alert_chain"} {
		if !contains(err.Error(), want) {
			t.Errorf("error message missing %q (operator fix hint): %v", want, err)
		}
	}
}

func TestValidateChainConfig_RejectsMultipleEmailsWithoutDelivery(t *testing.T) {
	cfg := ChainConfig{AlertChain: []string{"leon@example.com", "ops@example.com"}}
	if err := ValidateChainConfig(cfg, false); err == nil {
		t.Error("multi-email-only chain should reject (still loops without delivery)")
	}
}

// contains is a tiny helper to avoid pulling strings into this small
// test file when sweep/chain_test.go doesn't already import it.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
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
