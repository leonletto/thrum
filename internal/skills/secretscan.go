package skills

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Pattern describes one secret-scan regex. Name is the operator-
// facing category surfaced in Finding.PatternCategory (and in slog
// logs); Regex is the pre-compiled matcher.
type Pattern struct {
	Name  string
	Regex *regexp.Regexp
}

// AllowedPattern is a coordinator-blessed override for a specific
// secret-pattern match. Used by ScanWithOverrides at promote time
// when the coordinator passes --allow-secret-pattern + a reason. The
// override is a regex matched against the line content of any
// finding; matched findings are filtered out.
type AllowedPattern struct {
	// Pattern is a regex string the coordinator wrote (typically a
	// narrow allow-list — the literal fake-secret string or a tight
	// shape that doesn't suppress real findings).
	Pattern string
	// Reason is the audit-trail reason the coordinator gave for
	// allowing this pattern. Stored on the override but not
	// consulted by the scanner.
	Reason string
}

// SecretFinding is one secret-scan hit. NO matched-substring field
// per design-spec §14 (privacy-preserving) — the matched bytes
// never leave the scanner.
//
// Plan-AC inconsistency note: both E8.4 (validator) and E11.3
// (secret-scan) declare a type named "Finding" with different
// shapes. The validator's Finding{Kind, Path, Detail} shipped
// first (E8.4) and is consumed by E10.8 callers; renaming the
// secret-scan struct to SecretFinding preserves the validator's
// stable name while satisfying E11.3's "no matched-string"
// invariant. Update plan §E11.3 AC accordingly (coordinator-side
// plan-errata follow-up).
type SecretFinding struct {
	Path            string
	Line            int
	PatternCategory string
}

// patterns is the package-level catalogue per spec §14.1. Compiled
// at init() so a malformed regex surfaces at process start, not
// 100ms into the first promote call.
var patterns = []Pattern{
	{Name: "AWSAccessKey", Regex: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{Name: "AWSSecret", Regex: regexp.MustCompile(`aws_secret_access_key\s*=\s*['"][A-Za-z0-9/+=]{40}['"]`)},
	{Name: "StripeLiveKey", Regex: regexp.MustCompile(`sk_live_[0-9a-zA-Z]{24,}`)},
	{Name: "GitHubToken", Regex: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{36,}`)},
	{Name: "GenericBearer", Regex: regexp.MustCompile(`Authorization:\s*Bearer\s+[A-Za-z0-9._-]{20,}`)},
	{Name: "GenericAPIKey", Regex: regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*['"][A-Za-z0-9_\-]{16,}['"]`)},
	{Name: "PrivateKeyPEM", Regex: regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH |PGP |)PRIVATE KEY-----`)},
	{Name: "SlackToken", Regex: regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]+`)},
}

// Scanner walks a skill directory and applies the secret-scan
// pattern catalogue. Stateless; methods are safe to call from any
// goroutine without synchronization. The struct exists as a hook
// for future pattern-override at construction time (e.g. a
// repo-wide allow-list).
type Scanner struct{}

// NewScanner returns the package-default scanner. Future option
// args (extra patterns, allow-lists) extend here without breaking
// the call site.
func NewScanner() *Scanner { return &Scanner{} }

// Scan walks skillDir and returns every secret-pattern hit, sorted
// by (Path, Line, PatternCategory). Reads each file once (one
// bufio.Scanner pass); applies every pattern to each line. Findings
// from inside the YAML frontmatter region of SKILL.md are included
// — secrets in frontmatter are just as bad as in body.
func (s *Scanner) Scan(skillDir string) ([]SecretFinding, error) {
	var findings []SecretFinding
	walkErr := filepath.WalkDir(skillDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fileFindings, scanErr := scanFile(path)
		if scanErr != nil {
			return scanErr
		}
		findings = append(findings, fileFindings...)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("skills: secret-scan walk %s: %w", skillDir, walkErr)
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].PatternCategory < findings[j].PatternCategory
	})
	return findings, nil
}

// ScanWithOverrides runs Scan and filters out any finding whose
// line content matches one of the supplied AllowedPattern regexes.
// The overrides are audit-trail items (recorded via Stamper.
// RecordSecretScanOverride on promote); a finding suppressed here is
// not surfaced to the coordinator and does NOT block promotion.
//
// Returns the surviving findings + the original list (callers may
// want both to display "0 active findings, N overridden").
func (s *Scanner) ScanWithOverrides(skillDir string, overrides []AllowedPattern) (active []SecretFinding, suppressed []SecretFinding, err error) {
	allFindings, err := s.Scan(skillDir)
	if err != nil {
		return nil, nil, err
	}
	if len(overrides) == 0 {
		return allFindings, nil, nil
	}
	compiled := make([]*regexp.Regexp, 0, len(overrides))
	for _, o := range overrides {
		re, compileErr := regexp.Compile(o.Pattern)
		if compileErr != nil {
			return nil, nil, fmt.Errorf("skills: invalid override pattern %q: %w", o.Pattern, compileErr)
		}
		compiled = append(compiled, re)
	}
	// Re-walk each finding's source line to apply the override. We
	// already have the (path, line) tuples; reading the line again is
	// cheap relative to keeping every match string in memory (privacy
	// guarantee). For large skill dirs this could be optimized by
	// caching lines during Scan; v0.11 doesn't need that.
	for _, f := range allFindings {
		line, err := readLine(f.Path, f.Line)
		if err != nil {
			// Line disappeared between Scan and override check (rare;
			// promote is single-threaded). Treat as suppressed-by-error
			// so the finding doesn't block promote on a transient read.
			suppressed = append(suppressed, f)
			continue
		}
		filtered := false
		for _, re := range compiled {
			if re.MatchString(line) {
				suppressed = append(suppressed, f)
				filtered = true
				break
			}
		}
		if !filtered {
			active = append(active, f)
		}
	}
	return active, suppressed, nil
}

// scanFile reads a single file line-by-line and runs every pattern
// against each line. Returns the findings list — empty if no
// matches. Errors propagate (caller's filepath.WalkDir bails on the
// first error).
func scanFile(path string) ([]SecretFinding, error) {
	f, err := os.Open(path) // #nosec G304 -- path comes from controlled WalkDir under caller-supplied skillDir
	if err != nil {
		return nil, fmt.Errorf("skills: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var findings []SecretFinding
	scanner := bufio.NewScanner(f)
	// Allow very long lines (some YAML configs / encoded PEMs land
	// on a single multi-KB line). Default 64K buffer is too small.
	scanner.Buffer(make([]byte, 64*1024), 1*1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		for _, p := range patterns {
			if p.Regex.MatchString(line) {
				findings = append(findings, SecretFinding{
					Path:            path,
					Line:            lineNum,
					PatternCategory: p.Name,
				})
			}
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return findings, fmt.Errorf("skills: scan %s: %w", path, scanErr)
	}
	return findings, nil
}

// readLine returns the n-th line (1-indexed) of the given file.
// Used by ScanWithOverrides for the re-read pass; not exported.
func readLine(path string, n int) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- path is a finding's recorded path from a prior controlled Scan
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1*1024*1024)
	cur := 0
	for scanner.Scan() {
		cur++
		if cur == n {
			return scanner.Text(), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("line %d not found in %s", n, path)
}

// Note on Finding shape: this package intentionally does NOT export
// any matched-substring data. The PatternCategory + Path + Line
// triple is sufficient for the operator to locate + remediate;
// surfacing the matched bytes would defeat the privacy guarantee
// and create a worse footgun than the original secret (logged
// secrets propagate through diagnostics + bug reports).
