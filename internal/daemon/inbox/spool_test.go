// internal/daemon/inbox/spool_test.go
package inbox

import (
	"encoding/json"
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
