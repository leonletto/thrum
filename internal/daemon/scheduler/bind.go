package scheduler

import (
	"context"
	"encoding/json"
)

// RPCHandler is the daemon's JSON-RPC handler signature. Matches
// internal/daemon.Handler exactly so the daemon can register these via
// a one-line cast loop without importing scheduler-internal types into
// the daemon's wire-registration site.
type RPCHandler func(ctx context.Context, params json.RawMessage) (any, error)

// Methods returns a map of JSON-RPC method names → per-method handler
// closures, one per scheduler RPC method (10 total). Daemon-startup
// wiring iterates this map and registers each handler against the
// existing internal/daemon.Server.RegisterHandler API:
//
//	for method, handler := range scheduler.Methods(sched) {
//	    server.RegisterHandler(method, daemon.Handler(handler))
//	}
//
// The substrate doesn't import internal/daemon (that would create a
// cycle); the closure shape is the wire-side handler signature so the
// cast at the registration site is trivial.
//
// Auth model per Leon's Q-Spec-4 answer (option a): same-identity-as-
// caller. The daemon's existing per-RPC auth gate applies to these
// handlers exactly as it does to message.send.
func Methods(s *Scheduler) map[string]RPCHandler {
	return map[string]RPCHandler{
		"job.list": func(ctx context.Context, params json.RawMessage) (any, error) {
			var req ListJobsRequest
			if len(params) > 0 {
				if err := json.Unmarshal(params, &req); err != nil {
					return nil, err
				}
			}
			return s.RPC_JobList(ctx, req)
		},
		"job.show": func(ctx context.Context, params json.RawMessage) (any, error) {
			var req ShowJobRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			return s.RPC_JobShow(ctx, req)
		},
		"job.create": func(ctx context.Context, params json.RawMessage) (any, error) {
			var req CreateJobRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			return s.RPC_JobCreate(ctx, req)
		},
		"job.update": func(ctx context.Context, params json.RawMessage) (any, error) {
			var req UpdateJobRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			return s.RPC_JobUpdate(ctx, req)
		},
		"job.delete": func(ctx context.Context, params json.RawMessage) (any, error) {
			var req DeleteJobRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			return s.RPC_JobDelete(ctx, req)
		},
		"job.enable": func(ctx context.Context, params json.RawMessage) (any, error) {
			var req EnableDisableRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			return s.RPC_JobEnable(ctx, req)
		},
		"job.disable": func(ctx context.Context, params json.RawMessage) (any, error) {
			var req EnableDisableRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			return s.RPC_JobDisable(ctx, req)
		},
		"job.cancel": func(ctx context.Context, params json.RawMessage) (any, error) {
			var req CancelJobRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			return s.RPC_JobCancel(ctx, req)
		},
		"job.history": func(ctx context.Context, params json.RawMessage) (any, error) {
			var req JobHistoryRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			return s.RPC_JobHistory(ctx, req)
		},
		"job.done": func(ctx context.Context, params json.RawMessage) (any, error) {
			var req JobDoneRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			return s.RPC_JobDone(ctx, req)
		},
	}
}
