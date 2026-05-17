package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/skills"
)

// TestNewSkillHandler_PanicsOnNilLibrary pins the constructor's
// nil-guard for the only field that's required at E10.2 (list/show
// path). The watcher uses the same panic-at-construction pattern
// (internal/skills/watcher.go WatcherOpts validation) so wiring drift
// surfaces at boot, not at the first RPC call 500 ms later.
func TestNewSkillHandler_PanicsOnNilLibrary(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil library; got none")
		}
	}()
	_ = NewSkillHandler(nil, nil, nil, nil, nil, nil)
}

// writeSKILL creates a SKILL.md with the supplied YAML frontmatter
// and body. Parent directories are created as needed.
func writeSKILL(t *testing.T, root, rel, fmYAML, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	contents := "---\n" + fmYAML + "\n---\n" + body
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatalf("write %s: %v", p, err)
	}
}

// newSkillHandlerForTest builds a SkillHandler wired with only the
// library — sufficient for list/show/check_status tests. The role
// check on check_status no-ops with a nil DB (mirrors email.go's
// requireAgentRegistered nil-DB fallback).
func newSkillHandlerForTest(t *testing.T, root string) *SkillHandler {
	t.Helper()
	return NewSkillHandler(skills.NewLibrary(root), nil, nil, nil, nil, nil)
}

func TestSkillList_ReturnsPromotedSkills(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSKILL(t, root, ".thrum/skills/foo/SKILL.md",
		"name: foo\ndescription: foo skill\nversion: 1.0.0", "foo body")
	writeSKILL(t, root, ".thrum/skills/bar/SKILL.md",
		"name: bar\ndescription: bar skill", "bar body")
	writeSKILL(t, root, ".thrum/skills/baz/SKILL.md",
		"name: baz\ndescription: baz skill", "baz body")

	h := newSkillHandlerForTest(t, root)
	params, _ := json.Marshal(SkillListRequest{CallerAgentID: "@tester"})
	res, err := h.HandleList(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	resp, ok := res.(SkillListResponse)
	if !ok {
		t.Fatalf("response type = %T, want SkillListResponse", res)
	}
	entries, ok := resp.Skills.([]SkillListEntry)
	if !ok {
		t.Fatalf("Skills type = %T, want []SkillListEntry", resp.Skills)
	}
	if got := len(entries); got != 3 {
		t.Fatalf("len(entries) = %d, want 3", got)
	}
	// Library.List sorts alphabetically.
	want := []string{"bar", "baz", "foo"}
	for i, w := range want {
		if entries[i].Name != w {
			t.Errorf("entries[%d].Name = %q, want %q", i, entries[i].Name, w)
		}
	}
	// Description + version flattened from frontmatter.
	for _, e := range entries {
		if e.Description == "" {
			t.Errorf("%s: empty description", e.Name)
		}
		if e.Path == "" {
			t.Errorf("%s: empty path", e.Name)
		}
	}
}

func TestSkillList_PendingFilter(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSKILL(t, root, ".thrum/agents/alice/proposed-skills/widget/SKILL.md",
		"name: widget\ndescription: alice widget\nthrum:\n  proposed_by: '@alice'", "widget body")
	writeSKILL(t, root, ".thrum/agents/bob/proposed-skills/gadget/SKILL.md",
		"name: gadget\ndescription: bob gadget\nthrum:\n  proposed_by: '@bob'", "gadget body")

	h := newSkillHandlerForTest(t, root)
	params, _ := json.Marshal(SkillListRequest{CallerAgentID: "@tester", Pending: true})
	res, err := h.HandleList(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleList pending: %v", err)
	}
	resp := res.(SkillListResponse)
	entries, ok := resp.Skills.([]ProposedSkillEntry)
	if !ok {
		t.Fatalf("Skills type = %T, want []ProposedSkillEntry", resp.Skills)
	}
	if got := len(entries); got != 2 {
		t.Fatalf("len(entries) = %d, want 2", got)
	}
	// Library.ListPending sorts by author, then name → alice/widget, bob/gadget.
	if entries[0].Name != "widget" || entries[1].Name != "gadget" {
		t.Errorf("ordering: got %s, %s — want widget, gadget", entries[0].Name, entries[1].Name)
	}
	// age_hours is populated and within a sane range for a just-written file.
	for i, e := range entries {
		if e.AgeHours < 0 || e.AgeHours > 1 {
			t.Errorf("entries[%d].AgeHours = %f, want 0..1 for just-written file", i, e.AgeHours)
		}
		if e.ProposedBy == "" {
			t.Errorf("entries[%d].ProposedBy is empty", i)
		}
	}
}

func TestSkillList_ProposedByFilter(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSKILL(t, root, ".thrum/agents/alice/proposed-skills/widget/SKILL.md",
		"name: widget\ndescription: alice widget\nthrum:\n  proposed_by: '@alice'", "widget body")
	writeSKILL(t, root, ".thrum/agents/bob/proposed-skills/gadget/SKILL.md",
		"name: gadget\ndescription: bob gadget\nthrum:\n  proposed_by: '@bob'", "gadget body")

	h := newSkillHandlerForTest(t, root)
	params, _ := json.Marshal(SkillListRequest{CallerAgentID: "@tester", Pending: true, ProposedBy: "@alice"})
	res, err := h.HandleList(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleList filtered: %v", err)
	}
	entries := res.(SkillListResponse).Skills.([]ProposedSkillEntry)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Name != "widget" {
		t.Errorf("Name = %q, want widget", entries[0].Name)
	}
}

func TestSkillShow_RendersFrontmatterAndBody(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSKILL(t, root, ".thrum/skills/foo/SKILL.md",
		"name: foo\ndescription: foo skill\nversion: 1.0.0", "BODY CONTENT")

	h := newSkillHandlerForTest(t, root)
	params, _ := json.Marshal(SkillShowRequest{CallerAgentID: "@tester", Name: "foo"})
	res, err := h.HandleShow(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleShow: %v", err)
	}
	resp, ok := res.(SkillShowResponse)
	if !ok {
		t.Fatalf("response type = %T, want SkillShowResponse", res)
	}
	if resp.Frontmatter.Name != "foo" {
		t.Errorf("Frontmatter.Name = %q, want foo", resp.Frontmatter.Name)
	}
	if !strings.Contains(resp.Body, "BODY CONTENT") {
		t.Errorf("Body missing expected content: %q", resp.Body)
	}
	if resp.Raw != "" {
		t.Errorf("Raw should be empty without IncludeRaw, got %q", resp.Raw)
	}
}

func TestSkillShow_IncludeRawAppendsBytes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSKILL(t, root, ".thrum/skills/foo/SKILL.md",
		"name: foo\ndescription: foo skill", "BODY")

	h := newSkillHandlerForTest(t, root)
	params, _ := json.Marshal(SkillShowRequest{CallerAgentID: "@tester", Name: "foo", IncludeRaw: true})
	res, err := h.HandleShow(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleShow include_raw: %v", err)
	}
	resp := res.(SkillShowResponse)
	if resp.Raw == "" {
		t.Fatal("Raw should be populated when IncludeRaw=true")
	}
	// Raw is the full file — should contain both the YAML delimiter and the body.
	if !strings.Contains(resp.Raw, "---") || !strings.Contains(resp.Raw, "BODY") {
		t.Errorf("Raw missing expected content: %q", resp.Raw)
	}
}

