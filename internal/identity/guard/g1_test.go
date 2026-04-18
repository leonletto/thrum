package guard

import (
	"errors"
	"os"
	"path/filepath"
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
	err := G1a(&QuickstartContext{
		Mode:          ModeOff,
		IdentitiesDir: dir,
		Chain:         []int{100, 200},
		Force:         false,
	})
	if err != nil {
		t.Errorf("off mode should proceed, got %v", err)
	}
}

func TestG1a_WarnMode_Proceeds(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 100, "claude")
	err := G1a(&QuickstartContext{
		Mode:          ModeWarn,
		IdentitiesDir: dir,
		Chain:         []int{100, 200},
		Force:         false,
	})
	if err != nil {
		t.Errorf("warn mode should proceed without error, got %v", err)
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
