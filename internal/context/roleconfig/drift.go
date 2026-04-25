package roleconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DriftReport summarizes role-config drift detected against shipped templates.
type DriftReport struct {
	Hints []DriftHint
}

// DriftHint is a single advisory surfaced via slog.Warn → installSlogBridge.
type DriftHint struct {
	Code    string
	Message string
}

// Hint code strings — kept here as plain constants to avoid an import cycle
// with internal/cli (which depends on roleconfig). The canonical list lives
// in internal/cli/hintcodes.go (HintRolesConfigMigration etc.) and is checked
// for uniqueness by the L3 hintcodes test.
const (
	HintCodeRolesConfigMigration  = "roles.config.migration"
	HintCodeRolesConfigSchemaBump = "roles.config.schema-bump"
	HintCodeRolesConfigBodyDiff   = "roles.config.body-diff"
)

// DriftStatus inspects role_config + shipped templates and returns the
// drift hints to surface in `thrum prime`.
//
// Precedence (only one hint fires per repo):
//  1. role_templates/ exists ∧ role_config absent → migration
//  2. shipped.schema_version > saved.schema_version → schema bump
//  3. shipped.body_hash ≠ rendered_hash, schema unchanged → body diff
func DriftStatus(thrumDir string) (DriftReport, error) {
	var report DriftReport

	cfg, err := Load(thrumDir)
	if err != nil {
		// A wholly-missing config.json is the same shape as "no role_config
		// block": treat as cfg==nil so the migration check still fires for
		// repos predating config.json creation. Only propagate genuine
		// read/parse failures.
		if errors.Is(err, fs.ErrNotExist) {
			cfg = nil
		} else {
			return report, fmt.Errorf("load role_config: %w", err)
		}
	}

	if cfg == nil {
		// Migration check: any rendered template at all means configure-roles
		// has been run in some prior form — surface a hint to register
		// settings into config.json.
		rtDir := filepath.Join(thrumDir, "role_templates")
		entries, statErr := os.ReadDir(rtDir)
		if statErr == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
					report.Hints = append(report.Hints, DriftHint{
						Code:    HintCodeRolesConfigMigration,
						Message: "Role templates pre-date config tracking. Run /thrum:configure-roles to register settings.",
					})
					return report, nil
				}
			}
		}
		return report, nil
	}

	// Schema bump check.
	for role, settings := range cfg.Roles {
		shippedSchema, _, err := ShippedTemplateInfo(role, settings.Autonomy)
		if err != nil {
			continue
		}
		if shippedSchema > cfg.SchemaVersion {
			report.Hints = append(report.Hints, DriftHint{
				Code:    HintCodeRolesConfigSchemaBump,
				Message: fmt.Sprintf("Role template schema bumped (was %d, now %d). Run /thrum:configure-roles to answer new questions.", cfg.SchemaVersion, shippedSchema),
			})
			return report, nil
		}
	}

	// Body diff check.
	for role, settings := range cfg.Roles {
		_, shippedHash, err := ShippedTemplateInfo(role, settings.Autonomy)
		if err != nil {
			continue
		}
		if settings.RenderedHash != shippedHash {
			report.Hints = append(report.Hints, DriftHint{
				Code:    HintCodeRolesConfigBodyDiff,
				Message: fmt.Sprintf("Role template content updated in plugin (saved v%s). Run `thrum roles refresh` to apply.", cfg.PluginVersion),
			})
			return report, nil
		}
	}

	return report, nil
}
