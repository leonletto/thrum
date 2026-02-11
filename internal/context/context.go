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
