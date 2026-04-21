package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestSnapshotSaveNoPIDHint(t *testing.T) {
	h := SnapshotSaveNoPIDHint("impl_team_fix")

	if h.Code != HintSnapshotSaveNoPID {
		t.Errorf("Code = %q, want %q", h.Code, HintSnapshotSaveNoPID)
	}
	if h.Severity != SeverityWarn {
		t.Errorf("Severity = %q, want warn", h.Severity)
	}
	if !strings.Contains(h.Message, "impl_team_fix") {
		t.Errorf("Message should name the agent: %q", h.Message)
	}
	if len(h.Options) < 2 {
		t.Errorf("expected ≥2 remediation options; got %d", len(h.Options))
	}
	// Remediation must include a re-register command with the agent name baked in.
	found := false
	for _, o := range h.Options {
		if strings.Contains(o.Cmd, "thrum quickstart --name impl_team_fix") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected re-register option referencing agent name; options: %+v", h.Options)
	}
	if h.AllowForce {
		t.Error("snapshot.save.no-pid should be hard refusal; AllowForce must be false")
	}
}

func TestSnapshotSaveNoJSONLHint(t *testing.T) {
	h := SnapshotSaveNoJSONLHint(91614, "/home/leon/.claude")

	if h.Code != HintSnapshotSaveNoJSONL {
		t.Errorf("Code = %q, want %q", h.Code, HintSnapshotSaveNoJSONL)
	}
	if h.Severity != SeverityWarn {
		t.Errorf("Severity = %q, want warn", h.Severity)
	}
	if !strings.Contains(h.Message, "91614") {
		t.Errorf("Message should name the PID: %q", h.Message)
	}
	if !strings.Contains(h.Message, "/home/leon/.claude") {
		t.Errorf("Message should name the claude dir: %q", h.Message)
	}
	// Surface the full options map for label-keyed assertions below.
	byLabel := map[string]Option{}
	for _, o := range h.Options {
		byLabel[o.Label] = o
	}
	// locate: must reference the projects dir so the operator can find
	// their worktree's encoded slug.
	if locate, ok := byLabel["locate"]; !ok {
		t.Error("expected 'locate' option pointing at ~/.claude/projects/")
	} else if !strings.Contains(locate.Cmd, "projects/") {
		t.Errorf("locate.Cmd should reference projects/ dir; got %q", locate.Cmd)
	}
	// override: the --jsonl escape hatch. Must be present so operators
	// have a direct remediation when auto-detect fails — this is the
	// key thrum-ufv5.7 UX fix.
	if override, ok := byLabel["override"]; !ok {
		t.Error("expected 'override' option with --jsonl flag")
	} else if !strings.Contains(override.Cmd, "--jsonl") {
		t.Errorf("override.Cmd should mention --jsonl flag; got %q", override.Cmd)
	}
	// verify-pid: must reference the PID-specific session file.
	if vp, ok := byLabel["verify-pid"]; !ok {
		t.Error("expected 'verify-pid' option")
	} else if !strings.Contains(vp.Cmd, "91614.json") {
		t.Errorf("verify-pid.Cmd should reference sessions/<pid>.json; got %q", vp.Cmd)
	}
	if h.AllowForce {
		t.Error("snapshot.save.no-jsonl should be hard refusal; AllowForce must be false")
	}
}

func TestSnapshotSaveExtractFailedHint(t *testing.T) {
	jsonl := "/home/leon/.claude/projects/-Users-leon-test/sess_abc.jsonl"
	h := SnapshotSaveExtractFailedHint(jsonl)

	if h.Code != HintSnapshotSaveExtractFailed {
		t.Errorf("Code = %q, want %q", h.Code, HintSnapshotSaveExtractFailed)
	}
	if h.Severity != SeverityWarn {
		t.Errorf("Severity = %q, want warn", h.Severity)
	}
	if !strings.Contains(h.Message, jsonl) {
		t.Errorf("Message should name the JSONL path: %q", h.Message)
	}
	// Each diagnostic option should reference the JSONL path so copy/paste works.
	for _, o := range h.Options {
		if !strings.Contains(o.Cmd, jsonl) {
			t.Errorf("option %q Cmd should reference JSONL path for copy/paste; got %q", o.Label, o.Cmd)
		}
	}
	if h.AllowForce {
		t.Error("snapshot.save.extract-failed should be hard refusal; AllowForce must be false")
	}
}

func TestSnapshotHints_RenderTextContainsCodeAndMessage(t *testing.T) {
	// Render through the real text renderer to lock the trailer shape.
	hints := []Hint{
		SnapshotSaveNoPIDHint("impl_x"),
		SnapshotSaveNoJSONLHint(123, "/c"),
		SnapshotSaveExtractFailedHint("/c/sess.jsonl"),
	}
	var buf bytes.Buffer
	RenderText(hints, &buf)
	out := buf.String()

	for _, code := range []string{
		HintSnapshotSaveNoPID,
		HintSnapshotSaveNoJSONL,
		HintSnapshotSaveExtractFailed,
	} {
		if !strings.Contains(out, "["+code+"]") {
			t.Errorf("rendered output missing code marker [%s]; output:\n%s", code, out)
		}
	}
	// Warn severity should always prefix each hint.
	if strings.Count(out, "warn [") < 3 {
		t.Errorf("expected 3 warn-level rendered hints; output:\n%s", out)
	}
}

func TestSnapshotHints_RegisteredInCanonicalList(t *testing.T) {
	// Guard against forgetting to append a new code to AllHintCodes —
	// downstream L3 format/uniqueness tests rely on that slice.
	codes := map[string]bool{}
	for _, c := range AllHintCodes {
		codes[c] = true
	}
	for _, want := range []string{
		HintSnapshotSaveNoPID,
		HintSnapshotSaveNoJSONL,
		HintSnapshotSaveExtractFailed,
	} {
		if !codes[want] {
			t.Errorf("code %q is not in AllHintCodes", want)
		}
	}
}
