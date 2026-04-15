package permission

// DetectPaneState is the top-level entry point used by the CLI
// `thrum tmux check-pane` command. It consults the per-runtime
// pattern library.
//
// Return value encodes the detection result for the tmux.check-pane
// RPC:
//
//   - ""                               → no prompt detected (idle path)
//   - "permission:<runtime>.<name>"    → pattern matched; daemon can
//     look up the pattern via
//     Match() for nudge formatting.
//
// Unknown runtime (empty or not in the library) also returns empty,
// preserving the current "idle" behavior for agents that haven't had
// their runtime populated in the identity file yet.
func DetectPaneState(runtime, paneContent string) string {
	if runtime == "" || paneContent == "" {
		return ""
	}
	m := Match(runtime, paneContent)
	if m == nil {
		return ""
	}
	return "permission:" + runtime + "." + m.Name
}
