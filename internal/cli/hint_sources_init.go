package cli

func init() {
	RegisterHintSource("init", initHints)
}

// initHints emits `init.next-quickstart` (info) post-success when the newly
// initialized repo has no agent identity yet. Encodes R-15 step 2 + the
// implicit init→quickstart sequence: thrum init creates the repo scaffold
// but doesn't register an agent — the operator's next step is quickstart.
//
// Pilot scope: post-action only, full-init path. The worktree-redirect path
// (main.go:262–285) returns before reaching this hint — by design per spec.
func initHints(ctx HintCtx) []Hint {
	if !ctx.Post {
		return nil
	}

	repo, _ := ctx.Flags["repo"].(string)
	if repo == "" {
		return nil
	}
	if ctx.State == nil {
		return nil
	}

	status, _, err := ctx.State.IdentityStatus(repo)
	if err != nil || status != IdentityNone {
		// Error → silent (best-effort). Any existing identity (Live or Stale)
		// means "this machine has already been through quickstart"; don't
		// nag with the register tip.
		return nil
	}

	return []Hint{{
		Code:     HintInitNextQuickstart,
		Severity: SeverityInfo,
		Message:  "thrum initialized — register this session as an agent",
		Options: []Option{
			{Label: "register", Cmd: "thrum quickstart --name <name> --role <role> --module <module> --runtime claude"},
			{Label: "team", Cmd: "thrum team", Note: "after register, confirm visibility"},
		},
	}}
}
