//go:build !unix

package process

import "context"

// IsRunning always returns false on non-Unix platforms.
func IsRunning(_ int) bool { return false }

// IsClaudeProcess always returns false on non-Unix platforms.
func IsClaudeProcess(_ context.Context, _ int) bool { return false }

// IsRuntimeProcess always returns false on non-Unix platforms.
func IsRuntimeProcess(_ context.Context, _ int, _ string) bool { return false }

// FindClaudeAncestor always returns (0, "") on non-Unix platforms.
func FindClaudeAncestor(_ context.Context) (int, string) { return 0, "" }
