package guard

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestG3_CallerAgentIDPresent_Proceeds(t *testing.T) {
	if err := G3(ModeStrict, "impl_foo", nil); err != nil {
		t.Errorf("present id must pass, got %v", err)
	}
}

func TestG3_CallerAgentIDMissing_Strict_Refuses(t *testing.T) {
	err := G3(ModeStrict, "", nil)
	if err == nil {
		t.Fatal("want error on missing caller id")
	}
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *Error, got %T", err)
	}
	if gErr.Guard != "unauthenticated_rpc" {
		t.Errorf("guard=%q", gErr.Guard)
	}
}

func TestG3_CallerAgentIDMissing_Warn_ProceedsWithLog(t *testing.T) {
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, nil))
	if err := G3(ModeWarn, "", log); err != nil {
		t.Errorf("warn mode must proceed, got %v", err)
	}
	if !strings.Contains(buf.String(), "unauthenticated_rpc") {
		t.Errorf("expected warn log, got %q", buf.String())
	}
}

func TestG3_CallerAgentIDMissing_Off_Proceeds(t *testing.T) {
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, nil))
	if err := G3(ModeOff, "", log); err != nil {
		t.Errorf("off mode must proceed, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("off mode must not log, got %q", buf.String())
	}
}
