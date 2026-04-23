// internal/daemon/inbox/spool_test.go
package inbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	original := Envelope{
		MsgID:      "msg_01K123",
		From:       "@coordinator",
		ReceivedAt: time.Date(2026, 4, 18, 17, 42, 0, 0, time.UTC),
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Envelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.MsgID != original.MsgID || got.From != original.From || !got.ReceivedAt.Equal(original.ReceivedAt) {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, original)
	}
}

func TestWriteSpoolCreatesFile(t *testing.T) {
	dir := t.TempDir()
	env := Envelope{
		MsgID:      "msg_01K_test",
		From:       "@alice",
		ReceivedAt: time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	}
	if err := WriteSpool(dir, "bob", env); err != nil {
		t.Fatalf("WriteSpool: %v", err)
	}
	expected := filepath.Join(dir, "spool", "bob", "msg_01K_test.json")
	data, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	var got Envelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal written file: %v", err)
	}
	if got.MsgID != env.MsgID {
		t.Fatalf("msg_id mismatch: got %q, want %q", got.MsgID, env.MsgID)
	}
}

func TestWriteSpoolIdempotentOverwrite(t *testing.T) {
	dir := t.TempDir()
	env := Envelope{MsgID: "msg_dup", From: "@alice", ReceivedAt: time.Now()}
	if err := WriteSpool(dir, "bob", env); err != nil {
		t.Fatalf("first write: %v", err)
	}
	env.From = "@alice-updated"
	if err := WriteSpool(dir, "bob", env); err != nil {
		t.Fatalf("second write: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "spool", "bob", "msg_dup.json"))
	var got Envelope
	_ = json.Unmarshal(data, &got)
	if got.From != "@alice-updated" {
		t.Fatalf("second write did not overwrite: got From=%q", got.From)
	}
}

func TestWriteSpoolRejectsEmptyArgs(t *testing.T) {
	env := Envelope{MsgID: "m", From: "@x", ReceivedAt: time.Now()}
	if err := WriteSpool("", "bob", env); err == nil {
		t.Fatal("expected error for empty thrumDir")
	}
	if err := WriteSpool("/tmp", "", env); err == nil {
		t.Fatal("expected error for empty agentID")
	}
	if err := WriteSpool("/tmp", "bob", Envelope{From: "@x"}); err == nil {
		t.Fatal("expected error for empty MsgID")
	}
}
