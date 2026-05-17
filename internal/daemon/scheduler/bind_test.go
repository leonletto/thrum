package scheduler

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"
)

// TestMethods_RegistersAllTenRPCs pins the wire-registration surface:
// Methods() returns exactly the 10 method names canonical-ref §5.1 + §5.2
// list. Drift on either side (substrate adds a method without exposing
// it via Methods, OR canonical-ref adds without the substrate
// implementing) fails this test.
func TestMethods_RegistersAllTenRPCs(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	m := Methods(s)
	want := []string{
		"job.list",
		"job.show",
		"job.create",
		"job.update",
		"job.delete",
		"job.enable",
		"job.disable",
		"job.cancel",
		"job.history",
		"job.done",
	}
	if len(m) != len(want) {
		t.Errorf("got %d methods; want %d", len(m), len(want))
	}
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	for i := range want {
		if i >= len(got) {
			t.Errorf("missing method %q", want[i])
			continue
		}
		if got[i] != want[i] {
			t.Errorf("position %d: got %q; want %q", i, got[i], want[i])
		}
	}
}

// TestMethods_JobListClosureRoundTrip exercises one closure end-to-end
// to verify the JSON-RPC adapter unmarshals params, calls the typed
// method, and returns the typed response — proves the wire-level glue
// matches the substrate's Go-level signature.
func TestMethods_JobListClosureRoundTrip(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.RegisterInternal("internal.backup", "@every 1h", InternalOpts{}, &noopHandler{})
	s.mu.Lock()
	s.specs["docs-bot"] = JobSpec{
		ID: "docs-bot", Type: "scheduled_agent",
		Schedule: "0 9 * * *", Enabled: true,
	}
	s.mu.Unlock()

	h := Methods(s)["job.list"]
	if h == nil {
		t.Fatal("job.list handler missing")
	}
	// Empty params: list all.
	out, err := h(context.Background(), nil)
	if err != nil {
		t.Fatalf("job.list (empty): %v", err)
	}
	resp, ok := out.(ListJobsResponse)
	if !ok {
		t.Fatalf("response type = %T; want ListJobsResponse", out)
	}
	if len(resp.Jobs) != 2 {
		t.Errorf("got %d jobs; want 2", len(resp.Jobs))
	}

	// Filtered: type=scheduled_agent.
	out, err = h(context.Background(), json.RawMessage(`{"type":"scheduled_agent"}`))
	if err != nil {
		t.Fatalf("job.list (filtered): %v", err)
	}
	filtered := out.(ListJobsResponse)
	if len(filtered.Jobs) != 1 || filtered.Jobs[0].ID != "docs-bot" {
		t.Errorf("filtered: got %+v", filtered.Jobs)
	}
}

// TestMethods_JobShowClosure_UnmarshalError: bad JSON params surface as
// an unmarshal error (which the daemon's RPC layer reports back to the
// client).
func TestMethods_JobShowClosure_UnmarshalError(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	h := Methods(s)["job.show"]
	if _, err := h(context.Background(), json.RawMessage(`{not json`)); err == nil {
		t.Error("expected unmarshal error for malformed JSON")
	}
}
