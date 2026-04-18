package guard

import "path/filepath"

// ConfigForIdentityDir returns the guard.Config that governs writes
// targeting identity files under idDir, where idDir is the absolute
// path to a `.thrum/identities` directory. The helper climbs two
// levels (`identities` → `.thrum` → repo root) so the config lookup
// anchors on the same repo the identity lives under, not the caller's
// cwd or the daemon's own repo. Shared by the three daemon-side G4
// write sites — tmux.writeTmuxToIdentity, agent.HandleSetAgentStatus,
// permission.setAgentStatus — so the path-derivation style stays
// identical across them.
func ConfigForIdentityDir(idDir string) Config {
	thrumDir := filepath.Dir(idDir)
	return LoadConfig(thrumDir)
}
