package roleconfig

import (
	"bytes"
)

// RenderEnv carries per-agent customization inputs. Not used by RenderShipped
// today — the refresh artifact is per-role, not per-agent, and per-agent
// tokens (`{{.AgentName}}` etc.) MUST remain literal at refresh time so the
// downstream deploy pass (DeployAll in internal/context/context.go) can
// substitute them per-agent. Kept as a typed parameter so future
// scope-conditioned rendering can plug in without a signature change.
type RenderEnv struct {
	AgentName       string
	Module          string
	WorktreePath    string
	RepoRoot        string
	CoordinatorName string
}

// RenderShipped returns the body of a shipped role-template variant suitable
// for writing to .thrum/role_templates/<role>.md. The body is the shipped
// content with the YAML frontmatter stripped — Go template tokens are
// preserved verbatim so the per-agent deploy pass can substitute them.
//
// scope is accepted for future use (per-scope conditional sections); the
// current shipped templates do not branch on it.
func RenderShipped(role, autonomy, scope string, env RenderEnv) ([]byte, error) {
	_ = scope
	_ = env
	raw, err := ReadShippedTemplate(role, autonomy)
	if err != nil {
		return nil, err
	}
	body := frontmatterRe.ReplaceAll(raw, []byte(""))
	body = bytes.TrimLeft(body, "\n")
	return body, nil
}
