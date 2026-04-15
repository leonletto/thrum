package permission

import (
	"regexp"
	"testing"
)

func TestPattern_Fields(t *testing.T) {
	p := Pattern{
		Name:       "test",
		Regex:      regexp.MustCompile(`test`),
		ApproveKey: "y",
		DenyKey:    "n",
		Comment:    "test pattern",
	}
	if p.Name != "test" {
		t.Errorf("Name = %q", p.Name)
	}
	if p.ApproveKey != "y" {
		t.Errorf("ApproveKey = %q", p.ApproveKey)
	}
	if p.DenyKey != "n" {
		t.Errorf("DenyKey = %q", p.DenyKey)
	}
	if p.Comment != "test pattern" {
		t.Errorf("Comment = %q", p.Comment)
	}
	if p.Regex == nil || !p.Regex.MatchString("test") {
		t.Error("Regex should match 'test'")
	}
}

func TestMatch_UnknownRuntime(t *testing.T) {
	if m := Match("frobnicator", "any content"); m != nil {
		t.Errorf("expected nil for unknown runtime, got %+v", m)
	}
}

func TestMatch_EmptyPatterns(t *testing.T) {
	// Prior to Task 2.2 patterns map is empty; Match returns nil for
	// all known runtimes.
	if m := Match("cursor", "some pane content"); m != nil {
		t.Errorf("expected nil pre-population, got %+v", m)
	}
}
