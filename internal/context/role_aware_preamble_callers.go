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
	//   - runPreambleInit (around line 4014): --init handler. Tries
	//     RenderRoleTemplate first; falls back here when no rendered
	//     template exists for the role.
	//   - applyRolePreamble (around line 7192): quickstart / agent
	//     register path. Same try-RenderRoleTemplate-first pattern.
	"cmd/thrum/main.go",
}
