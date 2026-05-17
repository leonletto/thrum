package skills

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Sentinel errors. Callers compare with errors.Is to distinguish library
// problems (no .thrum/skills/ yet → suggest `thrum init`) from per-skill
// lookups (missing name).
var (
	// ErrLibraryNotInitialized signals the canonical .thrum/skills/
	// directory is absent. UX path: callers map this to a "run thrum
	// init" hint.
	ErrLibraryNotInitialized = errors.New("skills: library not initialized")

	// ErrSkillNotFound signals a name lookup did not match any
	// promoted skill.
	ErrSkillNotFound = errors.New("skills: not found")
)

// Library is the read-only filesystem walker for the per-project skill
// substrate. No caching in v0.11 (filesystem-as-source-of-truth per
// design-spec §3); E10.2 wraps this for `thrum skill list` / `show`,
// and C-B2 wraps it for the check-the-skill meta-skill.
//
// Callers construct one Library per repo root and reuse it for the
// process lifetime.
type Library struct {
	repoRoot string
}

// NewLibrary returns a Library rooted at repoRoot. The repo root is the
// directory that contains .thrum/ (NOT .thrum itself). Callers pass an
// absolute path; relative paths work for tests but are resolved against
// the caller's CWD at each call.
func NewLibrary(repoRoot string) *Library {
	return &Library{repoRoot: repoRoot}
}

// PendingFilter scopes a ListPending call. Zero-value filter returns all
// pending proposals across every agent's proposed-skills/ directory.
type PendingFilter struct {
	// ProposedBy filters by the frontmatter `thrum.proposed_by` field.
	// Empty string = no filter.
	ProposedBy string
}

// List enumerates promoted skills under .thrum/skills/, sorted by name.
// Returns ErrLibraryNotInitialized when the directory is missing.
// Frontmatter parse errors on individual skills do NOT abort the walk;
// affected entries appear in the result with a zero-value Frontmatter
// (callers that need a validity flag should use ListPending or call
// validator.Validate per-entry once it exists).
func (l *Library) List(ctx context.Context) ([]Skill, error) {
	skillsDir := filepath.Join(l.repoRoot, ".thrum", "skills")
	info, err := os.Stat(skillsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrLibraryNotInitialized, skillsDir)
		}
		return nil, fmt.Errorf("skills: stat library: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrLibraryNotInitialized, skillsDir)
	}

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, fmt.Errorf("skills: read library: %w", err)
	}

	var out []Skill
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !e.IsDir() {
			continue
		}
		skillMd := filepath.Join(skillsDir, e.Name(), "SKILL.md")
		if _, err := os.Stat(skillMd); err != nil {
			// directory without a SKILL.md — skip silently (parallel
			// case to a half-deleted skill or a .gitkeep-only entry).
			continue
		}
		skill, _ := loadSkill(skillMd, e.Name())
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns one promoted skill by directory name, or ErrSkillNotFound.
func (l *Library) Get(ctx context.Context, name string) (*Skill, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	skillMd := filepath.Join(l.repoRoot, ".thrum", "skills", name, "SKILL.md")
	if _, err := os.Stat(skillMd); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrSkillNotFound, name)
		}
		return nil, fmt.Errorf("skills: stat %s: %w", name, err)
	}
	skill, _ := loadSkill(skillMd, name)
	return &skill, nil
}

// ListPending walks every .thrum/agents/<agent>/proposed-skills/<name>/
// SKILL.md path, returning proposed skills filtered by the supplied
// PendingFilter. Missing .thrum/agents/ is a clean "no proposals"
// result, not an error (the agents tree is local-only state and may
// legitimately be absent in a fresh repo).
func (l *Library) ListPending(ctx context.Context, filter PendingFilter) ([]ProposedSkill, error) {
	agentsDir := filepath.Join(l.repoRoot, ".thrum", "agents")
	if _, err := os.Stat(agentsDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("skills: stat agents: %w", err)
	}

	// One glob per author directory; plan calls for filepath.Glob.
	authorDirs, err := filepath.Glob(filepath.Join(agentsDir, "*"))
	if err != nil {
		return nil, fmt.Errorf("skills: glob agents: %w", err)
	}

	var out []ProposedSkill
	for _, authorDir := range authorDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Stat(authorDir)
		if err != nil || !info.IsDir() {
			continue
		}
		proposedDir := filepath.Join(authorDir, "proposed-skills")
		if _, err := os.Stat(proposedDir); err != nil {
			continue
		}
		author := filepath.Base(authorDir)

		skillDirs, err := os.ReadDir(proposedDir)
		if err != nil {
			return nil, fmt.Errorf("skills: read proposed-skills for %s: %w", author, err)
		}
		for _, sd := range skillDirs {
			if !sd.IsDir() {
				continue
			}
			skillMd := filepath.Join(proposedDir, sd.Name(), "SKILL.md")
			if _, err := os.Stat(skillMd); err != nil {
				continue
			}
			proposed := loadProposed(skillMd, sd.Name(), author)
			if filter.ProposedBy != "" && proposed.Frontmatter.Thrum.ProposedBy != filter.ProposedBy {
				continue
			}
			out = append(out, proposed)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Author != out[j].Author {
			return out[i].Author < out[j].Author
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// GetProposed loads a single proposed skill at the supplied path. The
// path can be absolute or relative to the process CWD; the author is
// derived from the path (the parent of "proposed-skills/" is the agent
// directory).
func (l *Library) GetProposed(ctx context.Context, path string) (*ProposedSkill, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrSkillNotFound, path)
		}
		return nil, fmt.Errorf("skills: stat %s: %w", path, err)
	}
	author, name := proposedAuthorAndName(path)
	proposed := loadProposed(path, name, author)
	return &proposed, nil
}

