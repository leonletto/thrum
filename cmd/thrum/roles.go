package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/context/roleconfig"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:8574-8592
// Destination: roles.go:23-41
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func rolesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "roles",
		Short: "Manage role-based preamble templates",
		Long: `Manage role-based preamble templates in .thrum/role_templates/.

Role templates are Go text/template files that automatically generate
agent preambles during registration. Templates are rendered with agent
identity data (AgentName, Role, Module, WorktreePath, RepoRoot, CoordinatorName).`,
	}

	cmd.AddCommand(rolesListCmd())
	cmd.AddCommand(rolesDeployCmd())
	cmd.AddCommand(rolesRefreshCmd())
	cmd.AddCommand(rolesSaveConfigCmd())
	cmd.AddCommand(rolesTemplatesCmd())

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:8598-8611
// Destination: roles.go:53-66
// Tests: cmd/thrum/roles_save_config_test.go; cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// rolesSaveConfigCmd is the CLI shim used by /thrum:configure-roles to
// persist the user's answers. Reads JSON-on-stdin matching RoleConfig,
// backfills schema/version/timestamp/rendered_hash defaults, and atomically
// writes role_config to .thrum/config.json.
func rolesSaveConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "save-config",
		Short: "Write role_config to .thrum/config.json from JSON on stdin",
		Long: `Internal subcommand used by /thrum:configure-roles to persist answers.
Reads JSON from stdin, validates against the RoleConfig schema, fills
rendered_hash from current shipped templates, and atomically writes to
.thrum/config.json (preserving other top-level keys byte-identical).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			thrumDir := filepath.Join(flagRepo, ".thrum")
			return runRolesSaveConfig(thrumDir, os.Stdin)
		},
	}
}

// ORIGIN[thrum-8kxh]: moved from main.go:8617-8646
// Destination: roles.go:78-107
// Tests: cmd/thrum/roles_save_config_test.go
// Commit: 9946f64a8c
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// runRolesSaveConfig is the testable body of `thrum roles save-config`.
// Decodes RoleConfig from in, fills defaults for absent scalar fields,
// backfills rendered_hash from current shipped templates per role, and
// delegates to roleconfig.Save for atomic write + unknown-key preservation.
func runRolesSaveConfig(thrumDir string, in io.Reader) error {
	var cfg roleconfig.RoleConfig
	dec := json.NewDecoder(in)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return fmt.Errorf("decode role_config: %w", err)
	}
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = roleconfig.CurrentSchemaVersion
	}
	if cfg.PluginVersion == "" {
		cfg.PluginVersion = Version
	}
	if cfg.ConfiguredAt.IsZero() {
		cfg.ConfiguredAt = time.Now().UTC()
	}

	for role, settings := range cfg.Roles {
		if _, hash, err := roleconfig.ShippedTemplateInfo(role, settings.Autonomy); err == nil {
			settings.RenderedHash = hash
			cfg.Roles[role] = settings
		}
	}

	if err := roleconfig.Save(thrumDir, &cfg); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	fmt.Printf("Saved role_config (%d roles).\n", len(cfg.Roles))
	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:8652-8659
// Destination: roles.go:119-126
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// rolesTemplatesCmd groups inspection subcommands for embedded shipped
// templates. The configure-roles skill uses `print` to read shipped content
// over CLI rather than from a raw filesystem path (binary may run from any
// directory).
func rolesTemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "templates",
		Short: "Inspect shipped role templates",
	}
	cmd.AddCommand(rolesTemplatesPrintCmd())
	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:8661-8681
// Destination: roles.go:134-154
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func rolesTemplatesPrintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "print <role>-<autonomy>",
		Short: "Print the embedded shipped role template (with frontmatter)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, autonomy, ok := strings.Cut(args[0], "-")
			if !ok {
				// Single-variant role (e.g. orchestrator) — pass autonomy
				// empty so ReadShippedTemplate falls back to <role>.md.
				role, autonomy = args[0], ""
			}
			raw, err := roleconfig.ReadShippedTemplate(role, autonomy)
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(raw)
			return err
		},
	}
}

// ORIGIN[thrum-8kxh]: moved from main.go:8688-8704
// Destination: roles.go:167-183
// Tests: cmd/thrum/roles_refresh_test.go; cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// rolesRefreshCmd regenerates rendered .thrum/role_templates/<role>.md files
// from saved role_config answers + current shipped templates. Used after a
// plugin upgrade to apply new template content. Per-agent tokens
// (`{{.AgentName}}` etc.) are kept literal so the existing per-agent deploy
// pass can substitute them.
func rolesRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Regenerate .thrum/role_templates/<role>.md from saved answers",
		Long: `Regenerate rendered role templates from saved role_config answers plus
