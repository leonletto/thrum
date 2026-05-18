package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validFrontmatter returns a fully-stamped frontmatter that passes
// ValidatePromoted with no findings. Reused across the validate tests
// where the test cares about a SINGLE missing/duplicate field, not
// every required field.
func validFrontmatter(name string) string {
	return "name: " + name + "\n" +
		"description: " + name + " skill\n" +
		"thrum:\n" +
		"  proposed_by: '@alice'\n" +
		"  promoted_by: '@coordinator_main'\n" +
		"  created_at: '2026-05-17T18:00:00Z'\n" +
		"  trigger_reason: 'unit test'\n" +
		"  review:\n" +
		"    reviewed_by: '@coordinator_main'\n" +
		"    reviewed_at: '2026-05-17T18:00:00Z'\n" +
		"    check_skill_version: '0.0.0-stub'"
}

func (f *promoteFixture) callValidate(req SkillValidateRequest) (SkillValidateResponse, error) {
	f.t.Helper()
	params, err := json.Marshal(req)
	if err != nil {
		f.t.Fatalf("marshal: %v", err)
	}
	res, err := f.handler.HandleValidate(context.Background(), params)
	if err != nil {
		return SkillValidateResponse{}, err
	}
	resp, ok := res.(SkillValidateResponse)
	if !ok {
		f.t.Fatalf("response type = %T, want SkillValidateResponse", res)
	}
	return resp, nil
}

func TestValidate_AllSkillsClean(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	f.writePromoted("alpha", validFrontmatter("alpha"), "alpha body")
	f.writePromoted("beta", validFrontmatter("beta"), "beta body")

	resp, err := f.callValidate(SkillValidateRequest{
		CallerAgentID: "@tester",
	})
	if err != nil {
		t.Fatalf("HandleValidate: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("Results = %d, want 2", len(resp.Results))
	}
	for _, r := range resp.Results {
		if r.Status != "ok" {
			t.Errorf("%s: status = %q, want ok; findings: %+v", r.Name, r.Status, r.Findings)
		}
	}
}

func TestValidate_SingleNameFlagsMissingField(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	// Missing reviewed_by — ValidatePromoted flags missing_required.
	bad := "name: bad\n" +
		"description: bad skill\n" +
		"thrum:\n" +
		"  proposed_by: '@alice'\n" +
		"  promoted_by: '@coordinator_main'\n" +
		"  created_at: '2026-05-17T18:00:00Z'\n" +
		"  trigger_reason: 'unit test'\n" +
		"  review:\n" +
		"    reviewed_at: '2026-05-17T18:00:00Z'\n" +
		"    check_skill_version: '0.0.0-stub'"
	f.writePromoted("bad", bad, "BODY")

	resp, err := f.callValidate(SkillValidateRequest{
		CallerAgentID: "@tester",
		Name:          "bad",
	})
	if err != nil {
		t.Fatalf("HandleValidate: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("Results = %d, want 1", len(resp.Results))
	}
	r := resp.Results[0]
	if r.Name != "bad" {
		t.Errorf("Name = %q, want bad", r.Name)
	}
	if r.Status != "invalid" {
		t.Errorf("Status = %q, want invalid", r.Status)
	}
	if len(r.Findings) == 0 {
		t.Fatal("expected findings populated")
	}
	wantPath := "thrum.review.reviewed_by"
	found := false
	for _, fnd := range r.Findings {
		if fnd.Path == wantPath {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("findings missing %q path: %+v", wantPath, r.Findings)
	}
}

func TestValidate_DetectsDuplicateThrumBlock(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	// Simulated git-merge result: two top-level `thrum:` blocks. yaml.v3
	// silently collapses to the second on decode, but ValidateRawFrontmatter
	// walks the MappingNode and catches the duplicate key.
	dupe := "name: dupe\n" +
		"description: dupe skill\n" +
		"thrum:\n" +
		"  proposed_by: '@alice'\n" +
		"  promoted_by: '@coordinator_main'\n" +
		"  created_at: '2026-05-17T18:00:00Z'\n" +
		"  trigger_reason: 'unit test'\n" +
		"  review:\n" +
		"    reviewed_by: '@coordinator_main'\n" +
		"    reviewed_at: '2026-05-17T18:00:00Z'\n" +
		"    check_skill_version: '0.0.0-stub'\n" +
		"thrum:\n" +
		"  proposed_by: '@bob'\n" +
		"  promoted_by: '@coordinator_main'"
	f.writePromoted("dupe", dupe, "BODY")

	resp, err := f.callValidate(SkillValidateRequest{
		CallerAgentID: "@tester",
		Name:          "dupe",
	})
	if err != nil {
		t.Fatalf("HandleValidate: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("Results = %d, want 1", len(resp.Results))
	}
	r := resp.Results[0]
	if r.Status != "duplicate_provenance" {
		t.Errorf("Status = %q, want duplicate_provenance", r.Status)
	}
	found := false
	for _, fnd := range r.Findings {
		if fnd.Kind == "duplicate_field" && fnd.Path == "thrum" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("findings missing duplicate_field/thrum: %+v", r.Findings)
	}
}

func TestValidate_JsonOutputFormat(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	f.writePromoted("alpha", validFrontmatter("alpha"), "BODY")

	resp, err := f.callValidate(SkillValidateRequest{
		CallerAgentID: "@tester",
	})
	if err != nil {
		t.Fatalf("HandleValidate: %v", err)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	// Wire shape contract: results is an array of {name, status, findings}.
	if !strings.Contains(string(out), `"results"`) {
		t.Errorf("JSON missing 'results' key: %s", out)
	}
	if !strings.Contains(string(out), `"name":"alpha"`) {
		t.Errorf("JSON missing alpha entry: %s", out)
	}
	if !strings.Contains(string(out), `"status":"ok"`) {
		t.Errorf("JSON missing status field: %s", out)
	}
}

func TestValidate_CLIExitCode1OnFail(t *testing.T) {
	t.Parallel()
	// The CLI exit-code mapping is pure-function-classifiable: any
	// result with status != "ok" → exit 1; all "ok" → exit 0. The
	// classifier lives in cmd/thrum/skill.go (classifySkillValidate);
	// this test drives it directly without going through cobra.
	cases := []struct {
		name    string
		results []ValidationResult
		want    int
	}{
		{name: "all ok", results: []ValidationResult{{Status: "ok"}, {Status: "ok"}}, want: 0},
		{name: "one invalid", results: []ValidationResult{{Status: "ok"}, {Status: "invalid"}}, want: 1},
		{name: "duplicate", results: []ValidationResult{{Status: "duplicate_provenance"}}, want: 1},
		{name: "empty", results: nil, want: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyValidationResults(c.results)
			if got != c.want {
				t.Errorf("Classify %s = %d, want %d", c.name, got, c.want)
			}
		})
	}
}

// TestValidate_HandlesNoLibrary covers the empty-library case — a
// fresh repo with no .thrum/skills/ should not error; returns empty
// results.
func TestValidate_HandlesNoLibrary(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	// Remove the .thrum/skills/ dir entirely so Library.List would
	// otherwise return ErrLibraryNotInitialized.
	if err := os.RemoveAll(filepath.Join(f.root, ".thrum", "skills")); err != nil {
		t.Fatalf("remove skills dir: %v", err)
	}

	resp, err := f.callValidate(SkillValidateRequest{
		CallerAgentID: "@tester",
	})
	if err != nil {
		t.Fatalf("HandleValidate: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("Results = %d, want 0 for empty library", len(resp.Results))
	}
}
