package guard

import (
	"errors"
	"testing"
)

func TestG4_SubjectAlive_Proceeds(t *testing.T) {
	err := G4(&WriterContext{
		SubjectPID: 100,
		IsPIDAlive: func(int) bool { return true },
	})
	if err != nil {
		t.Errorf("want nil for alive subject, got %v", err)
	}
}

func TestG4_SubjectDead_RefusesWrite(t *testing.T) {
	err := G4(&WriterContext{
		SubjectPID: 100,
		IsPIDAlive: func(int) bool { return false },
	})
	if err == nil {
		t.Fatal("want error for dead subject")
	}
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *Error, got %T", err)
	}
	if gErr.Guard != "daemon_writer_liveness" {
		t.Errorf("guard=%q", gErr.Guard)
	}
	if gErr.ExpectedPID != 100 {
		t.Errorf("expected_pid=%d", gErr.ExpectedPID)
	}
}

func TestG4_CrossDaemonSubject_Exempt(t *testing.T) {
	// Cross-daemon mirror writes: the subject PID is valid on the
	// origin machine, not ours. G4 must not block.
	err := G4(&WriterContext{
		SubjectPID:   100,
		OriginDaemon: "remote-daemon-abc",
		IsPIDAlive:   func(int) bool { return false },
	})
	if err != nil {
		t.Errorf("cross-daemon subject should be exempt, got %v", err)
	}
}

func TestG4_LocalOriginExplicit_Enforces(t *testing.T) {
	// OriginDaemon="local" is semantically identical to "" — both
	// describe a locally-originated write.
	err := G4(&WriterContext{
		SubjectPID:   100,
		OriginDaemon: "local",
		IsPIDAlive:   func(int) bool { return false },
	})
	if err == nil {
		t.Error("want enforcement on explicit 'local' origin")
	}
}

func TestG4_WarnMode_LogsAndProceeds(t *testing.T) {
	err := G4(&WriterContext{
		Mode:       ModeWarn,
		SubjectPID: 100,
		IsPIDAlive: func(int) bool { return false },
	})
	if err != nil {
		t.Errorf("warn mode should proceed, got %v", err)
	}
}

func TestG4_OffMode_NoOp(t *testing.T) {
	err := G4(&WriterContext{
		Mode:       ModeOff,
		SubjectPID: 100,
		IsPIDAlive: func(int) bool { return false },
	})
	if err != nil {
		t.Errorf("off mode should proceed, got %v", err)
	}
}
