package cli

import (
	"log/slog"

	"github.com/leonletto/thrum/internal/context/roleconfig"
)

// EmitRolesConfigDriftHints surfaces every hint in the drift report via
// slog.Warn. installSlogBridge (cmd/thrum/main.go) routes those records into
// either the JSON output's "hints" array (--json mode) or stderr at
// LevelWarn (human mode). The hint code is derived from the first
// whitespace-delimited token of the message (see sloghint.go::deriveHintCode).
//
// Using a helper rather than inlining the slog calls at the prime call-site
// keeps each emitted code referenced by a hint_sources_*.go file, which the
// L3 hintcodes catalog tests grep for to detect orphaned codes.
//
// Code reference list — kept exhaustive on purpose so the L3 grep finds them:
//   - HintRolesConfigMigration
//   - HintRolesConfigSchemaBump
//   - HintRolesConfigBodyDiff
func EmitRolesConfigDriftHints(report roleconfig.DriftReport) {
	for _, hint := range report.Hints {
		slog.Warn(hint.Code + " " + hint.Message)
	}
}
