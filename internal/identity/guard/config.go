// Package guard enforces thrum's identity ownership rules.
// See dev-docs/specs/2026-04-17-thrum-identity-guard-design.md.
package guard

// Mode is the enforcement level for a single guard.
type Mode string

const (
	ModeStrict Mode = "strict"
	ModeWarn   Mode = "warn"
	ModeOff    Mode = "off"
)

// Config is the per-guard enforcement matrix loaded from
// .thrum/config.json's identity_guard block. Defaults to strict for
// every guard on first ship; operators may downgrade individual guards
// to warn or off to debug incidents or opt out of enforcement they
// cannot yet support.
type Config struct {
	CrossWorktree           Mode `json:"cross_worktree"`
	DeadPIDAutoReclaim      Mode `json:"dead_pid_auto_reclaim"`
	QuickstartSelfRename    Mode `json:"quickstart_self_rename"`
	QuickstartNameCollision Mode `json:"quickstart_name_collision"`
	NonGitBootstrap         Mode `json:"non_git_bootstrap"`
	UnauthenticatedRPC      Mode `json:"unauthenticated_rpc"`
	DaemonWriterLiveness    Mode `json:"daemon_writer_liveness"`
	PrimeOwnership          Mode `json:"prime_ownership"`
}

// DefaultConfig returns the ship-default: every guard strict.
func DefaultConfig() Config {
	return Config{
		CrossWorktree:           ModeStrict,
		DeadPIDAutoReclaim:      ModeStrict,
		QuickstartSelfRename:    ModeStrict,
		QuickstartNameCollision: ModeStrict,
		NonGitBootstrap:         ModeStrict,
		UnauthenticatedRPC:      ModeStrict,
		DaemonWriterLiveness:    ModeStrict,
		PrimeOwnership:          ModeStrict,
	}
}
