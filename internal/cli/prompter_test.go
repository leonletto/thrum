package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestScannerPrompter_StringReturnsDefaultOnEmptyInput(t *testing.T) {
	in := strings.NewReader("\n")
	out := &bytes.Buffer{}
	p := NewScannerPrompter(in, out)
	got, err := p.String(PromptAgentName, "Agent name", "coord_repo")
	if err != nil {
		t.Fatal(err)
	}
	if got != "coord_repo" {
		t.Errorf("got %q, want default %q", got, "coord_repo")
	}
}

func TestScannerPrompter_StringReturnsTypedValue(t *testing.T) {
	in := strings.NewReader("custom\n")
	out := io.Discard
	p := NewScannerPrompter(in, out)
	got, _ := p.String(PromptAgentName, "Agent name", "default")
	if got != "custom" {
		t.Errorf("got %q, want %q", got, "custom")
	}
}

func TestFakePrompter_ReturnsCannedString(t *testing.T) {
	p := &FakePrompter{Strings: map[PromptID]string{PromptRole: "implementer"}}
	got, err := p.String(PromptRole, "Role", "coordinator")
	if err != nil {
		t.Fatal(err)
	}
	if got != "implementer" {
		t.Errorf("got %q, want %q", got, "implementer")
	}
}

func TestFakePrompter_FallsBackToDefaultWhenUnset(t *testing.T) {
	p := &FakePrompter{}
	got, _ := p.String(PromptRole, "Role", "coordinator")
	if got != "coordinator" {
		t.Errorf("got %q, want default %q", got, "coordinator")
	}
}
