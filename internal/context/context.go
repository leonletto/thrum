// Package context provides per-agent context storage for Thrum.
// Context files are markdown files stored in .thrum/context/{agent-name}.md.
// They allow agents to persist volatile project state across sessions.
package context

import (
	"fmt"
	"os"
	"path/filepath"
)

// Save writes context content for the named agent.
// Creates the context directory if it doesn't exist.
func Save(thrumDir, agentName string, content []byte) error {
	dir := filepath.Join(thrumDir, "context")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create context directory: %w", err)
	}

	path := filepath.Join(dir, agentName+".md")
	if err := os.WriteFile(path, content, 0644); err != nil { //nolint:gosec // G306 - markdown files, not secrets
		return fmt.Errorf("write context file: %w", err)
	}

	return nil
}

// Load reads context content for the named agent.
// Returns nil, nil if the context file doesn't exist.
func Load(thrumDir, agentName string) ([]byte, error) {
	path := filepath.Join(thrumDir, "context", agentName+".md")
	data, err := os.ReadFile(path) //nolint:gosec // G304 - path from internal context directory
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read context file: %w", err)
	}
	return data, nil
}

// Clear removes the context file for the named agent.
// Returns nil if the file doesn't exist (idempotent).
func Clear(thrumDir, agentName string) error {
	path := filepath.Join(thrumDir, "context", agentName+".md")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove context file: %w", err)
	}
	return nil
}

// ContextPath returns the absolute path to the context file for the named agent.
func ContextPath(thrumDir, agentName string) string {
	return filepath.Join(thrumDir, "context", agentName+".md")
}

// PreamblePath returns the absolute path to the preamble file for the named agent.
func PreamblePath(thrumDir, agentName string) string {
	return filepath.Join(thrumDir, "context", agentName+"_preamble.md")
}

// LoadPreamble reads the preamble content for the named agent.
// Returns nil, nil if the preamble file doesn't exist.
func LoadPreamble(thrumDir, agentName string) ([]byte, error) {
	path := filepath.Join(thrumDir, "context", agentName+"_preamble.md")
	data, err := os.ReadFile(path) //nolint:gosec // G304 - path from internal context directory
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read preamble file: %w", err)
	}
	return data, nil
}

// SavePreamble writes preamble content for the named agent.
// Creates the context directory if it doesn't exist.
func SavePreamble(thrumDir, agentName string, content []byte) error {
	dir := filepath.Join(thrumDir, "context")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create context directory: %w", err)
	}

	path := filepath.Join(dir, agentName+"_preamble.md")
	if err := os.WriteFile(path, content, 0644); err != nil { //nolint:gosec // G306 - markdown files, not secrets
		return fmt.Errorf("write preamble file: %w", err)
	}

	return nil
}

// DefaultPreamble returns the default preamble template with basic thrum commands.
func DefaultPreamble() []byte {
	return []byte(`## Thrum Quick Reference

**Check messages:** ` + "`thrum inbox --unread`" + `
**Send message:** ` + "`thrum send \"message\" --to @role`" + `
**Reply:** ` + "`thrum reply <MSG_ID> \"response\"`" + `
**Status:** ` + "`thrum status`" + `
**Who's online:** ` + "`thrum agent list --context`" + `
**Save context:** ` + "`thrum context save`" + `
**Wait for messages:** ` + "`thrum wait --all --after -30s --timeout 5m`" + `
`)
}

// EnsurePreamble creates the default preamble file if it doesn't exist.
// No-op if the file already exists.
func EnsurePreamble(thrumDir, agentName string) error {
	path := filepath.Join(thrumDir, "context", agentName+"_preamble.md")
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	return SavePreamble(thrumDir, agentName, DefaultPreamble())
}