current shipped templates. Used after a plugin upgrade to apply new template
content. Fails loud if role_config is absent — run /thrum:configure-roles
first to capture answers.

Examples:
  thrum roles refresh`,
		RunE: func(cmd *cobra.Command, args []string) error {
			thrumDir := filepath.Join(flagRepo, ".thrum")
			return runRolesRefresh(thrumDir)
		},
	}
}

// ORIGIN[thrum-8kxh]: moved from main.go:8711-8757
// Destination: roles.go:196-242
// Tests: cmd/thrum/roles_refresh_test.go
// Commit: 9946f64a8c
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// runRolesRefresh is the testable body of `thrum roles refresh`. Fails loud
// when role_config is absent (no fallback — user is told to run
// configure-roles). On success, writes rendered templates and updates each
// role's rendered_hash to the current shipped body_hash, then atomically
// rewrites .thrum/config.json with the bumped plugin_version.
func runRolesRefresh(thrumDir string) error {
	cfg, err := roleconfig.Load(thrumDir)
	if err != nil {
		return fmt.Errorf("load role_config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("no role_config found in .thrum/config.json — run /thrum:configure-roles first")
	}

	rtDir := filepath.Join(thrumDir, "role_templates")
	if err := os.MkdirAll(rtDir, 0o750); err != nil {
		return fmt.Errorf("create role_templates dir: %w", err)
	}

	refreshed := 0
	for role, settings := range cfg.Roles {
		// Defense in depth: role keys come from .thrum/config.json, which is
		// internal-controlled, but a role string of "../evil" would resolve
		// to .thrum/evil.md when joined with rtDir. The embedded FS already
		// rejects such paths inside RenderShipped, but match the explicit
		// guard pattern used by runPreambleInit / worktreeCreateCmd.
		if strings.ContainsAny(role, "/\\") || strings.Contains(role, "..") {
			return fmt.Errorf("invalid role name %q in role_config: must not contain /, \\, or parent references", role)
		}
		body, err := roleconfig.RenderShipped(role, settings.Autonomy, settings.Scope, roleconfig.RenderEnv{})
		if err != nil {
			return fmt.Errorf("render %s/%s: %w", role, settings.Autonomy, err)
		}
		outPath := filepath.Join(rtDir, role+".md")
		if err := os.WriteFile(outPath, body, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		if _, hash, hashErr := roleconfig.ShippedTemplateInfo(role, settings.Autonomy); hashErr == nil {
			settings.RenderedHash = hash
			cfg.Roles[role] = settings
		}
		refreshed++
	}

	cfg.PluginVersion = Version
	if err := roleconfig.Save(thrumDir, cfg); err != nil {
		return fmt.Errorf("save updated role_config: %w", err)
	}

	fmt.Printf("Refreshed %d role templates from plugin v%s.\n", refreshed, Version)
	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:8759-8793
// Destination: roles.go:250-284
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func rolesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show configured templates and matching agents",
		Long: `List all role templates in .thrum/role_templates/ and show which
registered agents match each template.

Examples:
  thrum roles list`,
		RunE: func(cmd *cobra.Command, args []string) error {
			thrumDir := filepath.Join(flagRepo, ".thrum")

			templates, err := agentcontext.ListRoleTemplates(thrumDir)
			if err != nil {
				return fmt.Errorf("list role templates: %w", err)
			}

			if len(templates) == 0 {
				fmt.Println("No role templates found in .thrum/role_templates/")
				fmt.Println("  Create templates manually or use: /thrum:configure-roles")
				return nil
			}

			for name, agents := range templates {
				if len(agents) == 0 {
					fmt.Printf("%s    (0 agents)\n", name)
				} else {
					fmt.Printf("%s    (%d agents: %s)\n", name, len(agents), strings.Join(agents, ", "))
				}
			}

			return nil
		},
	}
}

// ORIGIN[thrum-8kxh]: moved from main.go:8795-8852
// Destination: roles.go:292-349
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func rolesDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Re-render preambles for registered agents from role templates",
		Long: `Re-render preambles for all registered agents that have matching
