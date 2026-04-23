package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderTextShapeB(t *testing.T) {
	h := Hint{
		Code:     HintTmuxCreateSessionExists,
		Severity: SeverityWarn,
		Message:  "tmux session 'foo' already running",
		Options: []Option{
			{Label: "attach", Cmd: "thrum tmux connect foo"},
			{Label: "replace", Cmd: "thrum tmux create --force", Note: "kills existing session"},
		},
		AllowForce: true,
	}
	var buf bytes.Buffer
	RenderText([]Hint{h}, &buf)
	got := buf.String()
	want := "\n" +
		"  warn [tmux.create.session-exists]: tmux session 'foo' already running\n" +
		"    attach:  thrum tmux connect foo\n" +
		"    replace: thrum tmux create --force    (kills existing session)\n"
	if got != want {
		t.Errorf("RenderText mismatch\ngot:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderTextEmptyInputProducesNothing(t *testing.T) {
	var buf bytes.Buffer
	RenderText(nil, &buf)
	if buf.Len() != 0 {
		t.Errorf("RenderText(nil) wrote %q, want empty", buf.String())
	}
	RenderText([]Hint{}, &buf)
	if buf.Len() != 0 {
		t.Errorf("RenderText([]) wrote %q, want empty", buf.String())
	}
}

func TestRenderTextOrderingWarnBeforeInfo(t *testing.T) {
	info := Hint{Code: "a.b", Severity: SeverityInfo, Message: "i"}
	warn := Hint{Code: "a.c", Severity: SeverityWarn, Message: "w"}
	var buf bytes.Buffer
	RenderText([]Hint{info, warn}, &buf)
	got := buf.String()
	if strings.Index(got, "[a.c]") > strings.Index(got, "[a.b]") {
		t.Errorf("warn must render before info, got:\n%s", got)
	}
}

func TestRenderTextStableWithinSeverity(t *testing.T) {
	h1 := Hint{Code: "a.b", Severity: SeverityInfo, Message: "first"}
	h2 := Hint{Code: "a.c", Severity: SeverityInfo, Message: "second"}
	var buf bytes.Buffer
	RenderText([]Hint{h1, h2}, &buf)
	got := buf.String()
	if strings.Index(got, "[a.b]") > strings.Index(got, "[a.c]") {
		t.Errorf("registration order must be preserved within severity, got:\n%s", got)
	}
}

func TestRenderJSONShapeC(t *testing.T) {
	h := Hint{
		Code:     HintInitNextQuickstart,
		Severity: SeverityInfo,
		Message:  "thrum initialized — register this session as an agent",
		Options: []Option{
			{Label: "register", Cmd: "thrum quickstart ..."},
			{Label: "team", Cmd: "thrum team", Note: "after register, confirm visibility"},
		},
	}
	got := RenderJSON([]Hint{h})
	if got == nil {
		t.Fatal("RenderJSON returned nil for non-empty input")
	}
	if len(got) != 1 {
		t.Fatalf("RenderJSON length = %d, want 1", len(got))
	}
	if got[0]["code"] != HintInitNextQuickstart {
		t.Errorf("code missing or wrong: %+v", got[0])
	}
	if got[0]["severity"] != "info" {
		t.Errorf("severity = %v, want info", got[0]["severity"])
	}
	opts, ok := got[0]["options"].([]map[string]any)
	if !ok {
		t.Fatalf("options wrong type: %T", got[0]["options"])
	}
	if len(opts) != 2 {
		t.Errorf("options length = %d, want 2", len(opts))
	}
	if opts[0]["label"] != "register" || opts[0]["cmd"] != "thrum quickstart ..." {
		t.Errorf("first option wrong: %+v", opts[0])
	}
	// Option with no Note must NOT include the note key.
	if _, hasNote := opts[0]["note"]; hasNote {
		t.Errorf("option with empty Note should omit 'note' key, got: %+v", opts[0])
	}
	if opts[1]["note"] != "after register, confirm visibility" {
		t.Errorf("second option note missing: %+v", opts[1])
	}
}

func TestRenderJSONEmptyInputReturnsNil(t *testing.T) {
	if got := RenderJSON(nil); got != nil {
		t.Errorf("RenderJSON(nil) = %+v, want nil", got)
	}
	if got := RenderJSON([]Hint{}); got != nil {
		t.Errorf("RenderJSON([]) = %+v, want nil", got)
	}
}

func TestEmitSuppressionQuietMode(t *testing.T) {
	var buf bytes.Buffer
	Emit([]Hint{{Code: "x.y", Severity: SeverityInfo, Message: "m"}}, true, false, &buf)
	if buf.Len() != 0 {
		t.Errorf("quiet mode must suppress, got %q", buf.String())
	}
}

func TestEmitSuppressionJSONMode(t *testing.T) {
	var buf bytes.Buffer
	Emit([]Hint{{Code: "x.y", Severity: SeverityInfo, Message: "m"}}, false, true, &buf)
	if buf.Len() != 0 {
		t.Errorf("json mode must suppress stderr rendering, got %q", buf.String())
	}
}

func TestEmitSuppressionNoHintsEnv(t *testing.T) {
	t.Setenv("THRUM_NO_HINTS", "1")
	var buf bytes.Buffer
	Emit([]Hint{{Code: "x.y", Severity: SeverityInfo, Message: "m"}}, false, false, &buf)
	if buf.Len() != 0 {
		t.Errorf("THRUM_NO_HINTS=1 must suppress, got %q", buf.String())
	}
}

func TestRenderJSONForEmitNoHintsEnvSuppresses(t *testing.T) {
	t.Setenv("THRUM_NO_HINTS", "1")
	got := RenderJSONForEmit([]Hint{{Code: "x.y", Severity: SeverityInfo, Message: "m"}})
	if got != nil {
		t.Errorf("THRUM_NO_HINTS must suppress JSON hints, got %+v", got)
	}
}

func TestRenderJSONForEmitDefaultEmits(t *testing.T) {
	got := RenderJSONForEmit([]Hint{{Code: "x.y", Severity: SeverityInfo, Message: "m"}})
	if got == nil {
		t.Error("default (no env) must emit JSON hints")
	}
}

// THRUM_NO_HINTS value handling — "0", "false", empty must NOT suppress.
func TestHintsSuppressedByEnvSemantics(t *testing.T) {
	cases := []struct {
		val      string
		wantSupp bool
	}{
		{"1", true},
		{"true", true},
		{"yes", true}, // anything non-falsy
		{"0", false},
		{"false", false},
		{"False", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.val, func(t *testing.T) {
			t.Setenv("THRUM_NO_HINTS", c.val)
			if got := hintsSuppressedByEnv(); got != c.wantSupp {
				t.Errorf("hintsSuppressedByEnv() with val=%q = %v, want %v", c.val, got, c.wantSupp)
			}
		})
	}
}
