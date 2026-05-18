package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/leonletto/thrum/internal/skills"
)

// recordingEnqueuer captures every EnqueueAll call so the delete test
// can assert that a Kind=delete event fanned out to the worker. Real
// *mirror.Worker is concrete and depends on a full Start lifecycle;
// the rpc handler only needs the interface surface, so a fake here is
// cheaper than wiring a real worker per test.
type recordingEnqueuer struct {
	mu     sync.Mutex
	events []skills.MirrorEvent
	count  int
	err    error
}

func (e *recordingEnqueuer) EnqueueAll(event skills.MirrorEvent) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return 0, e.err
	}
	e.events = append(e.events, event)
	return e.count, nil
}

func (e *recordingEnqueuer) snapshot() []skills.MirrorEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]skills.MirrorEvent, len(e.events))
	copy(out, e.events)
	return out
}

// callDelete dispatches HandleDelete and returns the typed response or
// the Go error. Auth-style failures travel via the Go error path.
func (f *promoteFixture) callDelete(req SkillDeleteRequest) (SkillDeleteResponse, error) {
	f.t.Helper()
	params, err := json.Marshal(req)
	if err != nil {
		f.t.Fatalf("marshal: %v", err)
	}
	res, err := f.handler.HandleDelete(context.Background(), params)
	if err != nil {
		return SkillDeleteResponse{}, err
	}
	resp, ok := res.(SkillDeleteResponse)
	if !ok {
		f.t.Fatalf("response type = %T, want SkillDeleteResponse", res)
	}
	return resp, nil
}

func TestDelete_CoordinatorOnly(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	insertTestAgent(t, f.db, "@researcher_x", "researcher")
	f.writePromoted("widget",
		"name: widget\ndescription: a widget", "BODY")

	_, err := f.callDelete(SkillDeleteRequest{
		CallerAgentID: "@researcher_x",
		Name:          "widget",
	})
	if err == nil {
		t.Fatal("expected unauthorized error, got nil")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("err = %v, want unauthorized", err)
	}
}

func TestDelete_RemovesCanonical(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	skillDir := filepath.Join(f.root, ".thrum", "skills", "widget")
	f.writePromoted("widget",
		"name: widget\ndescription: a widget", "BODY")
	if _, err := os.Stat(skillDir); err != nil {
		t.Fatalf("setup: skill dir missing pre-call: %v", err)
	}

	resp, err := f.callDelete(SkillDeleteRequest{
		CallerAgentID: "@coordinator_main",
		Name:          "widget",
		Reason:        "obsolete",
	})
	if err != nil {
		t.Fatalf("HandleDelete: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.DeletedAt.IsZero() {
		t.Error("DeletedAt should be populated")
	}
	if _, statErr := os.Stat(skillDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("skill dir should be removed; stat err = %v", statErr)
	}
}

func TestDelete_TriggersMirrorCleanup(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	enq := &recordingEnqueuer{count: 3}
	f.handler.enqueuer = enq
	f.writePromoted("widget",
		"name: widget\ndescription: a widget", "BODY")

	resp, err := f.callDelete(SkillDeleteRequest{
		CallerAgentID: "@coordinator_main",
		Name:          "widget",
	})
	if err != nil {
		t.Fatalf("HandleDelete: %v", err)
	}
	if resp.MirrorsCleared != 3 {
		t.Errorf("MirrorsCleared = %d, want 3", resp.MirrorsCleared)
	}
	events := enq.snapshot()
	if len(events) != 1 {
		t.Fatalf("EnqueueAll calls = %d, want 1", len(events))
	}
	if events[0].Kind != skills.MirrorEventKindDelete {
		t.Errorf("event.Kind = %q, want delete", events[0].Kind)
	}
	if events[0].SkillName != "widget" {
		t.Errorf("event.SkillName = %q, want widget", events[0].SkillName)
	}
}

func TestDelete_AuditLogEmitted(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	f.writePromoted("widget",
		"name: widget\ndescription: a widget", "BODY")

	if _, err := f.callDelete(SkillDeleteRequest{
		CallerAgentID: "@coordinator_main",
		Name:          "widget",
		Reason:        "superseded by foo",
		Force:         true,
	}); err != nil {
		t.Fatalf("HandleDelete: %v", err)
	}
	logs := f.logBuf.String()
	if !strings.Contains(logs, "skill deleted") {
		t.Errorf("audit log missing 'skill deleted': %s", logs)
	}
	for _, want := range []string{
		`name=widget`,
		`caller=@coordinator_main`,
		`reason="superseded by foo"`,
		`force=true`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("audit log missing %q: %s", want, logs)
		}
	}
}

func TestDelete_NotFoundError(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)

	resp, err := f.callDelete(SkillDeleteRequest{
		CallerAgentID: "@coordinator_main",
		Name:          "ghost",
	})
	if err != nil {
		t.Fatalf("HandleDelete returned Go error (expected response.Error): %v", err)
	}
	if resp.Error != ErrSkillNotFoundCode {
		t.Errorf("Error = %q, want %q", resp.Error, ErrSkillNotFoundCode)
	}
}

// TestDelete_ForceSkipsPromptAtCli is the CLI-layer counterpart; the
// daemon ignores Force per the AC. Verifying CLI behavior here is the
// gate per plan AC E10.6 line 1776. See cmd/thrum/skill_test.go for the
// cobra-tree side; this stub keeps the test name visible in the
// daemon package's test surface so plan-AC name traceability is
// preserved if someone greps for the test by name.
func TestDelete_ForceSkipsPromptAtCli(t *testing.T) {
	t.Skip("CLI behavior covered by cmd/thrum/skill_test.go TestSkillDelete_ForceSkipsPrompt")
}
