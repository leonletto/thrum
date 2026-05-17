package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Fixture content for library tests. Defined inline rather than in
// testdata/ because .gitignore globs .thrum/ at every depth — a
// checked-in testdata/library/.thrum/... tree would be untrackable.
const (
	fixtureAlphaMd = `---
name: alpha
description: First promoted skill for library walker tests.
version: 1.0.0
author: "@coordinator_main"
thrum:
  proposed_by: "@researcher_skills"
  promoted_by: "@coordinator_main"
  created_at: 2026-05-15T17:00:00Z
  trigger_reason: fixture
---

# alpha

Body for alpha.
`

	fixtureBetaMd = `---
name: beta
description: Second promoted skill for library walker tests.
version: 0.2.0
thrum:
  proposed_by: "@researcher_skills"
  promoted_by: "@coordinator_main"
  created_at: 2026-05-15T18:00:00Z
  trigger_reason: fixture
---

# beta
`

	fixtureZebraMd = `---
name: zebra
description: Last alphabetically, validates sort order.
version: 0.1.0
thrum:
  proposed_by: "@researcher_skills"
  promoted_by: "@coordinator_main"
  created_at: 2026-05-15T19:00:00Z
  trigger_reason: fixture
---

# zebra
`

	fixtureInvalidYAMLMd = `---
name: invalid-yaml
description: Has a deliberately broken YAML block
version: [this is not a string
thrum:
  this_is_not_valid: : :
---

# invalid-yaml

Body that should still be readable even when frontmatter is bad.
`

	fixtureProposedX1Md = `---
name: x1
description: Pending skill proposed by researcher_x.
thrum:
  proposed_by: "@researcher_x"
  trigger_reason: drafting
---

# x1
`

	fixtureProposedX2Md = `---
name: x2
description: Second pending skill by researcher_x.
thrum:
  proposed_by: "@researcher_x"
  trigger_reason: drafting
---

# x2
`

	fixtureProposedY1Md = `---
name: y1
description: Pending skill proposed by researcher_y.
thrum:
  proposed_by: "@researcher_y"
  trigger_reason: drafting
---

# y1
`

	fixtureProposedCoord1Md = `---
name: coord1
description: Pending skill proposed by coordinator_main.
thrum:
  proposed_by: "@coordinator_main"
  trigger_reason: drafting
---

# coord1
`
)

// writeFixture writes content to <root>/<relPath>, creating parent
// directories as needed. Test-only helper; uses 0o750 on directories to
// satisfy gosec G301.
func writeFixture(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// libraryFixture builds a fully-populated .thrum/skills + .thrum/agents
// tree under a fresh t.TempDir() and returns the repo root path. Cases
// that need a partial fixture call writeFixture directly.
func libraryFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, ".thrum/skills/alpha/SKILL.md", fixtureAlphaMd)
	writeFixture(t, root, ".thrum/skills/beta/SKILL.md", fixtureBetaMd)
	writeFixture(t, root, ".thrum/skills/zebra/SKILL.md", fixtureZebraMd)
	writeFixture(t, root, ".thrum/skills/invalid-yaml/SKILL.md", fixtureInvalidYAMLMd)
	writeFixture(t, root, ".thrum/agents/researcher_x/proposed-skills/x1/SKILL.md", fixtureProposedX1Md)
	writeFixture(t, root, ".thrum/agents/researcher_x/proposed-skills/x2/SKILL.md", fixtureProposedX2Md)
	writeFixture(t, root, ".thrum/agents/researcher_y/proposed-skills/y1/SKILL.md", fixtureProposedY1Md)
	writeFixture(t, root, ".thrum/agents/coordinator_main/proposed-skills/coord1/SKILL.md", fixtureProposedCoord1Md)
	return root
}

func TestLibrary_ListEmpty(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".thrum", "skills"), 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}

	lib := NewLibrary(root)
	skills, err := lib.List(context.Background())
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("List: expected empty slice, got %d skills", len(skills))
	}
}

func TestLibrary_ListMissingDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir() // no .thrum/skills/ created
	lib := NewLibrary(root)
	_, err := lib.List(context.Background())
	if err == nil {
		t.Fatalf("List: expected error, got nil")
	}
	if !errors.Is(err, ErrLibraryNotInitialized) {
		t.Fatalf("List: expected ErrLibraryNotInitialized, got %v", err)
	}
}

func TestLibrary_ListSortedByName(t *testing.T) {
	t.Parallel()

	lib := NewLibrary(libraryFixture(t))
	skills, err := lib.List(context.Background())
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(skills) != 4 {
		t.Fatalf("List: expected 4 skills, got %d: %+v", len(skills), names(skills))
	}
	if !sort.SliceIsSorted(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name }) {
		t.Fatalf("List: not sorted by name: %v", names(skills))
	}
	want := []string{"alpha", "beta", "invalid-yaml", "zebra"}
	for i, w := range want {
		if skills[i].Name != w {
			t.Errorf("skills[%d].Name = %q, want %q", i, skills[i].Name, w)
		}
	}
}

