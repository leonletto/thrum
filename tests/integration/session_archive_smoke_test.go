//go:build integration

// Session-archive smoke validates the inject-pipeline shape across
// all runtime plugin mirrors. Per the A2 amend in plan v2
// (thrum-6qmf.15 Task 15):
//
//   - Claude smoke is REQUIRED — the source-of-truth restart skill
//     template must carry the §1 Big-picture mandate that downstream
//     tooling parses.
//   - cursor / codex / opencode smokes are OPTIONAL coverage — they
//     either PASS (template content present + identical to source)
//     or t.Skip cleanly (mirror file missing on filesystem).
//
// In-repo we don't run actual runtime processes (claude / cursor /
// codex / opencode binaries) so the "smoke" here is a content-
// compatibility check across runtime mirror files. The runtime code
// paths are unchanged per Q-Spec-1 (daemon self-invokes Archive);
// what CAN drift is the template content that instructs agents how
// to write the §1 header, which is what this test guards against.
//
// True end-to-end runtime smoke (spinning a Claude/cursor/codex
// session in tmux, running /thrum:restart, validating the next
// session's prime injection) is an operational task; out of scope
// for the in-repo integration suite.
package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/sessionarchive"
)

// expectedBigPictureHeading is the exact string the spec §6A.1
// mandates. Both the parser (sessionarchive.ParseBigPicture) and
// the skill template (claude-plugin/commands/restart.md) reference
// this identical string — drift between them silently breaks the
// §1 discovery hint.
const expectedBigPictureHeading = "## 1. Big picture — what shipped this session"

// expectedSessionArchiveSection is text from the §Session-archive
// block the template must include so agents know about the archive
// after restart. Checked in all mirrors.
const expectedSessionArchiveSection = "After Restart: Session Archive"

// runtimeMirrors lists the restart-skill files across every runtime
// plugin we ship. Source-of-truth first, then the mirrors.
//
//	required=true: bd description / A2 amend says this runtime MUST
//	  pass; test fails if file missing or content drifts.
//	required=false: optional coverage; test t.Skip cleanly if file
//	  missing, fails only if file exists with wrong content.
var runtimeMirrors = []struct {
	runtime  string
	path     string
	required bool
}{
	{"claude (source of truth)", "claude-plugin/commands/restart.md", true},
	{"cursor", "cursor-plugin/commands/restart.md", false},
	{"codex", "codex-plugin/plugins/thrum/skills/thrum-restart/SKILL.md", false},
	{"opencode", "opencode-plugin/assets/commands/thrum-restart.md", false},
}

// repoRoot resolves the repo root from this test file's location.
// Walks upward from cwd until finding a go.mod.
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

// TestSessionArchiveSmoke_RuntimeMirrors_CarryBigPictureMandate is
// the Claude-REQUIRED + cursor/codex/opencode-OPTIONAL smoke per
// A2 amend. Validates that each runtime's restart-skill file
// carries the §Big-picture mandate text and the §Session-archive
// section. Drift in any required runtime → test fails. Drift in
// any optional runtime → test fails (file present, content wrong).
// Missing optional file → t.Skip.
func TestSessionArchiveSmoke_RuntimeMirrors_CarryBigPictureMandate(t *testing.T) {
	root := repoRoot(t)

	for _, m := range runtimeMirrors {
		t.Run(m.runtime, func(t *testing.T) {
			fullPath := filepath.Join(root, m.path)
			data, err := os.ReadFile(fullPath) // #nosec G304 -- test fixture path under repo root
			if err != nil {
				if os.IsNotExist(err) && !m.required {
					t.Skipf("optional runtime mirror %s not present; skipping", fullPath)
					return
				}
				t.Fatalf("read %s: %v", fullPath, err)
			}
			content := string(data)

			// 1. The Big-picture mandate must carry the EXACT heading
			//    text that ParseBigPicture parses. Drift between
			//    template and parser silently breaks the §1 discovery
			//    hint.
			if !strings.Contains(content, expectedBigPictureHeading) {
				t.Errorf("%s missing exact §1 heading %q — template/parser drift",
					fullPath, expectedBigPictureHeading)
			}

			// 2. The §Session-archive informational section must be
			//    present so agents understand the post-restart
			//    behavior (snapshot moves to sessions/, isn't
			//    deleted).
			if !strings.Contains(content, expectedSessionArchiveSection) {
				t.Errorf("%s missing §Session-archive section header %q",
					fullPath, expectedSessionArchiveSection)
			}
		})
	}
}

