package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/leonletto/thrum/internal/config"
)

// guardLocalOnlyPairing refuses peer add / peer join when sync is disabled.
//
// The guard is re-keyed to read daemon.sync.enabled (D9/I-3) rather than the
// legacy local_only bool: a node with sync disabled cannot form a meaningful
// peer relationship (the peer would connect but no events would flow), so we
// refuse early with a clear error message.
//
// thrumDir is the path to the .thrum directory (filepath.Join(flagRepo, ".thrum")).
//
// Absent config (no config.json) → allow. This matches the thrum-agents
// guard's non-local default and preserves the "works out of the box" UX.
//
// Ported from thrum-agents:cmd/thrum/peer_localonly_guard.go, predicate
// changed from local_only==true to sync.enabled==false (A4 back-port).
func guardLocalOnlyPairing(thrumDir string) error {
	cfgPath := filepath.Join(thrumDir, "config.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		// No config.json → absent config → allow (non-blocking default)
		return nil
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		// Config exists but can't be loaded → allow (don't block pairing on a
		// config-load error), but WARN so the operator knows the guard did not
		// fire for the intended reason (it could not verify sync state). MINOR-6.
		fmt.Fprintf(os.Stderr, "warning: peer pairing guard could not load %s (%v); proceeding without the sync-enabled check\n",
			filepath.Join(thrumDir, "config.json"), err)
		return nil
	}

	if !cfg.Daemon.Sync.Enabled {
		return errors.New("peer pairing requires sync to be enabled. " +
			"Set daemon.sync.enabled=true in .thrum/config.json, " +
			"then run 'thrum daemon restart' before pairing.")
	}

	return nil
}