func TestLibrary_ListPendingMultipleAuthors(t *testing.T) {
	t.Parallel()

	lib := NewLibrary(libraryFixture(t))
	pending, err := lib.ListPending(context.Background(), PendingFilter{})
	if err != nil {
		t.Fatalf("ListPending: unexpected error: %v", err)
	}
	if len(pending) != 4 {
		t.Fatalf("ListPending: expected 4, got %d: %+v", len(pending), pendingNames(pending))
	}
	wantAuthors := map[string]int{
		"researcher_x":     2,
		"researcher_y":     1,
		"coordinator_main": 1,
	}
	got := map[string]int{}
	for _, p := range pending {
		got[p.Author]++
	}
	for author, count := range wantAuthors {
		if got[author] != count {
			t.Errorf("author %q: got %d, want %d (full breakdown: %+v)", author, got[author], count, got)
		}
	}
}

func TestLibrary_ListPendingFilteredByAuthor(t *testing.T) {
	t.Parallel()

	lib := NewLibrary(libraryFixture(t))
	pending, err := lib.ListPending(context.Background(), PendingFilter{ProposedBy: "@researcher_x"})
	if err != nil {
		t.Fatalf("ListPending: unexpected error: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("ListPending: expected 2 for researcher_x, got %d: %+v", len(pending), pendingNames(pending))
	}
	for _, p := range pending {
		if p.Author != "researcher_x" {
			t.Errorf("unexpected author %q in filtered result", p.Author)
		}
		if p.Frontmatter.Thrum.ProposedBy != "@researcher_x" {
			t.Errorf("frontmatter proposed_by mismatch: %q", p.Frontmatter.Thrum.ProposedBy)
		}
	}
}

func TestLibrary_GetFound(t *testing.T) {
	t.Parallel()

	lib := NewLibrary(libraryFixture(t))
	skill, err := lib.Get(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if skill.Name != "alpha" {
		t.Fatalf("Get: name mismatch: %q", skill.Name)
	}
	if skill.Frontmatter.Description == "" {
		t.Fatalf("Get: frontmatter not parsed (empty description)")
	}
	if !strings.Contains(string(skill.Body), "Body for alpha") {
		t.Fatalf("Get: body missing fixture marker: %q", skill.Body)
	}
}

func TestLibrary_GetNotFound(t *testing.T) {
	t.Parallel()

	lib := NewLibrary(libraryFixture(t))
	_, err := lib.Get(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("Get: expected ErrSkillNotFound, got %v", err)
	}
}

func TestLibrary_FrontmatterInvalidContinues(t *testing.T) {
	t.Parallel()

	lib := NewLibrary(libraryFixture(t))
	skills, err := lib.List(context.Background())
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}

	var invalid *Skill
	parsed := 0
	for i := range skills {
		switch skills[i].Name {
		case "invalid-yaml":
			invalid = &skills[i]
		case "alpha", "beta", "zebra":
			if skills[i].Frontmatter.Description == "" {
				t.Errorf("%s: frontmatter not parsed", skills[i].Name)
			}
			parsed++
		}
	}
	if invalid == nil {
		t.Fatalf("invalid-yaml skill missing from result")
	}
	if parsed != 3 {
		t.Errorf("expected 3 valid skills parsed, got %d", parsed)
	}
	if invalid.Frontmatter.Description != "" {
		t.Errorf("invalid-yaml: expected zero-value frontmatter, got description %q", invalid.Frontmatter.Description)
	}
}

func TestLibrary_GetProposed(t *testing.T) {
	t.Parallel()

	root := libraryFixture(t)
	lib := NewLibrary(root)
	full := filepath.Join(root, ".thrum", "agents", "researcher_x", "proposed-skills", "x1", "SKILL.md")

	proposed, err := lib.GetProposed(context.Background(), full)
	if err != nil {
		t.Fatalf("GetProposed: unexpected error: %v", err)
	}
	if proposed.Author != "researcher_x" {
		t.Fatalf("Author mismatch: %q", proposed.Author)
	}
	if proposed.Frontmatter.Name != "x1" {
		t.Fatalf("Frontmatter.Name mismatch: %q", proposed.Frontmatter.Name)
	}
	if !proposed.FrontmatterValid {
		t.Fatalf("FrontmatterValid: want true for clean fixture")
	}
}

// Helpers.
func names(s []Skill) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.Name
	}
	return out
}

func pendingNames(p []ProposedSkill) []string {
	out := make([]string, len(p))
	for i, x := range p {
		out[i] = x.Author + "/" + x.Name
	}
	return out
}
