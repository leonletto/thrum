package context

// RoleAwarePreambleAllowedCallSites documents non-test files where a direct
// call to RoleAwarePreamble is intentional — i.e. the call site is a
// fallback for the case where RenderRoleTemplate(thrumDir, agentName, role)
// returns (nil, nil) (no template available for the role).
//
// Adding a new direct caller without listing it here will fail the audit
// test in role_aware_preamble_audit_test.go. Bypassing the canonical
// RenderRoleTemplate path silently overwrites customized templates and
// drops the user overlay at .thrum/context/<agent>.md.
//
// Paths are relative to the repository root.
var RoleAwarePreambleAllowedCallSites = []string{
	// Two intentional fallback sites:
	//   - runPreambleInit (cmd/thrum/main.go): --init handler. Tries
	//     RenderRoleTemplate first; falls back here when no rendered
	//     template exists for the role.
	//   - applyRolePreamble (cmd/thrum/roles.go): quickstart / agent
	//     register path. Same try-RenderRoleTemplate-first pattern.
	//
	// applyRolePreamble moved from main.go → roles.go in thrum-8kxh Phase 1
	// (T1.11, commit 9946f64a8c). runPreambleInit remains in main.go pending
	// Phase 2's context.go extraction.
	"cmd/thrum/main.go",
	"cmd/thrum/roles.go",
}
