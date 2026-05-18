package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// recordingReconciler captures every Reconcile / ReconcileNames call so
// the sync test can assert that the handler dispatched the right kind
// of reconcile (full vs scoped) without spinning up a full mirror
// worker.
type recordingReconciler struct {
	mu             sync.Mutex
	fullCalls      int
	scopedCalls    [][]string
	reconcileError error
}

func (r *recordingReconciler) Reconcile(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fullCalls++
	return r.reconcileError
}

func (r *recordingReconciler) ReconcileNames(_ context.Context, names []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scopedCalls = append(r.scopedCalls, append([]string(nil), names...))
	return r.reconcileError
}

func (r *recordingReconciler) snapshot() (full int, scoped [][]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.scopedCalls))
	for i, c := range r.scopedCalls {
		out[i] = append([]string(nil), c...)
	}
	return r.fullCalls, out
}

func (f *promoteFixture) callSync(req SkillSyncRequest) (SkillSyncResponse, error) {
	f.t.Helper()
	params, err := json.Marshal(req)
	if err != nil {
		f.t.Fatalf("marshal: %v", err)
	}
	res, err := f.handler.HandleSync(context.Background(), params)
	if err != nil {
		return SkillSyncResponse{}, err
	}
	resp, ok := res.(SkillSyncResponse)
	if !ok {
		f.t.Fatalf("response type = %T, want SkillSyncResponse", res)
	}
	return resp, nil
}

func TestSync_FullReconcile(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	rec := &recordingReconciler{}
	f.handler.reconciler = rec

	resp, err := f.callSync(SkillSyncRequest{
		CallerAgentID: "@coordinator_main",
	})
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	full, scoped := rec.snapshot()
	if full != 1 {
		t.Errorf("full Reconcile calls = %d, want 1", full)
	}
	if len(scoped) != 0 {
		t.Errorf("scoped Reconcile calls = %d, want 0 (names was null)", len(scoped))
	}
	if resp.ReconciledCount != 0 {
		// Full reconcile reports 0 — the worker doesn't surface a per-skill
		// count and we don't fake one. The CLI documents this.
		t.Errorf("ReconciledCount = %d, want 0 for full reconcile", resp.ReconciledCount)
	}
}

func TestSync_ScopedToNames(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	rec := &recordingReconciler{}
	f.handler.reconciler = rec

	resp, err := f.callSync(SkillSyncRequest{
		CallerAgentID: "@coordinator_main",
		Names:         []string{"foo"},
	})
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	full, scoped := rec.snapshot()
	if full != 0 {
		t.Errorf("full Reconcile calls = %d, want 0 (scoped path)", full)
	}
	if len(scoped) != 1 {
		t.Fatalf("scoped Reconcile calls = %d, want 1", len(scoped))
	}
	if len(scoped[0]) != 1 || scoped[0][0] != "foo" {
		t.Errorf("scoped names = %v, want [foo]", scoped[0])
	}
	if resp.ReconciledCount != 1 {
		t.Errorf("ReconciledCount = %d, want 1 (one name scoped)", resp.ReconciledCount)
	}
}

func TestSync_ReturnsErrors(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	rec := &recordingReconciler{reconcileError: errors.New("disk full")}
	f.handler.reconciler = rec

	resp, err := f.callSync(SkillSyncRequest{
		CallerAgentID: "@coordinator_main",
	})
	if err != nil {
		t.Fatalf("HandleSync returned Go error (expected response.Errors instead): %v", err)
	}
	if len(resp.Errors) == 0 {
		t.Fatal("expected response.Errors to be populated")
	}
	if resp.Errors[0] == "" {
		t.Errorf("Errors[0] empty; want descriptive error string")
	}
}

// TestSync_AnyAgentAuth confirms spec §7.10's "any (identified) agent
// may call" rule — sync is NOT coordinator-gated.
func TestSync_AnyAgentAuth(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	insertTestAgent(t, f.db, "@researcher_x", "researcher")
	rec := &recordingReconciler{}
	f.handler.reconciler = rec

	if _, err := f.callSync(SkillSyncRequest{
		CallerAgentID: "@researcher_x",
	}); err != nil {
		t.Fatalf("HandleSync should accept any identified agent; got: %v", err)
	}
	full, _ := rec.snapshot()
	if full != 1 {
		t.Errorf("Reconcile not invoked for researcher: full calls = %d", full)
	}
}
