package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ThrumConfig represents the top-level .thrum/config.json file.
type ThrumConfig struct {
	Runtime       RuntimeConfig       `json:"runtime"`
	Identity      IdentityConfig      `json:"identity,omitempty"`
	Daemon        DaemonConfig        `json:"daemon"`
	Backup        BackupConfig        `json:"backup"`
	Telegram      TelegramConfig      `json:"telegram"`
	Email         EmailConfig          `json:"email,omitzero"` // omitzero: Go 1.24+; entire block dropped when at zero value
	Users         map[string]UserPrefs `json:"users,omitempty"`
	Peers         PeersConfig         `json:"peers"`
	Restart       RestartConfig       `json:"restart"`
	Worktrees     WorktreesConfig     `json:"worktrees,omitempty"`
	Orchestration OrchestrationConfig `json:"orchestration,omitempty"`

	// IdentityGuard is the per-guard enforcement matrix. RawMessage to
	// avoid an import cycle; internal/identity/guard parses it at load.
	// See dev-docs/specs/2026-04-17-thrum-identity-guard-design.md.
	IdentityGuard *json.RawMessage `json:"identity_guard,omitempty"`

	// PermissionSupervisors lists recipients of permission-prompt nudges.
	// Each entry is one of:
	//   - a role name ("coordinator", "orchestrator") → broadcasts to
	//     every active agent with that role
	//   - a specific agent name ("@coordinator_main") → direct delivery
	//   - a specific user ("@user:leon-letto") → direct delivery, auto-
	//     forwarded to Telegram if the bridge is configured for that user
	// Default when absent or empty: ["coordinator"] (applied at
	// nudge-dispatch time, not at load).
	PermissionSupervisors []string `json:"permission_supervisors,omitempty"`

	// ProjectName is the short human-readable identifier used to form
	// the supervisor sender identity (@supervisor_<ProjectName>). Falls
	// back to filepath.Base(repo_root) at daemon boot if empty.
	ProjectName string `json:"project_name,omitempty"`

	// Jobs holds operator-authored periodic job specs per canonical §4.1.
	// Stored as raw JSON so internal/config stays independent of the
	// scheduler package — the scheduler's ReloadConfig decodes each entry
	// into its JobSpec at boot/reload. See A-B1 plan §5947-5950.
	Jobs map[string]json.RawMessage `json:"jobs,omitempty"`
}