role templates. Templates in .thrum/role_templates/ are rendered with
each agent's identity data and written to .thrum/context/{agent}_preamble.md.

This is a full overwrite — templates are the source of truth.

Examples:
  thrum roles deploy              # Deploy for all agents
  thrum roles deploy --agent foo  # Deploy for a specific agent
  thrum roles deploy --dry-run    # Preview what would change`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentFilter, _ := cmd.Flags().GetString("agent")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			thrumDir := filepath.Join(flagRepo, ".thrum")

			result, err := agentcontext.DeployAll(thrumDir, agentFilter, dryRun)
			if err != nil {
				return fmt.Errorf("deploy role templates: %w", err)
			}

			if dryRun {
				fmt.Println("Dry run — no files written")
			}

			totalProcessed := len(result.Updated) + len(result.Skipped)
			if totalProcessed == 0 {
				fmt.Println("No agents found")
				return nil
			}

			if len(result.Updated) > 0 {
				verb := "Updated"
				if dryRun {
					verb = "Would update"
				}
				fmt.Printf("%s %d/%d agents", verb, len(result.Updated), totalProcessed)
				if len(result.Skipped) > 0 {
					fmt.Printf(" (no template for: %s)", strings.Join(result.Skipped, ", "))
				}
				fmt.Println()
			} else {
				fmt.Printf("No matching templates for %d agents\n", totalProcessed)
			}

			return nil
		},
	}

	cmd.Flags().String("agent", "", "Deploy for a specific agent only")
	cmd.Flags().Bool("dry-run", false, "Preview changes without writing files")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:8856-8899
// Destination: roles.go:359-402
// Tests: cmd/thrum/main_test.go (indirect via Execute()); cross-phase caller from Phase 2's quickstartCmd (sync_cmd.go)
// Commit: 9946f64a8c
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// applyRolePreamble applies the preamble for an agent using the priority:
// preambleFile > role template > default. Called from both quickstart and agent register.
func applyRolePreamble(thrumDir, agentName, role, preambleFile string, force bool) error {
	if preambleFile != "" {
		// --preamble-file takes precedence over everything
		customContent, err := os.ReadFile(preambleFile) // #nosec G304 -- preambleFile is user-specified via --preamble-file CLI flag; this is a CLI tool, user controls the path
		if err != nil {
			return fmt.Errorf("failed to read preamble file %q: %w", preambleFile, err)
		}
		composed := append(agentcontext.DefaultPreamble(), []byte("\n---\n\n")...)
		composed = append(composed, customContent...)
		if err := agentcontext.SavePreamble(thrumDir, agentName, composed); err != nil {
			return fmt.Errorf("failed to save composed preamble: %w", err)
		}
		return nil
	}

	rendered, renderErr := agentcontext.RenderRoleTemplate(thrumDir, agentName, role)
	if renderErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to render role template for %q: %v (using default)\n", role, renderErr) // #nosec G705 -- stderr diagnostic, not web output
	} else if rendered != nil {
		// Role template found — use it as the preamble
		if err := agentcontext.SavePreamble(thrumDir, agentName, rendered); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save role template preamble: %v\n", err)
		}
		return nil
	}

	// Fall back to role-aware default preamble
	preamble := agentcontext.RoleAwarePreamble(role)
	if force {
		// Force mode: always overwrite with current default
		if err := agentcontext.SavePreamble(thrumDir, agentName, preamble); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save preamble: %v\n", err)
		}
	} else {
		// Only write if no preamble exists yet
		path := agentcontext.PreamblePath(thrumDir, agentName)
		if _, err := os.Stat(path); os.IsNotExist(err) { // #nosec G703 -- path from PreamblePath, not user input
			if err := agentcontext.SavePreamble(thrumDir, agentName, preamble); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create preamble: %v\n", err)
			}
		}
	}
	return nil
}
