//go:build !unix

package process

// IsRunning always returns false on non-Unix platforms.
func IsRunning(_ int) bool { return false }

// IsClaudeProcess always returns false on non-Unix platforms.
func IsClaudeProcess(_ int) bool { return false }

// FindClaudeAncestor always returns 0 on non-Unix platforms.
func FindClaudeAncestor() int { return 0 }