// TestSessionArchiveSmoke_TemplateOutput_ParsesViaParseBigPicture
// validates the round-trip: the template's instructed heading text
// + body shape MUST parse correctly via ParseBigPicture. A snapshot
// matching the template's recommended structure should yield the
// expected §1 body via the parser. This is the OTHER side of the
// drift guard — covers cases where the heading text alone matches
// but the surrounding structure breaks parsing.
//
// Single-line body: raw and normalized parses are identical here.
// Multi-paragraph raw=true line-break preservation is covered
// separately by TestParseBigPicture_RawTrue_PreservesLineBreaks in
// internal/daemon/sessionarchive/parser_test.go — no duplication
// needed at the smoke layer.
func TestSessionArchiveSmoke_TemplateOutput_ParsesViaParseBigPicture(t *testing.T) {
	// Construct a synthetic snapshot matching the structure the
	// template instructs agents to write. Frontmatter + the §1
	// heading + a representative 1-3 sentence body.
	bodyText := "Locked the session-archive substrate. All 16 tasks closed across 5 E-groupings; CLI surface ships in v0.11."
	snapshot := "---\n" +
		"agent: impl_session_archive\n" +
		"session_id: ses_test\n" +
		"saved_at: 2026-05-17T22:00:00.000Z\n" +
		"reason: manual\n" +
		"machine_id: test-host\n" +
		"---\n\n" +
		expectedBigPictureHeading + "\n\n" +
		bodyText + "\n\n" +
		"## 2. Resume Plan\n\n" +
		"Move on to E5 acceptance sweep.\n"

	// Normalized parse (CLI default mode / discovery hint).
	got := sessionarchive.ParseBigPicture([]byte(snapshot), false)
	if got != bodyText {
		t.Errorf("normalized §1 parse mismatch:\n got:  %q\n want: %q", got, bodyText)
	}

	// Raw parse (CLI --verbose mode). For a single-line body, this
	// returns the same string as the normalized parse above.
	gotRaw := sessionarchive.ParseBigPicture([]byte(snapshot), true)
	if gotRaw != bodyText {
		t.Errorf("raw §1 parse mismatch:\n got:  %q\n want: %q", gotRaw, bodyText)
	}
}

// TestSessionArchiveSmoke_ASCIIDashVariant_AlsoParses covers the
// reviewer-flagged coverage gap (Low #2): the parser accepts both
// the em-dash "—" and the ASCII "--" heading variants. The skill
// template shows only the em-dash in its example, but an agent
// running under a runtime that drops the em-dash to "--" should
// still produce a parseable snapshot.
func TestSessionArchiveSmoke_ASCIIDashVariant_AlsoParses(t *testing.T) {
	const asciiHeading = "## 1. Big picture -- what shipped this session"
	bodyText := "Closed E5 with the ASCII-variant smoke test."
	snapshot := "---\nagent: t\n---\n" + asciiHeading + "\n\n" + bodyText + "\n"

	got := sessionarchive.ParseBigPicture([]byte(snapshot), false)
	if got != bodyText {
		t.Errorf("ASCII-variant §1 parse mismatch:\n got:  %q\n want: %q", got, bodyText)
	}
}

// TestSessionArchiveSmoke_BigPictureSentinels checks that key
// sentinel strings from the §Big-picture mandate appear in every
// runtime mirror. Replaces a strict byte-for-byte equality check
// because sync-skills.sh's adapters intentionally rewrite heading
// levels (codex normalizes ### → ####) and skill-invocation syntax
// (codex `/thrum:foo` → `$thrum-foo`); both transformations would
// false-positive a strict equality assertion. The sentinels below
// are semantically load-bearing — drift in any of them indicates
// the mandate / archive documentation has changed and the mirrors
// need a re-sync.
func TestSessionArchiveSmoke_BigPictureSentinels(t *testing.T) {
	root := repoRoot(t)

	// Sentinel phrases drawn from the spec §6A.1 mandate + the
	// §Session-archive section. Each MUST survive the sync-skills.sh
	// adapter pipeline; drift indicates either the source was
	// edited without re-syncing or an adapter started rewriting
	// content it shouldn't.
	sentinels := []string{
		expectedBigPictureHeading,                    // exact heading parser expects
		"Every restart snapshot MUST begin with",     // mandate framing
		"1-3 sentences",                              // expected length contract
		"becomes YOUR OWN log entry",                 // why-this-matters framing
		"thrum agent sessions list",                  // browse command name
		"`.thrum/agents/<your-agent-id>/sessions/`",  // archive path
		"Permissions are user-only",                  // permission contract
		"ephemeral agents archive into the worktree", // ephemeral caveat
	}

	for _, m := range runtimeMirrors[1:] { // skip source-of-truth itself
		t.Run(m.runtime, func(t *testing.T) {
			fullPath := filepath.Join(root, m.path)
			data, err := os.ReadFile(fullPath) // #nosec G304 -- test fixture path
			if err != nil {
				if os.IsNotExist(err) && !m.required {
					t.Skipf("optional runtime mirror %s not present; skipping", fullPath)
					return
				}
				t.Fatalf("read mirror: %v", err)
			}
			content := string(data)
			for _, sentinel := range sentinels {
				if !strings.Contains(content, sentinel) {
					t.Errorf("%s missing sentinel %q — sync-skills.sh likely stale",
						fullPath, sentinel)
				}
			}
		})
	}
}
