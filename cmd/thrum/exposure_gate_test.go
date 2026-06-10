package main

import (
	"context"
	"testing"

	"github.com/leonletto/thrum/internal/config"
	thrumSync "github.com/leonletto/thrum/internal/sync"
)

// These tests cover the boot-time a-sync exposure-gate ASSEMBLY
// (resolveBootExposureGate) with an injected prober + save/warn fns — the
// structural substitute for the deferred T11 Step-5 live daemon smoke
// (thrum-44mt review #1). They exercise the 6 boot transitions without a daemon
// or network.

const (
	expRemote = "https://github.com/owner/repo.git"
	expCanon  = "github.com/owner/repo"
)

// cfgWithSync builds a ThrumConfig whose Sync stanza carries the given cached
// fields. localOnly controls Daemon.LocalOnly (relevant only to the nil-Sync
// path, which these tests do not hit — Sync is always non-nil here).
func cfgWithSync(vis, remote, override string) *config.ThrumConfig {
	return &config.ThrumConfig{
		Daemon: config.DaemonConfig{
			Sync: &config.SyncConfig{
				Enabled:                true,
				DetectedVisibility:     vis,
				DetectedRemote:         remote,
				PublicExposureOverride: override,
			},
		},
	}
}

// proberReturning yields a Prober that records call count and returns v.
func proberReturning(v thrumSync.Visibility, calls *int) thrumSync.Prober {
	return func(context.Context, string) thrumSync.Visibility {
		*calls++
		return v
	}
}

// Transition (a): the probe fires only when there is a network remote to probe.
// A local-path origin yields no probe URL, so the prober is never invoked and
// a-sync stays ON (private/allowed). Combined with runDaemon's syncDir-block +
// !localOnly guard, this is why peer/email-only and explicit --local users are
// never probed.
func TestResolveBootExposureGate_LocalPathOriginNeverProbes(t *testing.T) {
	calls := 0
	cfg := cfgWithSync("", "", "")
	out := resolveBootExposureGate(context.Background(), cfg, exposureGateDeps{
		originURL:  "/srv/git/repo.git", // no network host → not probeable
		prober:     proberReturning(thrumSync.VisPublic, &calls),
		saveConfig: func(*config.ThrumConfig) error { return nil },
		warn:       func(string) {},
	})
	if calls != 0 {
		t.Fatalf("prober must not fire for a non-network remote, fired %d times", calls)
	}
	if out.LocalOnly {
		t.Fatalf("local-path remote ⇒ private/allowed, got localOnly: %+v", out)
	}
}

// Transition (b): private remote ⇒ a-sync ON, no warning, visibility persisted.
func TestResolveBootExposureGate_PrivateStaysOn(t *testing.T) {
	calls, saves, warns := 0, 0, 0
	cfg := cfgWithSync("", "", "")
	out := resolveBootExposureGate(context.Background(), cfg, exposureGateDeps{
		originURL:  expRemote,
		prober:     proberReturning(thrumSync.VisPrivate, &calls),
		saveConfig: func(*config.ThrumConfig) error { saves++; return nil },
		warn:       func(string) { warns++ },
	})
	if calls != 1 {
		t.Fatalf("probe must fire once for a network remote, fired %d", calls)
	}
	if out.LocalOnly || out.Reason != "" {
		t.Fatalf("private ⇒ ON, no reason, got %+v", out)
	}
	if warns != 0 {
		t.Fatalf("private ⇒ no warning, got %d", warns)
	}
	if saves != 1 {
		t.Fatalf("detected visibility must be persisted once, saved %d", saves)
	}
	if cfg.Daemon.Sync.DetectedVisibility != string(thrumSync.VisPrivate) {
		t.Fatalf("DetectedVisibility = %q, want private", cfg.Daemon.Sync.DetectedVisibility)
	}
}

// Transition (c): public + no override (prior cache private) ⇒ OFF + reason +
// warning fired with the canonical remote + visibility persisted.
func TestResolveBootExposureGate_PublicFlipsOffAndWarns(t *testing.T) {
	calls := 0
	warnRemote := ""
	cfg := cfgWithSync(string(thrumSync.VisPrivate), expCanon, "")
	out := resolveBootExposureGate(context.Background(), cfg, exposureGateDeps{
		originURL:  expRemote,
		prober:     proberReturning(thrumSync.VisPublic, &calls),
		saveConfig: func(*config.ThrumConfig) error { return nil },
		warn:       func(r string) { warnRemote = r },
	})
	if !out.LocalOnly || out.Reason == "" {
		t.Fatalf("public+no-override ⇒ OFF with reason, got %+v", out)
	}
	if warnRemote != expCanon {
		t.Fatalf("warning must fire with canonical remote %q, got %q", expCanon, warnRemote)
	}
	if cfg.Daemon.Sync.DetectedVisibility != string(thrumSync.VisPublic) {
		t.Fatalf("DetectedVisibility = %q, want public", cfg.Daemon.Sync.DetectedVisibility)
	}
}

// Transition (d): public + matching override ⇒ a-sync ON, no warning.
func TestResolveBootExposureGate_PublicWithMatchingOverrideStaysOn(t *testing.T) {
	calls, warns := 0, 0
	cfg := cfgWithSync("", "", expCanon) // override == canonical remote
	out := resolveBootExposureGate(context.Background(), cfg, exposureGateDeps{
		originURL:  expRemote,
		prober:     proberReturning(thrumSync.VisPublic, &calls),
		saveConfig: func(*config.ThrumConfig) error { return nil },
		warn:       func(string) { warns++ },
	})
	if out.LocalOnly {
		t.Fatalf("public + matching override ⇒ ON, got %+v", out)
	}
	if warns != 0 {
		t.Fatalf("matching override ⇒ no warning, got %d", warns)
	}
}

// Transition (e): undetectable + no cache ⇒ fail-closed OFF, no warning (an
// undetected repo is not a transition INTO the exposed state).
func TestResolveBootExposureGate_UndetectableNoCacheFailsClosed(t *testing.T) {
	calls, warns := 0, 0
	cfg := cfgWithSync("", "", "")
	out := resolveBootExposureGate(context.Background(), cfg, exposureGateDeps{
		originURL:  expRemote,
		prober:     proberReturning(thrumSync.VisUndetectable, &calls),
		saveConfig: func(*config.ThrumConfig) error { return nil },
		warn:       func(string) { warns++ },
	})
	if !out.LocalOnly || out.Reason == "" {
		t.Fatalf("undetectable + no cache ⇒ fail-closed OFF with reason, got %+v", out)
	}
	if warns != 0 {
		t.Fatalf("undetectable ⇒ no warning, got %d", warns)
	}
}

// Transition (f): undetectable + determinate cache (private, same remote) ⇒
// the probe always re-runs, and on an undetectable result the determinate cache
// is trusted (auto-heal) ⇒ a-sync ON.
func TestResolveBootExposureGate_UndetectableTrustsDeterminateCache(t *testing.T) {
	calls := 0
	cfg := cfgWithSync(string(thrumSync.VisPrivate), expCanon, "")
	out := resolveBootExposureGate(context.Background(), cfg, exposureGateDeps{
		originURL:  expRemote,
		prober:     proberReturning(thrumSync.VisUndetectable, &calls),
		saveConfig: func(*config.ThrumConfig) error { return nil },
		warn:       func(string) {},
	})
	if calls != 1 {
		t.Fatalf("gate must always re-probe, fired %d", calls)
	}
	if out.LocalOnly {
		t.Fatalf("undetectable + determinate private cache ⇒ auto-heal ON, got %+v", out)
	}
}