// IdentityConfig holds the daemon's per-repo identity.
// Daemon_id is generated once at thrum init (or first daemon start of an
// un-initialized repo) and persists forever. Other fields are refreshed on
// each daemon start from current runtime values — they are informational
// metadata, not keys. Git_origin_url is set once at init.
type IdentityConfig struct {
	DaemonID     string `json:"daemon_id,omitempty"`
	RepoName     string `json:"repo_name,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	RepoPath     string `json:"repo_path,omitempty"`
	GitOriginURL string `json:"git_origin_url,omitempty"`
	InitAt       string `json:"init_at,omitempty"`
}

// WorktreesConfig holds worktree management settings.
type WorktreesConfig struct {
	BasePath     string `json:"base_path,omitempty"`
	BeadsEnabled bool   `json:"beads_enabled"`
	ThrumEnabled bool   `json:"thrum_enabled"`
}

// OrchestrationConfig holds orchestrator role settings.
type OrchestrationConfig struct {
	MergeTarget     string `json:"merge_target,omitempty"`
	DefaultAutonomy string `json:"default_autonomy,omitempty"`
}

// TelegramConfig holds Telegram bridge settings.
// The bridge is disabled when Token is empty.
type TelegramConfig struct {
	Token     string          `json:"token,omitempty"`      // BotFather token (e.g., "123456789:AAH...")
	Target    string          `json:"target,omitempty"`     // Target agent mention (e.g., "@coordinator_main")
	UserID    string          `json:"user_id,omitempty"`    // Thrum username (e.g., "leon-letto")
	ChatID    int64           `json:"chat_id,omitempty"`    // Telegram chat ID for outbound messages
	Enabled   *bool           `json:"enabled,omitempty"`    // Explicit enable/disable; nil = enabled if token set
	AllowFrom []int64         `json:"allow_from,omitempty"` // Allowed Telegram user IDs; empty = block all
	AllowAll  bool            `json:"allow_all,omitempty"`  // If true, allow all users (overrides AllowFrom)
	Groups    []TelegramGroup `json:"groups,omitempty"`     // Group bridge configs
}

// TelegramGroup holds per-group bridge settings.
type TelegramGroup struct {
	ChatID       int64         `json:"chat_id"`
	Name         string        `json:"name"`
	TrustedBots  []int64       `json:"trusted_bots,omitempty"`
	RemoteAgents []RemoteAgent `json:"remote_agents,omitempty"`
}

// RemoteAgent describes an agent in a remote repo reachable via the group bridge.
type RemoteAgent struct {
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
	Bot    string `json:"bot"`
}

// TelegramEnabled returns whether the bridge should run.
func (t TelegramConfig) TelegramEnabled() bool {
	if t.Token == "" {
		return false
	}
	if t.Enabled != nil {
		return *t.Enabled
	}
	return true
}

// IsAllowed returns whether a Telegram user ID is permitted to send messages.
// Empty AllowFrom with AllowAll=false blocks all (fail-closed).
func (t TelegramConfig) IsAllowed(userID int64) bool {
	if t.AllowAll {
		return true
	}
	return slices.Contains(t.AllowFrom, userID)
}

// FindGroup returns the TelegramGroup for the given chatID, or nil if not found.
func (t TelegramConfig) FindGroup(chatID int64) *TelegramGroup {
	for i := range t.Groups {
		if t.Groups[i].ChatID == chatID {
			return &t.Groups[i]
		}
	}
	return nil
}

// IsTrustedBot returns true if botUserID is listed as trusted in the given group.
func (t TelegramConfig) IsTrustedBot(chatID int64, botUserID int64) bool {
	g := t.FindGroup(chatID)
	if g == nil {
		return false
	}
	return slices.Contains(g.TrustedBots, botUserID)
}

// GroupNames returns the names of all configured groups in order.
func (t TelegramConfig) GroupNames() []string {
	names := make([]string, len(t.Groups))
	for i, g := range t.Groups {
		names[i] = g.Name
	}
	return names
}

// MaskedToken returns the token masked to the first 10 characters.
// Used for display/logging — never log the full token.
func (t TelegramConfig) MaskedToken() string {
	if len(t.Token) <= 10 {
		return t.Token
	}
	return t.Token[:10]
}

// BoolPtr is a helper to create a pointer to a bool.
func BoolPtr(v bool) *bool { return &v }

// RuntimeConfig holds runtime selection preferences.
type RuntimeConfig struct {
	Primary string `json:"primary,omitempty"` // "claude", "auggie", "cursor", etc.
}

// DaemonConfig holds daemon-specific settings.
type DaemonConfig struct {
	LocalOnly       bool   `json:"local_only,omitempty"`
	SyncInterval    int    `json:"sync_interval,omitempty"` // seconds; 0 means use default (60)
	WSPort          string `json:"ws_port,omitempty"`       // "auto" or specific port number
	PeerPort        string `json:"peer_port,omitempty"`     // "auto" or specific port number for peer connections
	SingleAgentMode bool   `json:"single_agent_mode,omitempty"`
	LogLevel        string `json:"log_level,omitempty"` // "debug", "info", "warn", "error"; default "info"

	// Scheduler holds A-B1 scheduler-primitive settings consumed by the
	// daemon-side wiring (canonical §4.4).
	Scheduler DaemonSchedulerConfig `json:"scheduler"`

	// AgentLifecycle holds B-B1 agent-lifecycle housekeeping settings
	// consumed by the internal.agent_lifecycle_cleanup daily handler.
	AgentLifecycle DaemonAgentLifecycleConfig `json:"agent_lifecycle"`

	// A-B4 substrate config blocks (v0.11; canonical §4.4).

	// StalledSweep tunes internal.stalled_agent_sweep cadence
	// (stability knob). Separate from Reminders.DispatchIntervalSeconds
	// (UX-precision knob) per @architect_substrate decision 2026-05-15;
	// coupling them would defeat Leon-brainstorm-Q3.3's minute-
	// resolution Xm/Xh/Xd reminder contract.
	StalledSweep StalledSweepConfig `json:"stalled_sweep,omitempty"`

	// Reminders tunes internal.reminder_dispatch cadence
	// (UX-precision knob). Must stay finer-grained than
	// StalledSweep.IntervalMinutes to honor user-set minute-
	// resolution reminders.
	Reminders RemindersConfig `json:"reminders,omitempty"`

	// Sweep configures the daemon-source target_chain for stalled-
	// agent reminders. Empty AlertChain → fall back to
	// [@Escalation.SupervisorAgentName] (canonical §4.4 single-
	// supervisor; the brainstorm cycle-2 #3 two-element fallback
	// was reduced to single-supervisor in the §4.4 amendment).
	Sweep SweepChainConfig `json:"sweep,omitempty"`

	// Escalation names the supervisor agent reminders escalate to
	// when SweepChainConfig.AlertChain is unset (canonical §4.4).
	Escalation EscalationConfig `json:"escalation,omitempty"`
}

// StalledSweepConfig tunes the stalled-agent sweep cadence
// (canonical §4.4; A-B4 Q3.7).
type StalledSweepConfig struct {
	// IntervalMinutes is how often the sweep runs. Zero is the
	// "use default" sentinel (15 minutes via
	// DaemonConfig.StalledSweepIntervalMinutes).
	IntervalMinutes int `json:"interval_minutes,omitempty"`
}

// RemindersConfig tunes the reminder-dispatcher cadence
// (canonical §4.4; A-B4 plan E4.1 Task 10).
type RemindersConfig struct {
	// DispatchIntervalSeconds is how often the dispatcher scans
	// for due reminders. Zero is the "use default" sentinel
	// (30 seconds via DaemonConfig.RemindersDispatchIntervalSeconds).
	DispatchIntervalSeconds int `json:"dispatch_interval_seconds,omitempty"`
}

// SweepChainConfig overrides the default target_chain for
// daemon-source reminders (canonical §4.4; A-B4 cycle-2 #3).
type SweepChainConfig struct {
	// AlertChain is the explicit delivery chain
	// (e.g. ["@coordinator_main", "leon@example.com"]). When empty,
	// sweep falls back to [@Escalation.SupervisorAgentName].
	AlertChain []string `json:"alert_chain,omitempty"`
}

// EscalationConfig names the supervisor agent that sweep-emitted
// reminders escalate to when SweepChainConfig.AlertChain is unset
// (canonical §4.4).
type EscalationConfig struct {
	// SupervisorAgentName is the agent name (with or without leading
	// @ — the resolver normalizes). Empty → "coordinator" (canonical
	// default).
	SupervisorAgentName string `json:"supervisor_agent_name,omitempty"`
}

// StalledSweepIntervalMinutes returns the effective sweep cadence,
// clamping zero/negative values to the canonical 15-minute default
// per canonical §4.4.
func (c DaemonConfig) StalledSweepIntervalMinutes() int {
	if c.StalledSweep.IntervalMinutes > 0 {
		return c.StalledSweep.IntervalMinutes
	}
	return 15
}

// RemindersDispatchIntervalSeconds returns the effective dispatcher
// cadence, clamping zero/negative to the canonical 30-second default
// per canonical §4.4.
func (c DaemonConfig) RemindersDispatchIntervalSeconds() int {
	if c.Reminders.DispatchIntervalSeconds > 0 {
		return c.Reminders.DispatchIntervalSeconds
	}
	return 30
}

// DaemonSchedulerConfig holds A-B1 scheduler tuning consumed by daemon-boot
// wiring. Kept zero-valued by default so omitempty + back-compat both work;
// the consumer (internal.scheduler_event_cleanup handler) resolves zero to
// 7 days per A-B1 plan §5966-5969.
type DaemonSchedulerConfig struct {
	// EventRetentionDays controls scheduler_job_events pruning cadence
	// for the internal.scheduler_event_cleanup canonical cleanup job.
	// Zero (the default) means "use the consumer's default" (7 days).
	EventRetentionDays int `json:"event_retention_days,omitempty"`
}

// DaemonAgentLifecycleConfig holds B-B1 agent-lifecycle housekeeping
// tuning consumed by daemon-boot wiring. Zero-valued by default; the
// consumer (internal.agent_lifecycle_cleanup handler) clamps zero to 7
// days per canonical §6.3 + Q-Spec-3.
type DaemonAgentLifecycleConfig struct {
	// EventRetentionDays controls agent_lifecycle_events pruning
	// cadence. Zero (the default) means "use the consumer's default"
	// (7 days).
	EventRetentionDays int `json:"event_retention_days,omitempty"`
}

// BackupConfig holds backup-related settings.
type BackupConfig struct {
	Dir        string          `json:"dir,omitempty"`
	Schedule   string          `json:"schedule,omitempty"` // Go duration: "24h", "12h", "6h"; empty = disabled
	Retention  RetentionConfig `json:"retention"`
	Plugins    []PluginConfig  `json:"plugins,omitempty"`
	PostBackup string          `json:"post_backup,omitempty"`
}

// RetentionConfig controls GFS (Grandfather-Father-Son) archive rotation.
// Pointer fields distinguish "not set" (nil → use default) from explicit zero.
type RetentionConfig struct {
	Daily   *int `json:"daily"`   // default 5; nil = use default
	Weekly  *int `json:"weekly"`  // default 4; nil = use default
	Monthly *int `json:"monthly"` // default -1 (keep forever); nil = use default
}

// RetentionDaily returns the effective daily retention count.
func (r RetentionConfig) RetentionDaily() int {
	if r.Daily != nil {
		return *r.Daily
	}
	return DefaultRetentionDaily
}

// RetentionWeekly returns the effective weekly retention count.
func (r RetentionConfig) RetentionWeekly() int {
	if r.Weekly != nil {
		return *r.Weekly
	}
	return DefaultRetentionWeekly
}

// RetentionMonthly returns the effective monthly retention count.
func (r RetentionConfig) RetentionMonthly() int {
	if r.Monthly != nil {
		return *r.Monthly
	}
	return DefaultRetentionMonthly
}

// IntPtr is a helper to create a pointer to an int.
func IntPtr(v int) *int { return &v }

// PluginConfig defines a third-party backup plugin.
type PluginConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Include []string `json:"include"`
}

// Default retention values.
const (
	DefaultRetentionDaily   = 5
	DefaultRetentionWeekly  = 4
	DefaultRetentionMonthly = -1
)

// DefaultSyncInterval is the default git sync interval in seconds.
const DefaultSyncInterval = 60

// DefaultWSPort is the default WebSocket port strategy.
const DefaultWSPort = "auto"

// DefaultLogLevel is the default daemon log level.
const DefaultLogLevel = "info"

// RestartConfig controls session restart with context snapshot behavior.
type RestartConfig struct {
	MaxLines        int `json:"max_lines,omitempty"`        // Max lines in snapshot (default: 200)
	AutoThreshold   int `json:"auto_threshold,omitempty"`   // Context % trigger, 0 = disabled
	GracefulTimeout int `json:"graceful_timeout,omitempty"` // Seconds to wait for graceful save
	// SilenceWatchdogSeconds controls thrum-puhr.10: how long to wait
	// after a post-launch / post-restart injection before checking the
	// pane for activity and nudging it if still silent. 0 = use default
	// (30s). Negative = disabled (no watchdog).
	SilenceWatchdogSeconds int `json:"silence_watchdog_seconds,omitempty"`
}

// RestartMaxLines returns the configured max lines, defaulting to 200.
// 200 lines ≈ 8 terminal screens of recent conversation, enough to recover
// the current thread of work without burning ~20k tokens of context. For
// older context, agents should use `git log` / `git status` / `git diff`.
func (r RestartConfig) RestartMaxLines() int {
	if r.MaxLines <= 0 {
		return 200
	}
	return r.MaxLines
}

// RestartGracefulTimeout returns the configured timeout, defaulting to 30s.
func (r RestartConfig) RestartGracefulTimeout() int {
	if r.GracefulTimeout <= 0 {
		return 30
	}
	return r.GracefulTimeout
}

// SilenceWatchdog returns (seconds, enabled). seconds == 0 + enabled ==
// true means "watchdog disabled by explicit user choice" (negative
// config value); a positive return is the threshold the post-action
// goroutine should wait before sampling pane activity. Default 30s.
func (r RestartConfig) SilenceWatchdog() (seconds int, enabled bool) {
	if r.SilenceWatchdogSeconds < 0 {
		return 0, false
	}
	if r.SilenceWatchdogSeconds == 0 {
		return 30, true
	}
	return r.SilenceWatchdogSeconds, true
}

// LoadThrumConfig reads .thrum/config.json from the given thrum directory.
// Returns a zero-value ThrumConfig (all defaults) if the file doesn't exist.
func LoadThrumConfig(thrumDir string) (*ThrumConfig, error) {
	configPath := filepath.Join(thrumDir, "config.json")

	data, err := os.ReadFile(configPath) // #nosec G304 -- configPath is .thrum/config.json, an internal config file
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg := &ThrumConfig{
				Peers: DefaultPeersConfig(),
			}
			applyDefaults(cfg)
			return cfg, nil
		}
		return nil, err
	}

	// D-B1.2: fail fast if config.json contains a known-secret field name
	// (imap_password / smtp_password / oauth.refresh_token). Credentials
	// belong in .thrum/secrets/email.json.
	if err := CheckForSecretNames(data); err != nil {
		return nil, err
	}

	// thrum-1k00: detect whether the "peers" key is present in the raw
	// JSON. A stanza present with zero-values is distinguishable from
	// an absent stanza only at the raw JSON level — json.Unmarshal
	// leaves cfg.Peers at its Go zero-value in both cases. Without this
	// distinction, defaulting AutoConnect on a zero-valued struct would
	// clobber an explicit `auto_connect: false`.
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw) // best-effort; Unmarshal below reports real errors

	var cfg ThrumConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if _, peersPresent := raw["peers"]; !peersPresent {
		cfg.Peers = DefaultPeersConfig()
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// applyDefaults fills in sensible defaults for zero-value fields.
// Note: LocalOnly defaults to true (local-first). Users must explicitly
// set local_only=false in config.json to enable remote git sync.
func applyDefaults(cfg *ThrumConfig) {
	if cfg.Daemon.SyncInterval == 0 {
		cfg.Daemon.SyncInterval = DefaultSyncInterval
	}
	if cfg.Daemon.WSPort == "" {
		cfg.Daemon.WSPort = DefaultWSPort
	}
	if cfg.Daemon.LogLevel == "" {
		cfg.Daemon.LogLevel = DefaultLogLevel
	}
	if cfg.Backup.Retention.Daily == nil {
		cfg.Backup.Retention.Daily = IntPtr(DefaultRetentionDaily)
	}
	if cfg.Backup.Retention.Weekly == nil {
		cfg.Backup.Retention.Weekly = IntPtr(DefaultRetentionWeekly)
	}
	if cfg.Backup.Retention.Monthly == nil {
		cfg.Backup.Retention.Monthly = IntPtr(DefaultRetentionMonthly)
	}
	// Stanza-present-but-partial defaults. When the whole peers stanza
	// is absent LoadThrumConfig has already substituted DefaultPeersConfig()
	// (thrum-1k00), so here we only fill individual fields that remained
	// at zero-value after a partial user-supplied stanza.
	if cfg.Peers.PairingCodeLength == 0 {
		cfg.Peers.PairingCodeLength = DefaultPeersConfig().PairingCodeLength
	}
	if cfg.Daemon.PeerPort == "" {
		cfg.Daemon.PeerPort = "auto"
	}
}

// ValidatePermissionSupervisors checks whether the configured supervisor
// list contains at least one coordinator-role recipient. The permission
// nudge pipeline uses this array as the authoritative routing list
// (thrum-zmsk); if an operator forgets to include a coordinator, prompts
// can land in dead mailboxes.
//
// A config satisfies the invariant when the list contains one of:
//   - the bare role "coordinator"
//   - an "@coordinator_*" or "@coordinator-*" agent name (any name with
//     that prefix is treated as a coordinator-role entry by this check,
//     matching the codebase convention that coordinator agents are named
//     @coordinator_<module>)
//
// Returns a human-readable warning describing the problem, or "" when the
// config is valid. An empty / nil list is considered valid — the resolver
// falls back to ["coordinator"] at dispatch time in that case.
func ValidatePermissionSupervisors(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	for _, e := range entries {
		if e == "coordinator" {
			return ""
		}
		if strings.HasPrefix(e, "@coordinator_") || strings.HasPrefix(e, "@coordinator-") {
			return ""
		}
	}
	return "permission_supervisors is set but contains no coordinator-role entry (" +
		strings.Join(entries, ", ") +
		"); permission prompts may go undelivered if listed agents are offline. " +
		"Add \"coordinator\" or an @coordinator_<name> agent to .thrum/config.json."
}

// SaveThrumConfig writes .thrum/config.json, merging with any existing content.
// Reads the file first so future top-level keys are preserved.
func SaveThrumConfig(thrumDir string, cfg *ThrumConfig) error {
	configPath := filepath.Join(thrumDir, "config.json")

	// Read existing file to preserve unknown keys
	existing := make(map[string]any)
	if data, err := os.ReadFile(configPath); err == nil { // #nosec G304 -- configPath is .thrum/config.json, an internal config file
		_ = json.Unmarshal(data, &existing) // best-effort; overwrite if corrupt
	}

	// Marshal and merge the runtime section (only if non-empty)
	if cfg.Runtime.Primary != "" {
		runtimeBytes, err := json.Marshal(cfg.Runtime)
		if err != nil {
			return err
		}
		var runtimeMap any
		_ = json.Unmarshal(runtimeBytes, &runtimeMap)
		existing["runtime"] = runtimeMap
	}

	// Marshal and merge the daemon section
	daemonBytes, err := json.Marshal(cfg.Daemon)
	if err != nil {
		return err
	}
	var daemonMap any
	_ = json.Unmarshal(daemonBytes, &daemonMap)
	existing["daemon"] = daemonMap

	// Marshal and merge the backup section (only if user has configured something)
	isDefaultRetention := cfg.Backup.Retention.RetentionDaily() == DefaultRetentionDaily &&
		cfg.Backup.Retention.RetentionWeekly() == DefaultRetentionWeekly &&
		cfg.Backup.Retention.RetentionMonthly() == DefaultRetentionMonthly
	if cfg.Backup.Dir != "" || cfg.Backup.Schedule != "" || len(cfg.Backup.Plugins) > 0 || cfg.Backup.PostBackup != "" || !isDefaultRetention {
		backupBytes, err := json.Marshal(cfg.Backup)
		if err != nil {
			return err
		}
		var backupMap any
		_ = json.Unmarshal(backupBytes, &backupMap)
		existing["backup"] = backupMap
	}

	// Marshal and merge the restart section (only if any field is set)
	if cfg.Restart.MaxLines > 0 || cfg.Restart.AutoThreshold > 0 || cfg.Restart.GracefulTimeout > 0 {
		restartBytes, err := json.Marshal(cfg.Restart)
		if err != nil {
			return err
		}
		var restartMap any
		_ = json.Unmarshal(restartBytes, &restartMap)
		existing["restart"] = restartMap
	}

	// Marshal and merge the telegram section (only if token is set or explicitly configured)
	if cfg.Telegram.Token != "" || cfg.Telegram.Enabled != nil || cfg.Telegram.AllowAll || len(cfg.Telegram.AllowFrom) > 0 || len(cfg.Telegram.Groups) > 0 {
		telegramBytes, err := json.Marshal(cfg.Telegram)
		if err != nil {
			return err
		}
		var telegramMap any
		_ = json.Unmarshal(telegramBytes, &telegramMap)
		existing["telegram"] = telegramMap
	}

	// Marshal and merge the email section (omit when at zero value).
	if !isZeroEmail(cfg.Email) {
		emailBytes, err := json.Marshal(cfg.Email)
		if err != nil {
			return err
		}
		var emailMap any
		_ = json.Unmarshal(emailBytes, &emailMap)
		existing["email"] = emailMap
	}

	// Marshal and merge the users section (only if any entry).
	if len(cfg.Users) > 0 {
		usersBytes, err := json.Marshal(cfg.Users)
		if err != nil {
			return err
		}
		var usersMap any
		_ = json.Unmarshal(usersBytes, &usersMap)
		existing["users"] = usersMap
	}

	// Marshal and merge the worktrees section (only if base_path is set)
	if cfg.Worktrees.BasePath != "" {
		existing["worktrees"] = cfg.Worktrees
	}

	// Marshal and merge the orchestration section (only if merge_target is set)
	if cfg.Orchestration.MergeTarget != "" {
		existing["orchestration"] = cfg.Orchestration
	}

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// Atomic write via temp file + rename. canonical-ref §3.10 Property 1
	// mandates this for the daemon-driven mesh-mutation path; the operator
	// write path uses the same primitive so a crash mid-write cannot
	// produce a half-written config.json that would lose the whole
	// email.peers[] roster on next daemon boot.
	dir := filepath.Dir(configPath)
	tmp, err := os.CreateTemp(dir, "config.json.tmp-*")
	if err != nil {
		return fmt.Errorf("temp config: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod config: %w", err)
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// AddPlugin adds a plugin to the config, replacing any existing plugin with the same name.
func (cfg *ThrumConfig) AddPlugin(p PluginConfig) {
	for i, existing := range cfg.Backup.Plugins {
		if existing.Name == p.Name {
			cfg.Backup.Plugins[i] = p
			return
		}
	}
	cfg.Backup.Plugins = append(cfg.Backup.Plugins, p)
}

// RemovePlugin removes a plugin by name. Returns true if found and removed.
func (cfg *ThrumConfig) RemovePlugin(name string) bool {
	for i, p := range cfg.Backup.Plugins {
		if p.Name == name {
			cfg.Backup.Plugins = append(cfg.Backup.Plugins[:i], cfg.Backup.Plugins[i+1:]...)
			return true
		}
	}
	return false
}
