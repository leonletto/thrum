package permission

import (
	"testing"
)

func TestNew_FieldsSet(t *testing.T) {
	db := openTestDB(t)
	p := New(nil, db, "supervisor_thrum", "thrum", ".")
	if p == nil {
		t.Fatal("New returned nil")
	}
	if p.supervisorID != "supervisor_thrum" {
		t.Errorf("supervisorID = %q, want supervisor_thrum", p.supervisorID)
	}
	if p.projectName != "thrum" {
		t.Errorf("projectName = %q, want thrum", p.projectName)
	}
	if p.thrumDir != "." {
		t.Errorf("thrumDir = %q, want .", p.thrumDir)
	}
	if p.store == nil {
		t.Error("store not set")
	}
}

func TestNew_NilStateAllowed(t *testing.T) {
	// Tests that do not exercise event-write paths (reply parser,
	// scheduler time logic) should be able to construct a Permission
	// without a State.
	db := openTestDB(t)
	p := New(nil, db, "supervisor_thrum", "thrum", ".")
	if p == nil {
		t.Fatal("New returned nil")
	}
	if p.state != nil {
		t.Error("state should be nil when passed nil")
	}
	if p.store == nil {
		t.Error("store should be initialized even when state is nil")
	}
}
