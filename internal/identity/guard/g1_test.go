package guard

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestG1a_NoExistingIdentities_Proceeds(t *testing.T) {
	dir := t.TempDir()
	err := G1a(&QuickstartContext{IdentitiesDir: dir, Chain: []int{100}, Force: false})
	if err != nil {
		t.Errorf("want nil (no existing), got %v", err)
	}
}

func TestG1a_CallerOwnsAnExistingIdentity_Refuses(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 100, "claude")
	err := G1a(&QuickstartContext{IdentitiesDir: dir, Chain: []int{100, 200}, Force: false})
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *Error, got %v", err)
	}
	if gErr.Guard != "quickstart_self_rename" {
		t.Errorf("guard=%q", gErr.Guard)
	}
	if gErr.ExpectedAgent != "impl_foo" {
		t.Errorf("expected_agent=%q", gErr.ExpectedAgent)
	}
}

func TestG1a_Force_RenamesExistingToDeleted(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 100, "claude")
	err := G1a(&QuickstartContext{IdentitiesDir: dir, Chain: []int{100}, Force: true})
	if err != nil {
		t.Errorf("--force should proceed, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "impl_foo.json")); !os.IsNotExist(statErr) {
		t.Error("original should have been renamed away")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "impl_foo.json.deleted")); statErr != nil {
		t.Errorf("expected .deleted sidekick, got %v", statErr)
	}
}

func TestG1a_OffMode_NoOp(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 100, "claude")
	buf := &bytes.Buffer{}
	err := G1a(&QuickstartContext{
		Mode:          ModeOff,
		IdentitiesDir: dir,
		Chain:         []int{100, 200},
		Force:         false,
		WarnLogger:    slog.New(slog.NewJSONHandler(buf, nil)),
	})
	if err != nil {
		t.Errorf("off mode should proceed, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("off mode must not emit slog; got %q", buf.String())
	}
}

func TestG1a_WarnMode_LogsAndProceeds(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 100, "claude")
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, nil))
	err := G1a(&QuickstartContext{
		Mode:          ModeWarn,
		IdentitiesDir: dir,
		Chain:         []int{100, 200},
		Force:         false,
		WarnLogger:    log,
	})
	if err != nil {
		t.Errorf("warn mode should proceed without error, got %v", err)
	}
	if !strings.Contains(buf.String(), "quickstart_self_rename") {
		t.Errorf("warn mode must emit slog with guard name; got %q", buf.String())
	}
}

func TestG1b_NameFree_Proceeds(t *testing.T) {
	dir := t.TempDir()
	err := G1b(&QuickstartContext{
		IdentitiesDir: dir,
		Chain:         []int{100},
		RequestedName: "impl_foo",
		IsPIDAlive:    func(int) bool { return true },
	})
	if err != nil {
		t.Errorf("want nil (empty dir), got %v", err)
	}
}

func TestG1b_NameHeldByLivePID_NotInChain_Refuses(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 999, "claude")
	err := G1b(&QuickstartContext{
		IdentitiesDir: dir,
		Chain:         []int{100},
		RequestedName: "impl_foo",
		IsPIDAlive:    func(p int) bool { return p == 999 },
	})
	if err == nil {
		t.Fatal("want error (live foreign squatter)")
	}
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *Error, got %T", err)
	}
	if gErr.Guard != "quickstart_name_collision" {
		t.Errorf("guard=%q", gErr.Guard)
	}
	if gErr.ExpectedPID != 999 {
		t.Errorf("expected_pid=%d", gErr.ExpectedPID)
	}
}

func TestG1b_NameHeldByDeadPID_Proceeds(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 999, "claude")
	err := G1b(&QuickstartContext{
		IdentitiesDir: dir,
		Chain:         []int{100},
		RequestedName: "impl_foo",
		IsPIDAlive:    func(int) bool { return false },
	})
	if err != nil {
		t.Errorf("dead-PID squat shouldn't block, got %v", err)
	}
}

func TestG1b_NameHeldByCaller_Proceeds(t *testing.T) {
	// Caller owns the target — G1a is the guard that catches this,
	// so G1b must not double-fire.
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 100, "claude")
	err := G1b(&QuickstartContext{
		IdentitiesDir: dir,
		Chain:         []int{100, 200},
		RequestedName: "impl_foo",
		IsPIDAlive:    func(int) bool { return true },
	})
	if err != nil {
		t.Errorf("caller ownership is G1a's job; G1b should pass, got %v", err)
	}
}

func TestG1b_Force_RenamesExistingToDeleted(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 999, "claude")
	err := G1b(&QuickstartContext{
		IdentitiesDir: dir,
		Chain:         []int{100},
		RequestedName: "impl_foo",
		Force:         true,
		IsPIDAlive:    func(int) bool { return true },
	})
	if err != nil {
		t.Errorf("--force should proceed, got %v", err)
	}
	if _, s := os.Stat(filepath.Join(dir, "impl_foo.json")); !os.IsNotExist(s) {
		t.Error("original should be renamed away")
	}
	if _, s := os.Stat(filepath.Join(dir, "impl_foo.json.deleted")); s != nil {
		t.Errorf("expected .deleted sidekick, got %v", s)
	}
}

func TestG1b_OffMode_NoOp(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 999, "claude")
	buf := &bytes.Buffer{}
	err := G1b(&QuickstartContext{
		Mode:          ModeOff,
		IdentitiesDir: dir,
		Chain:         []int{100},
		RequestedName: "impl_foo",
		IsPIDAlive:    func(int) bool { return true },
		WarnLogger:    slog.New(slog.NewJSONHandler(buf, nil)),
	})
	if err != nil {
		t.Errorf("off mode should proceed, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("off mode must not emit slog; got %q", buf.String())
	}
}

func TestG1b_WarnMode_LogsAndProceeds(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 999, "claude")
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, nil))
	err := G1b(&QuickstartContext{
		Mode:          ModeWarn,
		IdentitiesDir: dir,
		Chain:         []int{100},
		RequestedName: "impl_foo",
		IsPIDAlive:    func(int) bool { return true },
		WarnLogger:    log,
	})
	if err != nil {
		t.Errorf("warn mode should proceed, got %v", err)
	}
	if !strings.Contains(buf.String(), "quickstart_name_collision") {
		t.Errorf("warn mode must emit slog with guard name; got %q", buf.String())
	}
}

func TestG1a_CallerDoesNotOwnAnyIdentity_Proceeds(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_other", 9999, "claude") // not in caller's chain
	err := G1a(&QuickstartContext{
		IdentitiesDir: dir,
		Chain:         []int{100, 200},
		Force:         false,
	})
	if err != nil {
		t.Errorf("want nil (chain doesn't match any existing owner), got %v", err)
	}
}
