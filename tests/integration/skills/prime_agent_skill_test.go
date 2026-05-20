//go:build integration

// Package skills prime_agent_skill validates the structural shape of
// claude-plugin/skills/prime-agent/SKILL.md per spec §7.4 + canonical
// §8.5. The skill body must literally invoke `thrum inbox --unread`
// as Step 1 (NOT a discipline assumption) and run the skill-library
// diff as Step 2 (NOT optional). This test parses the SKILL.md body
// and asserts both invariants — catches drift if a future
// well-intentioned edit softens "Run this exact command" into
// "Consider running" or reorders the steps.
//
// Mirrors are validated by a separate sentinel sweep in
// session_archive_smoke_test.go pattern (cursor + opencode +
// codex paths) — this test focuses on the source-of-truth file.
package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks upward from this test file's location to find the
// nearest go.mod. Skill files live at claude-plugin/skills/... under
// the repo root.
func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found from %s upward", cwd)
		}
		dir = parent
	}
}

// stripFrontmatter returns the body of a markdown file with the
// leading YAML frontmatter block (`---` ... `---`) removed. Skill
// validation tests only care about the body content; the
// frontmatter is asserted separately.
func stripFrontmatter(content string) string {
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return content
	}
	_, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return rest
	}
	return body
}

// readSkill reads the prime-agent SKILL.md from claude-plugin (the
// source of truth; mirrors get validated separately by sync-driven
// sentinel tests).
func readSkill(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	path := filepath.Join(root, "claude-plugin", "skills", "prime-agent", "SKILL.md")
	data, err := os.ReadFile(path) // #nosec G304 -- test fixture path under repo root
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	return string(data)
}

func TestPrimeAgentSkill_FrontmatterPresent(t *testing.T) {
	content := readSkill(t)
	if !strings.HasPrefix(content, "---\n") {
		t.Errorf("SKILL.md must start with YAML frontmatter delimiter; got: %q", content[:min(40, len(content))])
	}

	// Spec §7.4 — description must trigger on "scheduled-agent wake"
	// or "lean prime restart" phrases (per Task 23 acceptance
	// criterion + Anthropic skill-description guidance).
	for _, phrase := range []string{"scheduled-agent wake", "lean prime restart"} {
		if !strings.Contains(content, phrase) {
			t.Errorf("description should contain trigger phrase %q for skill matching", phrase)
		}
	}
}

// TestPrimeAgentSkill_Step1_IsLiteralInboxCall asserts the first
// action verb after the description is `thrum inbox --unread`.
//
// Per spec §7.4: "Step 1 is hard-required `thrum inbox --unread`
// (NOT a discipline-based assumption per brainstorm Q4 + canonical
// §8.5)." A future edit that turns this into "consider running
// `thrum inbox`" or "the agent should typically check the inbox"
// silently breaks the wake-primer dispatch chain — the agent
// would never see the agent.wake message that triggered its
// own session.
//
// Implementation: locate the first code block following the
// description and assert it's exactly `thrum inbox --unread` (with
// the --unread flag — without the flag we'd also surface
// already-read messages and bury the wake-primer).
func TestPrimeAgentSkill_Step1_IsLiteralInboxCall(t *testing.T) {
	body := stripFrontmatter(readSkill(t))

	// Find the first bash code block (```bash ... ```).
	_, afterFence, ok := strings.Cut(body, "```bash\n")
	if !ok {
		t.Fatal("body has no ```bash code blocks — Step 1 must contain a literal shell invocation")
	}
	firstBlock, _, ok := strings.Cut(afterFence, "\n```")
	if !ok {
		t.Fatal("first ```bash block not closed")
	}
	firstBlock = strings.TrimSpace(firstBlock)

	const want = "thrum inbox --unread"
	if firstBlock != want {
		t.Errorf("Step 1 code block must be exactly %q (no extra flags, no comments inline); got: %q",
			want, firstBlock)
	}
}

// TestPrimeAgentSkill_Step2_IsSkillLibraryCheck asserts the second
// code block contains the skill-library diff against the
// last_seen_skills.txt file.
//
// Per canonical §8.5: skill drift between wakes is a real failure
// mode. If a new skill ships to .claude/skills/ between wakes, the
// next wake's lean-prime should surface it. The agent's
// last_seen_skills.txt file tracks the prior skill set; Step 2
// diffs current against last-seen.
//
// Implementation: find the SECOND ```bash code block (Step 2) and
// assert it references both:
//   - `ls .claude/skills/` (current skill set)
//   - `last_seen_skills.txt` (prior set)
//
// The exact shell shape is allowed to vary (operator preference
// on diff vs comm, file paths via env vs literal) but both
// anchors must appear.
func TestPrimeAgentSkill_Step2_IsSkillLibraryCheck(t *testing.T) {
	body := stripFrontmatter(readSkill(t))

	// Skip the first ```bash block (Step 1).
	_, afterFirst, ok := strings.Cut(body, "```bash\n")
	if !ok {
		t.Fatal("body has no ```bash blocks")
	}
	_, afterFirstClose, ok := strings.Cut(afterFirst, "\n```")
	if !ok {
		t.Fatal("first ```bash block not closed")
	}

	// Find the second ```bash block.
	_, afterSecond, ok := strings.Cut(afterFirstClose, "```bash\n")
	if !ok {
		t.Fatal("body has only ONE ```bash block — Step 2 (skill-library check) is missing")
	}
	secondBlock, _, ok := strings.Cut(afterSecond, "\n```")
	if !ok {
		t.Fatal("second ```bash block not closed")
	}

	for _, anchor := range []string{"ls .claude/skills", "last_seen_skills.txt"} {
		if !strings.Contains(secondBlock, anchor) {
			t.Errorf("Step 2 code block must reference %q for the skill-library diff; got:\n%s",
				anchor, secondBlock)
		}
	}
}

