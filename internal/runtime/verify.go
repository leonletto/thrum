package runtime

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const defaultVerifyTimeout = 3000 // milliseconds

// verifyBinary checks if a binary exists on PATH and its output matches
// expected substrings. Returns false on any error, timeout, or mismatch.
func verifyBinary(check BinaryCheck) bool {
	binPath, err := exec.LookPath(check.Name)
	if err != nil {
		return false
	}

	timeout := check.Timeout
	if timeout <= 0 {
		timeout = defaultVerifyTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, check.VerifyArgs...) //#nosec G204 -- binPath comes from hardcoded agent registry, not user input
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}

	lower := strings.ToLower(string(output))
	for _, match := range check.MatchAny {
		if strings.Contains(lower, strings.ToLower(match)) {
			return true
		}
	}
	return false
}
