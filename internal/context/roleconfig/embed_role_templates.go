// Package roleconfig owns the persistence and embedded-template machinery for
// the role-skills layer: shipped role templates, the per-project role_config
// section of .thrum/config.json, and drift detection helpers.
package roleconfig

import "embed"

//go:embed templates/roles/*.md
var embeddedRoleTemplates embed.FS