// TestPrimeAgentSkill_Step2_WritesLastSeenBaseline asserts the
// Task 27 addition: Step 2 must NOT just diff against
// last_seen_skills.txt — it must also WRITE the current skill
// set back to that file so the NEXT wake's diff has a fresh
// baseline.
//
// Per canonical §8.5 + Task 27: writing the baseline at wake-time
// (rather than end-of-session) means each wake updates its own
// "what existed when I last booted" record. The next wake's diff
// surfaces only what's NEW between wake N's boot and wake N+1's
// boot — clean signal.
//
// Implementation check: the second ```bash block must contain
// BOTH the diff (asserted separately by Step2_IsSkillLibraryCheck)
// AND a write-back invocation. The write surface is intentionally
// flexible (cp / mv from tmp / direct ls > redirect) — we look
// for the destination path as the structural anchor.
func TestPrimeAgentSkill_Step2_WritesLastSeenBaseline(t *testing.T) {
	body := stripFrontmatter(readSkill(t))

	// Skip first ```bash block (Step 1: inbox).
	_, afterFirst, ok := strings.Cut(body, "```bash\n")
	if !ok {
		t.Fatal("no ```bash blocks")
	}
	_, afterFirstClose, ok := strings.Cut(afterFirst, "\n```")
	if !ok {
		t.Fatal("first block not closed")
	}

	// Second ```bash block — Step 2.
	_, afterSecond, ok := strings.Cut(afterFirstClose, "```bash\n")
	if !ok {
		t.Fatal("body has only ONE ```bash block — Step 2 missing")
	}
	secondBlock, _, ok := strings.Cut(afterSecond, "\n```")
	if !ok {
		t.Fatal("second ```bash block not closed")
	}

	// Look for any write-back to LAST_SEEN. The skill's prose uses
	// the LAST_SEEN bash variable rather than a hard-coded path so
	// the AGENT_NAME interpolation is the variable-substituted
	// hook for tests + readers. Acceptable shapes:
	//   cp /tmp/current_skills.txt "${LAST_SEEN}"
	//   mv "${LAST_SEEN}.new" "${LAST_SEEN}"
	//   ls .claude/skills/ > "${LAST_SEEN}"
	// All three contain '${LAST_SEEN}"' on the destination side OR
	// reference the file by its absolute pattern. Check for the
	// envvar pattern as the canonical signal.
	if !strings.Contains(secondBlock, "${LAST_SEEN}") {
		t.Errorf("Step 2 must reference ${LAST_SEEN} for the write-back baseline update")
	}
	// Must have at least TWO references to ${LAST_SEEN}: one for
	// the diff input (read side) and one for the write destination
	// (write side). A single reference means the write was missed.
	count := strings.Count(secondBlock, "${LAST_SEEN}")
	if count < 2 {
		t.Errorf("Step 2 should reference ${LAST_SEEN} at least twice (read + write); found %d", count)
	}
}

// TestPrimeAgentSkill_NoEarlyExit asserts there's no early-exit
// affordance between Step 1 and Step 2. An agent reading the
// skill must not encounter language like "you may stop after
// Step 1 if the inbox is empty" — both steps are load-bearing
// per spec §7.4 + canonical §8.5.
//
// Soft check: looks for early-exit phrasings that have historically
// crept into similar skills. Not exhaustive, but catches the
// common drift patterns. A reviewer should still read the body
// holistically.
func TestPrimeAgentSkill_NoEarlyExit(t *testing.T) {
	body := stripFrontmatter(readSkill(t))

	// Find the section between the first ```bash block (Step 1)
	// and the second ```bash block (Step 2). That's the prose
	// where early-exit drift would land.
	_, afterFirst, ok := strings.Cut(body, "```bash\n")
	if !ok {
		t.Fatal("no ```bash blocks")
	}
	_, afterFirstClose, ok := strings.Cut(afterFirst, "\n```")
	if !ok {
		t.Fatal("first block not closed")
	}
	between, _, ok := strings.Cut(afterFirstClose, "```bash\n")
	if !ok {
		t.Fatal("no second block")
	}
	betweenLower := strings.ToLower(between)

	// Phrases that historically signal early-exit drift in
	// prime/restart skills. If any appear, the skill is at risk
	// of softening Step 2 from "always run" into "run if X".
	earlyExitDriftPhrases := []string{
		"you may stop",
		"skip step 2",
		"step 2 is optional",
		"only if",        // "only if the inbox is empty" pattern
		"otherwise exit", // any "exit before Step 2" pattern
	}
	for _, phrase := range earlyExitDriftPhrases {
		if strings.Contains(betweenLower, phrase) {
			t.Errorf("between-steps prose contains early-exit drift phrase %q — Step 2 must run regardless",
				phrase)
		}
	}
}
