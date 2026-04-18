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

// Merge overlays repo over base and daemon over both. An empty Mode
// ("") in a layer means "not set — defer to the lower layer." Callers
// typically invoke as Merge(DefaultConfig(), repo, daemon) so an absent
// .thrum/config.json field falls through to the strict default.
func Merge(base, repo, daemon Config) Config {
	out := base
	overlay := func(dst *Mode, src Mode) {
		if src != "" {
			*dst = src
		}
	}
	overlay(&out.CrossWorktree, repo.CrossWorktree)
	overlay(&out.DeadPIDAutoReclaim, repo.DeadPIDAutoReclaim)
	overlay(&out.QuickstartSelfRename, repo.QuickstartSelfRename)
	overlay(&out.QuickstartNameCollision, repo.QuickstartNameCollision)
	overlay(&out.NonGitBootstrap, repo.NonGitBootstrap)
	overlay(&out.UnauthenticatedRPC, repo.UnauthenticatedRPC)
	overlay(&out.DaemonWriterLiveness, repo.DaemonWriterLiveness)
	overlay(&out.PrimeOwnership, repo.PrimeOwnership)

	overlay(&out.CrossWorktree, daemon.CrossWorktree)
	overlay(&out.DeadPIDAutoReclaim, daemon.DeadPIDAutoReclaim)
	overlay(&out.QuickstartSelfRename, daemon.QuickstartSelfRename)
	overlay(&out.QuickstartNameCollision, daemon.QuickstartNameCollision)
	overlay(&out.NonGitBootstrap, daemon.NonGitBootstrap)
	overlay(&out.UnauthenticatedRPC, daemon.UnauthenticatedRPC)
	overlay(&out.DaemonWriterLiveness, daemon.DaemonWriterLiveness)
	overlay(&out.PrimeOwnership, daemon.PrimeOwnership)
	return out
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