// TestSkillShow_PendingPathWorks covers spec §19 AC E10 #4: skill.show
// must accept a path argument pointing into a proposed-skill directory
// and return the proposal's frontmatter + body. Promote pre-flight
// (E10.4) uses this to render the proposal to the coordinator.
func TestSkillShow_PendingPathWorks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rel := ".thrum/agents/alice/proposed-skills/widget/SKILL.md"
	writeSKILL(t, root, rel,
		"name: widget\ndescription: alice widget\nthrum:\n  proposed_by: '@alice'", "WIDGET BODY")

	h := newSkillHandlerForTest(t, root)
	params, _ := json.Marshal(SkillShowRequest{
		CallerAgentID: "@tester",
		Path:          filepath.Join(root, rel),
	})
	res, err := h.HandleShow(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleShow path: %v", err)
	}
	resp := res.(SkillShowResponse)
	if resp.Frontmatter.Name != "widget" {
		t.Errorf("Frontmatter.Name = %q, want widget", resp.Frontmatter.Name)
	}
	if !strings.Contains(resp.Body, "WIDGET BODY") {
		t.Errorf("Body missing content: %q", resp.Body)
	}
}

func TestSkillCheckStatus_StubError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// nil DB → requireCoordinator no-ops (test bypass per helper docstring),
	// so the call proceeds to the stub response.
	h := newSkillHandlerForTest(t, root)
	params, _ := json.Marshal(SkillCheckStatusRequest{
		CallerAgentID: "@coordinator_main",
		CheckID:       "chk_anything",
	})
	res, err := h.HandleCheckStatus(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleCheckStatus: %v", err)
	}
	resp, ok := res.(SkillCheckStatusResponse)
	if !ok {
		t.Fatalf("response type = %T, want SkillCheckStatusResponse", res)
	}
	if resp.Status != "error" {
		t.Errorf("Status = %q, want error", resp.Status)
	}
	if resp.Error != ErrCheckSkillNotAvailableCode {
		t.Errorf("Error = %q, want %q", resp.Error, ErrCheckSkillNotAvailableCode)
	}
}

// TestSkillList_MissingCallerAgentID confirms the unauthorized-on-
// empty caller_agent_id guard fires for the any-agent read RPCs
// (no peercred fallback at the v0.11 first-ship layer; CLI is
// expected to resolve identity before dispatch).
func TestSkillList_MissingCallerAgentID(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	h := newSkillHandlerForTest(t, root)
	params, _ := json.Marshal(SkillListRequest{})
	_, err := h.HandleList(context.Background(), params)
	if err == nil || !strings.Contains(err.Error(), "caller_agent_id") {
		t.Errorf("want unauthorized error mentioning caller_agent_id, got: %v", err)
	}
}

// TestSkillShow_NameOrPathRequired confirms the request validator
// rejects the empty-name + empty-path combination — without a target
// there's nothing to render.
func TestSkillShow_NameOrPathRequired(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	h := newSkillHandlerForTest(t, root)
	params, _ := json.Marshal(SkillShowRequest{CallerAgentID: "@tester"})
	_, err := h.HandleShow(context.Background(), params)
	if err == nil || !strings.Contains(err.Error(), "name or path") {
		t.Errorf("want 'name or path required' error, got: %v", err)
	}
}
