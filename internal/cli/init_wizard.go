package cli

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// tmuxGate verifies tmux is on PATH. Returns an error message with
// install suggestions when missing. Called as Step 0 of the wizard
// before any filesystem changes. Successful detection writes a
// confirmation line to out (typically os.Stderr).
func tmuxGate(out io.Writer) error {
	if path, err := exec.LookPath("tmux"); err == nil {
		_, _ = fmt.Fprintf(out, "  tmux: found at %s\n", path)
		return nil
	}
	var preferred string
	switch {
	case has("brew"):
		preferred = "brew install tmux         ← detected on your system"
	case has("port"):
		preferred = "sudo port install tmux    ← detected on your system"
	case has("apt-get"):
		preferred = "apt install tmux          ← detected on your system"
	}
	var b strings.Builder
	b.WriteString("tmux is required but not found on PATH.\n\nInstall with:\n")
	if preferred != "" {
		b.WriteString("  " + preferred + "\n\nOr one of:\n")
	}
	b.WriteString("  brew install tmux         # Homebrew\n")
	b.WriteString("  sudo port install tmux    # MacPorts\n")
	b.WriteString("  apt install tmux          # Debian/Ubuntu\n\n")
	b.WriteString("Then re-run: thrum init")
	return fmt.Errorf("%s", b.String())
}

func has(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}
