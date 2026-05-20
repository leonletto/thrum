package roleconfig

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// CR.7 T7.4 (thrum-6qmf.1.10) — smoke coverage for the Context-restart
// discipline preamble section added in T7.1. Pins two invariants:
//
//  1. The six long-running role templates (coordinator / implementer /
//     researcher × strict / autonomous) carry the section verbatim from
//     brainstorm §Q3.
//  2. Ephemeral roles (tester / reviewer / planner / deployer / monitor /
//     orchestrator / documenter) do NOT carry it. Adding it to them would
//     dilute their preambles with rules they'll never hit.
//
// Existing shipped-template tests already cover parseability and the count
// invariant (20 files); this file adds the section-presence assertions so a
// future refactor that accidentally drops the discipline from a long-running
// role or splashes it across all twenty templates trips a test.

func TestContextRestartDiscipline_PresentInLongRunningRoles(t *testing.T) {
	longRunning := []struct {
		role     string
		autonomy string
	}{
		{"coordinator", "strict"},
		{"coordinator", "autonomous"},
		{"implementer", "strict"},
		{"implementer", "autonomous"},
		{"researcher", "strict"},
		{"researcher", "autonomous"},
	}
	for _, tc := range longRunning {
		name := tc.role + "-" + tc.autonomy
		t.Run(name, func(t *testing.T) {
			raw, err := ReadShippedTemplate(tc.role, tc.autonomy)
			if err != nil {
				t.Fatalf("ReadShippedTemplate: %v", err)
			}
			body := string(raw)
			if !strings.Contains(body, "### Context-restart discipline") {
				t.Errorf("%s template missing '### Context-restart discipline' heading", name)
			}
			// Spot-check three load-bearing phrases from the brainstorm
			// §Q3 verbatim text — catches paraphrasing drift.
			//
			// Whitespace-normalize before substring check so the assertions
			// survive markdown reflow (prettier's fmt-all can shift line-
			// break positions without changing semantic content; the brain-
			// storm §Q3 spec is about the load-bearing phrasing, not exact
			// wrap columns). thrum-mn8s.
			normalized := strings.Join(strings.Fields(body), " ")
			for _, phrase := range []string{
				"warn_threshold` (default 70%)",
				"do NOT dispatch sub-agents",
				"force-restart you at 80% + 3 minutes",
			} {
				if !strings.Contains(normalized, phrase) {
					t.Errorf("%s missing verbatim phrase %q", name, phrase)
				}
			}
		})
	}
}

func TestContextRestartDiscipline_AbsentFromEphemeralRoles(t *testing.T) {
	// Ephemeral roles finish in one session window, so the discipline
	// would be dead weight in their preambles. Some have separate
	// variants and some are single-file; ListShippedTemplates returns
	// the canonical set.
	templates, err := ListShippedTemplates()
	if err != nil {
		t.Fatalf("ListShippedTemplates: %v", err)
	}
	longRunningRoles := map[string]struct{}{
		"coordinator": {},
		"implementer": {},
		"researcher":  {},
	}
	for _, name := range templates {
		// Names look like "coordinator-strict" / "orchestrator".
		role, _, _ := strings.Cut(name, "-")
		if _, ok := longRunningRoles[role]; ok {
			continue // covered by the positive test
		}
		t.Run(name, func(t *testing.T) {
			parts := strings.SplitN(name, "-", 2)
			autonomy := ""
			if len(parts) > 1 {
				autonomy = parts[1]
			}
			raw, err := ReadShippedTemplate(parts[0], autonomy)
			if err != nil {
				t.Fatalf("ReadShippedTemplate(%q,%q): %v", parts[0], autonomy, err)
			}
			if strings.Contains(string(raw), "Context-restart discipline") {
				t.Errorf("ephemeral role template %q unexpectedly contains 'Context-restart discipline'", name)
			}
		})
	}
}

func TestRoleTemplates_ParseAsValidUTF8(t *testing.T) {
	templates, err := ListShippedTemplates()
	if err != nil {
		t.Fatalf("ListShippedTemplates: %v", err)
	}
	for _, name := range templates {
		t.Run(name, func(t *testing.T) {
			parts := strings.SplitN(name, "-", 2)
			autonomy := ""
			if len(parts) > 1 {
				autonomy = parts[1]
			}
			raw, err := ReadShippedTemplate(parts[0], autonomy)
			if err != nil {
				t.Fatalf("ReadShippedTemplate: %v", err)
			}
			if !utf8.Valid(raw) {
				t.Errorf("%s is not valid UTF-8", name)
			}
		})
	}
}
