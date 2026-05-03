package main

import "testing"

func TestShouldUseWizard(t *testing.T) {
	cases := []struct {
		name                               string
		tty, nonInter, exists, force, want bool
	}{
		{"fresh repo on TTY", true, false, false, false, true},
		{"existing repo no force", true, false, true, false, false},
		{"existing repo with force", true, false, true, true, true},
		{"non-TTY", false, false, false, false, false},
		{"non-interactive flag", true, true, false, false, false},
		{"non-interactive overrides force", true, true, true, true, false},
	}
	for _, c := range cases {
		if got := shouldUseWizard(c.tty, c.nonInter, c.exists, c.force); got != c.want {
			t.Errorf("%s: shouldUseWizard(tty=%v, nonInter=%v, exists=%v, force=%v) = %v, want %v",
				c.name, c.tty, c.nonInter, c.exists, c.force, got, c.want)
		}
	}
}
