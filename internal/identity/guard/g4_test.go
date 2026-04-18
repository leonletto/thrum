package guard

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
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
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, nil))
	err := G4(&WriterContext{
		Mode:       ModeWarn,
		SubjectPID: 100,
		IsPIDAlive: func(int) bool { return false },
		WarnLogger: log,
	})
	if err != nil {
		t.Errorf("warn mode should proceed, got %v", err)
	}
	if !strings.Contains(buf.String(), "daemon_writer_liveness") {
		t.Errorf("warn mode must emit slog with guard name; got %q", buf.String())
	}
}

func TestG4_OffMode_NoOp(t *testing.T) {
	buf := &bytes.Buffer{}
	err := G4(&WriterContext{
		Mode:       ModeOff,
		SubjectPID: 100,
		IsPIDAlive: func(int) bool { return false },
		WarnLogger: slog.New(slog.NewJSONHandler(buf, nil)),
	})
	if err != nil {
		t.Errorf("off mode should proceed, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("off mode must not emit slog; got %q", buf.String())
	}
}
