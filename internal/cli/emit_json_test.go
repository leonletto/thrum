package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"
)

// captureStdout runs fn while os.Stdout is replaced by a pipe, returning
// whatever fn wrote. Helper for EmitJSON tests.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	fn()
	_ = w.Close()
	<-done
	return buf.String()
}

func TestEmitJSON_ObjectBodyNoHints(t *testing.T) {
	ResetPushedHintsForTest()
	body := map[string]any{"agent": "coordinator", "count": 3}
	out := captureStdout(t, func() {
		if err := EmitJSON(body); err != nil {
			t.Fatalf("EmitJSON: %v", err)
		}
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v\noutput:\n%s", err, out)
	}
	if got["agent"] != "coordinator" {
		t.Errorf("agent=%v want coordinator", got["agent"])
	}
	if _, has := got["hints"]; has {
		t.Errorf("hints key present when no hints emitted")
	}
}

func TestEmitJSON_ObjectBodyWithPushedHints(t *testing.T) {
	ResetPushedHintsForTest()
	pushedHints = []Hint{{Code: "worktree.x", Severity: SeverityWarn, Message: "m"}}
	body := map[string]any{"agent": "x"}
	out := captureStdout(t, func() {
		if err := EmitJSON(body); err != nil {
			t.Fatalf("EmitJSON: %v", err)
		}
	})
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	hints, ok := got["hints"].([]any)
	if !ok {
		t.Fatalf("hints key missing or wrong shape in:\n%s", out)
	}
	if len(hints) != 1 {
		t.Errorf("hints len=%d want 1", len(hints))
	}
	first, _ := hints[0].(map[string]any)
	if first["code"] != "worktree.x" {
		t.Errorf("code=%v want worktree.x", first["code"])
	}
}

func TestEmitJSON_ArrayBodyWithPushedHints(t *testing.T) {
	ResetPushedHintsForTest()
	pushedHints = []Hint{{Code: "x", Severity: SeverityWarn, Message: "m"}}
	body := []string{"a", "b"}
	out := captureStdout(t, func() {
		_ = EmitJSON(body)
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("array+hints should wrap into object; got:\n%s", out)
	}
	res, ok := got["result"].([]any)
	if !ok || len(res) != 2 {
		t.Errorf("result key missing or wrong: %v", got["result"])
	}
}

func TestEmitJSON_ArrayBodyNoHintsVerbatim(t *testing.T) {
	ResetPushedHintsForTest()
	body := []string{"a", "b"}
	out := captureStdout(t, func() {
		_ = EmitJSON(body)
	})
	var arr []string
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("array without hints should stay array; got:\n%s", out)
	}
	if len(arr) != 2 {
		t.Errorf("len=%d want 2", len(arr))
	}
}

func TestEmitJSONWithHints_MergesCollectedAndPushed(t *testing.T) {
	ResetPushedHintsForTest()
	pushedHints = []Hint{{Code: "pushed.x", Severity: SeverityWarn, Message: "pm"}}
	collected := []Hint{{Code: "collected.y", Severity: SeverityInfo, Message: "cm"}}
	body := map[string]any{"ok": true}
	out := captureStdout(t, func() {
		_ = EmitJSONWithHints(body, collected)
	})
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	hints, _ := got["hints"].([]any)
	if len(hints) != 2 {
		t.Fatalf("hints len=%d want 2\n%s", len(hints), out)
	}
	codes := []string{
		hints[0].(map[string]any)["code"].(string),
		hints[1].(map[string]any)["code"].(string),
	}
	if codes[0] != "collected.y" || codes[1] != "pushed.x" {
		t.Errorf("order wrong: %v (want collected first)", codes)
	}
}
