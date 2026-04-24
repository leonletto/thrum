package cli

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

//go:embed templates/claude-md/thrum-block.md
var claudeMDTemplate []byte

// Markers delimit the Thrum-managed block inside CLAUDE.md. They are part of
// the embedded template itself; the constants below are just the sentinels
// the setup logic uses to locate an existing block.
const (
	claudeMDBeginMarker = "<!-- BEGIN THRUM -->"
	claudeMDEndMarker   = "<!-- END THRUM -->"
)

// ErrClaudeMDBlockExists is returned by SetupClaudeMD when --apply is used
// and the file already contains a Thrum block — the caller must pass
// --force to replace it.
var ErrClaudeMDBlockExists = errors.New("thrum block already present in CLAUDE.md — use --force to replace")

// SetupClaudeMDMode describes what SetupClaudeMD did. Useful for the CLI
// layer to emit a precise human-readable summary.
type SetupClaudeMDMode string

const (
	ModePrinted  SetupClaudeMDMode = "printed"
	ModeCreated  SetupClaudeMDMode = "created"
	ModeAppended SetupClaudeMDMode = "appended"
	ModeReplaced SetupClaudeMDMode = "replaced"
)

// SetupClaudeMDOptions controls SetupClaudeMD behavior.
type SetupClaudeMDOptions struct {
	Dir   string    // directory containing CLAUDE.md; empty means os.Getwd()
	Apply bool      // false → print to Out; true → write CLAUDE.md
	Force bool      // only meaningful with Apply=true; replaces an existing block
	Out   io.Writer // default os.Stdout; used only when Apply=false
}

// SetupClaudeMDResult summarizes the outcome.
type SetupClaudeMDResult struct {
	Mode SetupClaudeMDMode
	Path string // absolute path to CLAUDE.md (empty when Mode=printed)
}

// SetupClaudeMD installs or prints the Thrum-managed block for CLAUDE.md.
// See SetupClaudeMDOptions for the mode matrix. Idempotent with --force: two
// successive --apply --force invocations produce byte-identical file content.
func SetupClaudeMD(opts SetupClaudeMDOptions) (SetupClaudeMDResult, error) {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}

	if !opts.Apply {
		if _, err := out.Write(claudeMDTemplate); err != nil {
			return SetupClaudeMDResult{}, fmt.Errorf("write template to stdout: %w", err)
		}
		return SetupClaudeMDResult{Mode: ModePrinted}, nil
	}

	dir := opts.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return SetupClaudeMDResult{}, fmt.Errorf("get cwd: %w", err)
		}
		dir = cwd
	}
	path := filepath.Join(dir, "CLAUDE.md")

	existing, err := os.ReadFile(path) // #nosec G304 -- path is <dir>/CLAUDE.md; dir is caller-provided or cwd
	switch {
	case errors.Is(err, os.ErrNotExist):
		if werr := os.WriteFile(path, claudeMDTemplate, 0600); werr != nil {
			return SetupClaudeMDResult{}, fmt.Errorf("write %s: %w", path, werr)
		}
		return SetupClaudeMDResult{Mode: ModeCreated, Path: path}, nil
	case err != nil:
		return SetupClaudeMDResult{}, fmt.Errorf("read %s: %w", path, err)
	}

	hasBlock := bytes.Contains(existing, []byte(claudeMDBeginMarker)) &&
		bytes.Contains(existing, []byte(claudeMDEndMarker))

	if hasBlock && !opts.Force {
		return SetupClaudeMDResult{Path: path}, ErrClaudeMDBlockExists
	}

	var (
		newContent []byte
		mode       SetupClaudeMDMode
	)
	if hasBlock {
		newContent = replaceClaudeMDBlock(existing, claudeMDTemplate)
		mode = ModeReplaced
	} else {
		newContent = appendClaudeMDBlock(existing, claudeMDTemplate)
		mode = ModeAppended
	}

	if werr := os.WriteFile(path, newContent, 0600); werr != nil {
		return SetupClaudeMDResult{}, fmt.Errorf("write %s: %w", path, werr)
	}
	return SetupClaudeMDResult{Mode: mode, Path: path}, nil
}

// replaceClaudeMDBlock replaces the first Thrum block in content (span from
// BEGIN marker through END marker's trailing newline) with template.
// Expects both markers to exist; caller checks via hasBlock.
func replaceClaudeMDBlock(content, template []byte) []byte {
	beginStr := []byte(claudeMDBeginMarker)
	endStr := []byte(claudeMDEndMarker)

	beginIdx := bytes.Index(content, beginStr)
	endIdxRel := bytes.Index(content[beginIdx:], endStr)
	endEndIdx := beginIdx + endIdxRel + len(endStr)
	// Absorb the newline that terminates the END marker line, if any — the
	// template carries its own trailing \n, so leaving the old one in would
	// double it and break idempotency.
	if endEndIdx < len(content) && content[endEndIdx] == '\n' {
		endEndIdx++
	}

	result := make([]byte, 0, len(content)+len(template))
	result = append(result, content[:beginIdx]...)
	result = append(result, template...)
	result = append(result, content[endEndIdx:]...)
	return result
}

// appendClaudeMDBlock appends template to content with a single blank-line
// separator. Ensures content ends with a newline before the separator so the
// output remains idempotent under subsequent --apply --force runs.
func appendClaudeMDBlock(content, template []byte) []byte {
	if len(content) == 0 {
		// Treat empty file same as missing: just the template.
		return append([]byte{}, template...)
	}
	result := make([]byte, 0, len(content)+len(template)+2)
	result = append(result, content...)
	if !bytes.HasSuffix(content, []byte("\n")) {
		result = append(result, '\n')
	}
	result = append(result, '\n') // blank line separator
	result = append(result, template...)
	return result
}
