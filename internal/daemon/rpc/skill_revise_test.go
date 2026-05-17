package rpc

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reviseFixture wraps newPromoteFixture — every revise test needs the
// same db + messenger + handler scaffolding, just without the stamper/
// scanner/promote-mutex machinery that HandleRevise never touches.
func newReviseFixture(t *testing.T) *promoteFixture { return newPromoteFixture(t) }

// callRevise dispatches HandleRevise and returns the typed response or
// the Go error (auth-style failures travel via the Go error path per
// the established HandleCheck pattern).
func (f *promoteFixture) callRevise(req SkillReviseRequest) (SkillReviseResponse, error) {
	f.t.Helper()
	params, err := json.Marshal(req)
	if err != nil {
		f.t.Fatalf("marshal request: %v", err)
	}
	res, err := f.handler.HandleRevise(context.Background(), params)
	if err != nil {
		return SkillReviseResponse{}, err
	}
	resp, ok := res.(SkillReviseResponse)
	if !ok {
		f.t.Fatalf("response type = %T, want SkillReviseResponse", res)
	}
	return resp, nil
}

func TestRevise_CoordinatorOnly(t *testing.T) {
	t.Parallel()
	f := newReviseFixture(t)
	insertTestAgent(t, f.db, "@researcher_x", "researcher")
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: w\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'", "BODY")

	_, err := f.callRevise(SkillReviseRequest{
		CallerAgentID: "@researcher_x",
		Path:          path,
		Findings:      "needs rework",
	})
	if err == nil {
		t.Fatal("expected unauthorized error, got nil")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("err = %v, want unauthorized", err)
	}
}

func TestRevise_ProposalNotFound(t *testing.T) {
	t.Parallel()
	f := newReviseFixture(t)
	bogus := filepath.Join(f.root, ".thrum", "agents", "@nobody", "proposed-skills", "ghost", "SKILL.md")

	resp, err := f.callRevise(SkillReviseRequest{
		CallerAgentID: "@coordinator_main",
		Path:          bogus,
		Findings:      "any",
	})
	if err != nil {
		t.Fatalf("HandleRevise returned Go error (expected response.Error instead): %v", err)
	}
	if resp.Error != ErrProposalNotFoundCode {
		t.Errorf("Error = %q, want %q", resp.Error, ErrProposalNotFoundCode)
	}
}

func TestRevise_SendsInboxMessage(t *testing.T) {
	t.Parallel()
	f := newReviseFixture(t)
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'",
		"WIDGET BODY")

	resp, err := f.callRevise(SkillReviseRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
		Findings:      "Overlaps with skill `existing-thing`. Fold A+B into B.",
	})
	if err != nil {
		t.Fatalf("HandleRevise: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.MessageID == "" {
		t.Error("MessageID should be populated")
	}
	calls := f.messenger.snapshot()
	if len(calls) != 1 {
		t.Fatalf("messenger calls = %d, want 1", len(calls))
	}
	// Recipient must be the path-derived submitter (the path's <author>
	// segment is the canonical source).
	if calls[0].To != "@alice" {
		t.Errorf("Recipient = %q, want @alice", calls[0].To)
	}
	// Body shape matches the AC template (canonical headings + Skill name + Path + Coordinator findings + Check-the-skill stub).
	body := calls[0].Body
	for _, want := range []string{
		"# Skill revision feedback",
		"**Skill:** widget",
		"**Path:** " + path,
		"## Coordinator findings",
		"Overlaps with skill `existing-thing`",
		"## Check-the-skill output",
		"stub: not run",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody:\n%s", want, body)
		}
	}
	// New thread for a fresh revision; threadID empty per send.go contract.
	if calls[0].ThreadID != "" {
		t.Errorf("ThreadID = %q, want empty (opens a new thread)", calls[0].ThreadID)
	}
}

func TestRevise_NeverWritesSubmitterFolder(t *testing.T) {
	t.Parallel()
	f := newReviseFixture(t)
	authorRoot := filepath.Join(f.root, ".thrum", "agents")
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'",
		"WIDGET BODY")

	// Snapshot every file under .thrum/agents/ before the call: name +
	// size + mtime per file. This is the AC's "filesystem-tracker"
	// substitute — we don't need a real syscall hook because the
	// fixture's filesystem is per-test (t.TempDir), so any write under
	// the snapshotted root would change the resulting map.
	before := snapshotTree(t, authorRoot)

	if _, err := f.callRevise(SkillReviseRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
		Findings:      "rework please",
	}); err != nil {
		t.Fatalf("HandleRevise: %v", err)
	}

	after := snapshotTree(t, authorRoot)
	if len(before) != len(after) {
		t.Fatalf("HandleRevise added/removed files under .thrum/agents/; before=%d after=%d", len(before), len(after))
	}
	for k, v := range before {
		got, ok := after[k]
		if !ok {
			t.Errorf("HandleRevise removed %s", k)
			continue
		}
		if got.size != v.size || got.mtime != v.mtime {
			t.Errorf("HandleRevise modified %s: before=%+v after=%+v", k, v, got)
		}
	}
}

func TestRevise_FrontmatterAuthorMismatchLogsWarning(t *testing.T) {
	t.Parallel()
	f := newReviseFixture(t)
	// Path's author segment is "@alice"; frontmatter says proposed_by is "@bob" — mismatch.
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@bob'\n  trigger_reason: 'test'",
		"BODY")

	resp, err := f.callRevise(SkillReviseRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
		Findings:      "rework",
	})
	if err != nil {
		t.Fatalf("HandleRevise: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	logs := f.logBuf.String()
	if !strings.Contains(logs, "submitter mismatch") {
		t.Errorf("expected slog.Warn line about submitter mismatch; got: %s", logs)
	}
	// Routing uses the path-derived author (canonical), not the frontmatter value.
	calls := f.messenger.snapshot()
	if len(calls) != 1 {
		t.Fatalf("messenger calls = %d, want 1", len(calls))
	}
	if calls[0].To != "@alice" {
		t.Errorf("Recipient = %q, want @alice (path is canonical)", calls[0].To)
	}
}

// --- helpers ---

// snapshotTree walks a directory and returns a map of rel-path → (size, mtime).
// Used to verify HandleRevise doesn't modify any submitter-owned files.
func snapshotTree(t *testing.T, root string) map[string]fileMetadata {
	t.Helper()
	out := map[string]fileMetadata{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = fileMetadata{size: info.Size(), mtime: info.ModTime().UnixNano()}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return out
}

type fileMetadata struct {
	size  int64
	mtime int64
}
