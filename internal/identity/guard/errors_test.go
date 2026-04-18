package guard

import (
	"errors"
	"strings"
	"testing"
)

func TestError_IncludesAllFields(t *testing.T) {
	e := &Error{
		Guard:         "cross_worktree",
		Reason:        "pid_mismatch",
		CallerPID:     123,
		CallerCWD:     "/a/b",
		ExpectedAgent: "impl_foo",
		ExpectedPID:   456,
		Remediation:   "cd /a/foo and retry",
	}
	s := e.Error()
	for _, want := range []string{
		"cross_worktree",
		"pid_mismatch",
		"impl_foo",
		"123",
		"456",
		"/a/b",
		"cd /a/foo",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %q", want, s)
		}
	}
}

func TestError_ImplementsErrorInterface(t *testing.T) {
	var _ error = &Error{}
	e := &Error{Guard: "g", Reason: "r"}
	var target *Error
	if !errors.As(e, &target) {
		t.Error("errors.As should unwrap to *Error")
	}
}

func TestError_OmitsEmptyOptionalFields(t *testing.T) {
	e := &Error{Guard: "g1a", Reason: "non_git"}
	s := e.Error()
	if strings.Contains(s, "expected pid") {
		t.Errorf("zero ExpectedPID should not render, got %q", s)
	}
	if strings.Contains(s, "caller pid") {
		t.Errorf("zero CallerPID should not render, got %q", s)
	}
}
