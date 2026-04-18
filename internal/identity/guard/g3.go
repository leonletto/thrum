package guard

import "log/slog"

// G3 is the RPC fail-closed helper. Every daemon RPC handler whose
// authorization depends on caller identity must call G3 at the top of
// the request path: when CallerAgentID is absent, we refuse rather
// than falling through to the historical "pick an arbitrary agent"
// behavior that the pre-pxz fallback exhibited. Spec §G3 puts this at
// the core of the design because 448 pre-pxz RPCs hit the fallback
// path today with zero identity verification.
//
// The helper is intentionally parameter-light — it is called from
// dozens of handler sites and we want every call to be trivial: pass
// the configured mode and the CallerAgentID string from the RPC
// frame. In warn mode we emit a structured slog event and let the
// handler proceed with its legacy fallback; in off mode we silently
// bypass. Strict mode returns a *Error that the RPC layer surfaces as
// an authorization failure.
func G3(mode Mode, callerAgentID string, warnLogger *slog.Logger) error {
	if mode == ModeOff {
		return nil
	}
	if callerAgentID != "" {
		return nil
	}
	e := &Error{
		Guard:       "unauthenticated_rpc",
		Reason:      "no_caller_agent_id",
		Remediation: "run 'thrum quickstart' to register an identity; CLI callers must forward CallerAgentID on every RPC",
	}
	if mode == ModeWarn {
		if warnLogger != nil {
			warnLogger.Warn("identity_guard_fire",
				"guard", e.Guard,
				"reason", e.Reason,
			)
		}
		return nil
	}
	return e
}
