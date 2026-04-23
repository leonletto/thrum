package permission

import (
	"testing"
	"time"
)

func TestNudgeRow_Fields(t *testing.T) {
	now := time.Now().UTC()
	r := NudgeRow{
		MessageID:     "msg_01XYZ",
		Session:       "cursor-test",
		TmuxTarget:    "cursor-test:0.0",
		AgentName:     "researcher_cursor",
		PatternKey:    "cursor.not_in_allowlist",
		ApproveKey:    "y",
		DenyKey:       "Escape",
		FirstDetected: now,
		LastNudgeAt:   now,
		NudgeCount:    1,
		LastPaneHash:  [32]byte{1, 2, 3},
		ExpiresAt:     now.Add(8 * time.Hour),
	}
	if r.MessageID != "msg_01XYZ" {
		t.Errorf("MessageID = %q", r.MessageID)
	}
	if r.Session != "cursor-test" {
		t.Errorf("Session = %q", r.Session)
	}
	if r.TmuxTarget != "cursor-test:0.0" {
		t.Errorf("TmuxTarget = %q", r.TmuxTarget)
	}
	if r.NudgeCount != 1 {
		t.Errorf("NudgeCount = %d", r.NudgeCount)
	}
	if r.LastPaneHash != [32]byte{1, 2, 3} {
		t.Errorf("LastPaneHash mismatch")
	}
	if r.ExpiresAt.Sub(r.FirstDetected) != 8*time.Hour {
		t.Errorf("ExpiresAt should be 8h after FirstDetected")
	}
}