// loadSkill reads a SKILL.md and returns a Skill struct. Frontmatter
// parse errors leave the Frontmatter at zero values; callers that care
// about validity must consult the validator (E11.1) — Library is
// intentionally lenient.
func loadSkill(path, name string) (Skill, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from controlled directory walk
	if err != nil {
		return Skill{Name: name, Path: path}, err
	}
	fm, body, _ := splitFrontmatter(data)
	return Skill{
		Name:        name,
		Path:        path,
		Frontmatter: fm,
		Body:        body,
	}, nil
}

// loadProposed mirrors loadSkill but populates the proposed-skill
// extras (author, FrontmatterValid).
func loadProposed(path, name, author string) ProposedSkill {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from controlled directory walk
	if err != nil {
		return ProposedSkill{
			Skill:  Skill{Name: name, Path: path},
			Author: author,
		}
	}
	fm, body, parseErr := splitFrontmatter(data)
	return ProposedSkill{
		Skill: Skill{
			Name:        name,
			Path:        path,
			Frontmatter: fm,
			Body:        body,
		},
		Author:           author,
		FrontmatterValid: parseErr == nil,
	}
}

// splitFrontmatter parses a SKILL.md file into (frontmatter, body, err).
// Files without a leading "---" YAML preamble return body=full and
// frontmatter at zero. YAML decode errors are returned so callers can
// flag FrontmatterValid=false; body still surfaces in that case so a
// reviewer can read the prose.
//
// The E11.1 task will replace this with the nested+flat compat parser;
// for E8.2 the inline implementation is sufficient.
func splitFrontmatter(data []byte) (Frontmatter, []byte, error) {
	var fm Frontmatter
	if !bytes.HasPrefix(data, []byte("---")) {
		return fm, data, nil
	}
	// Strip the leading delimiter line.
	rest := data[3:]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	}
	// Find the closing delimiter at the start of a line.
	yamlBytes, body, found := bytes.Cut(rest, []byte("\n---"))
	if !found {
		// no closing delimiter — treat as malformed; surface entire
		// remainder as body so the file isn't lost.
		return fm, data, fmt.Errorf("skills: frontmatter has no closing delimiter")
	}
	// Skip the rest of the closing-delimiter line.
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	}
	// Trim any leading blank line so callers don't see a phantom \n.
	body = bytes.TrimLeft(body, "\n")

	if err := yaml.Unmarshal(yamlBytes, &fm); err != nil {
		return Frontmatter{}, body, fmt.Errorf("skills: yaml: %w", err)
	}
	return fm, body, nil
}

// proposedAuthorAndName derives (author, skillName) from a proposed-skill
// path like .thrum/agents/<author>/proposed-skills/<name>/SKILL.md. The
// expectation is that the path includes those directory levels; if the
// path doesn't match the canonical shape we return the deepest two
// directory names as a best-effort fallback.
func proposedAuthorAndName(path string) (author, name string) {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	// Trim trailing SKILL.md if present.
	if n := len(parts); n > 0 && parts[n-1] == "SKILL.md" {
		parts = parts[:n-1]
	}
	if len(parts) >= 1 {
		name = parts[len(parts)-1]
	}
	// Walk back for the proposed-skills marker; author is its parent.
	for i := len(parts) - 1; i >= 1; i-- {
		if parts[i] == "proposed-skills" {
			author = parts[i-1]
			break
		}
	}
	return author, name
}
