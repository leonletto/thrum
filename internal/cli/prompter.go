package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// PromptID identifies a wizard prompt. Tests key canned responses by ID
// so cosmetic label changes don't break tests.
type PromptID int

const (
	PromptAgentName PromptID = iota
	PromptRole
	PromptModule
	PromptWorktreesRoot
	PromptRoleTemplates
	PromptOverwriteRoleTemplate
)

// Prompter abstracts user prompts so wizard tests can inject canned
// responses without a real TTY.
type Prompter interface {
	String(id PromptID, label, defaultValue string) (string, error)
	Choice(id PromptID, label string, options []string, defaultIdx int) (int, error)
	Confirm(id PromptID, label string, defaultYes bool) (bool, error)
}

// ScannerPrompter is the production Prompter, reading line-by-line from
// stdin and writing prompts to the provided writer (typically stderr).
type ScannerPrompter struct {
	scanner *bufio.Scanner
	out     io.Writer
}

func NewScannerPrompter(in io.Reader, out io.Writer) *ScannerPrompter {
	return &ScannerPrompter{scanner: bufio.NewScanner(in), out: out}
}

func (p *ScannerPrompter) String(_ PromptID, label, defaultValue string) (string, error) {
	_, _ = fmt.Fprintf(p.out, "  %s [%s]: ", label, defaultValue)
	if !p.scanner.Scan() {
		if err := p.scanner.Err(); err != nil {
			return "", err
		}
		return defaultValue, nil
	}
	line := strings.TrimSpace(p.scanner.Text())
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

func (p *ScannerPrompter) Choice(_ PromptID, label string, options []string, defaultIdx int) (int, error) {
	_, _ = fmt.Fprintf(p.out, "  %s\n", label)
	for i, opt := range options {
		_, _ = fmt.Fprintf(p.out, "    [%d] %s\n", i+1, opt)
	}
	_, _ = fmt.Fprintf(p.out, "  Choose [%d]: ", defaultIdx+1)
	if !p.scanner.Scan() {
		if err := p.scanner.Err(); err != nil {
			return 0, err
		}
		return defaultIdx, nil
	}
	line := strings.TrimSpace(p.scanner.Text())
	if line == "" {
		return defaultIdx, nil
	}
	var n int
	if _, err := fmt.Sscanf(line, "%d", &n); err != nil || n < 1 || n > len(options) {
		return 0, fmt.Errorf("invalid choice %q; expected 1..%d", line, len(options))
	}
	return n - 1, nil
}

func (p *ScannerPrompter) Confirm(_ PromptID, label string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	_, _ = fmt.Fprintf(p.out, "  %s %s: ", label, suffix)
	if !p.scanner.Scan() {
		return defaultYes, p.scanner.Err()
	}
	line := strings.ToLower(strings.TrimSpace(p.scanner.Text()))
	if line == "" {
		return defaultYes, nil
	}
	return line == "y" || line == "yes", nil
}

// FakePrompter is the test impl. Canned responses are keyed by PromptID;
// missing entries fall back to the default passed at call time.
type FakePrompter struct {
	Strings  map[PromptID]string
	Choices  map[PromptID]int
	Confirms map[PromptID]bool
}

func (p *FakePrompter) String(id PromptID, _, defaultValue string) (string, error) {
	if v, ok := p.Strings[id]; ok {
		return v, nil
	}
	return defaultValue, nil
}

func (p *FakePrompter) Choice(id PromptID, _ string, _ []string, defaultIdx int) (int, error) {
	if v, ok := p.Choices[id]; ok {
		return v, nil
	}
	return defaultIdx, nil
}

func (p *FakePrompter) Confirm(id PromptID, _ string, defaultYes bool) (bool, error) {
	if v, ok := p.Confirms[id]; ok {
		return v, nil
	}
	return defaultYes, nil
}
