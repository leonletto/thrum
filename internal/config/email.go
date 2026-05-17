package config

// EmailConfig holds the .thrum/config.json `email` block — non-secret
// connection metadata, mesh policy, rate limits, and operator-curated
// peer roster. Secret credentials (passwords, OAuth tokens) live in
// .thrum/secrets/email.json and never in this struct.
//
// Field shapes track design-spec §4. Mesh + plus-addressing are kept under
// the mailbox-access trust root (Scope B); signed envelopes are deferred
// to E18 / v0.11.x.
type EmailConfig struct {
	Enabled bool `json:"enabled,omitempty"`

	IMAP EmailIMAP `json:"imap,omitzero"`
	SMTP EmailSMTP `json:"smtp,omitzero"`

	AuthMethod            string   `json:"auth_method,omitempty"`              // "password" (v0.11); "oauth" reserved
	Username              string   `json:"username,omitempty"`                 // IMAP/SMTP login user
	FromAddress           string   `json:"from_address,omitempty"`             // RFC 5322 From: address
	FromDisplayNameFormat string   `json:"from_display_name_format,omitempty"` // e.g. "{agent} @ {handle}"
	DaemonHandle          string   `json:"daemon_handle,omitempty"`            // mesh-visible handle (plus-address label)
	TargetUser            string   `json:"target_user,omitempty"`              // thrum username this mailbox bridges to
	TargetEmail           string   `json:"target_email,omitempty"`             // operator email for supervisor relay
	DefaultMention        string   `json:"default_mention,omitempty"`          // fallback @mention for inbound w/o explicit target
	AllowFrom             []string `json:"allow_from,omitempty"`               // allowlisted From addresses
	PollIntervalSeconds   int      `json:"poll_interval_seconds,omitempty"`    // IMAP poll fallback (when IDLE unavailable)
	EmbedShortID          bool     `json:"embed_short_id,omitempty"`           // include thrum message short-id in subject
	UnknownRecipient      string   `json:"unknown_recipient,omitempty"`        // "drop" (v0.11 default); other strategies reserved
	MaxOutboundBytes      int      `json:"max_outbound_bytes,omitempty"`       // body size ceiling per outbound MIME

	RateLimits EmailRateLimits `json:"rate_limits,omitzero"`
	Mesh       EmailMesh       `json:"mesh,omitzero"`
	Queue      EmailQueue      `json:"queue,omitzero"`

	Peers []EmailPeer `json:"peers,omitempty"`
}

// EmailIMAP holds IMAP connection parameters. Password lives in
// secrets/email.json (imap_password); never populated from config.json.
type EmailIMAP struct {
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	UseStartTLS bool   `json:"use_starttls,omitempty"`
	UseIDLE     bool   `json:"use_idle,omitempty"`
}

// EmailSMTP holds SMTP submission parameters. Password lives in
// secrets/email.json (smtp_password); never populated from config.json.
//
// InsecureSkipVerify carries json:"-" so operators cannot disable TLS
// verification via config.json — per plan §D-B1.5 / decision #6. The
// field exists for test injection only.
type EmailSMTP struct {
	Host               string `json:"host,omitempty"`
	Port               int    `json:"port,omitempty"`
	UseStartTLS        bool   `json:"use_starttls,omitempty"`
	InsecureSkipVerify bool   `json:"-"`
}

// EmailMesh holds mesh-pairing + gossip policy (canonical-ref §3.10).
type EmailMesh struct {
	VouchAcceptance         string `json:"vouch_acceptance,omitempty"`          // "auto_with_notify" | "manual" | "auto"
	VouchTTLHours           int    `json:"vouch_ttl_hours,omitempty"`           // 0 = no expiry
	AllowTransitiveVouching bool   `json:"allow_transitive_vouching,omitempty"` // accept B-via-A pairings
	RevocationPropagation   string `json:"revocation_propagation,omitempty"`    // "gossip" | "manual"
	HopCountCeiling         int    `json:"hop_count_ceiling,omitempty"`         // mesh-wide loop ceiling for relayed messages
	PairPendingTTLHours     int    `json:"pair_pending_ttl_hours,omitempty"`    // pending stranger-pair expiry; 0 → 24h default in bridge
}

// EmailRateLimits holds the per-peer + global inbound/outbound caps.
// Enforcement lives in internal/bridge/email/ratelimit.go (D-B1.9).
type EmailRateLimits struct {
	OutboundPerPeerPerHour int `json:"outbound_per_peer_per_hour,omitempty"`
	InboundPerPeerPerHour  int `json:"inbound_per_peer_per_hour,omitempty"`
	GlobalInboundPerMinute int `json:"global_inbound_per_minute,omitempty"`
}

// EmailQueue holds outbound-queue retry tuning. Workers exponentially
// back off failed sends up to MaxAttempts.
type EmailQueue struct {
	MaxAttempts           int `json:"max_attempts,omitempty"`
	BackoffInitialSeconds int `json:"backoff_initial_seconds,omitempty"`
	BackoffCapSeconds     int `json:"backoff_cap_seconds,omitempty"`
}

// EmailPeer is an operator-curated mesh peer entry. DaemonID is a public
// identifier (NOT a credential) — the secrets-leak check whitelists it.
// VouchedBy is either "self" (operator-added) or another peer's handle.
type EmailPeer struct {
	Handle       string `json:"handle,omitempty"`
	DaemonID     string `json:"daemon_id,omitempty"`
	ContactEmail string `json:"contact_email,omitempty"`
	VouchedBy    string `json:"vouched_by,omitempty"`
	AddedAt      string `json:"added_at,omitempty"` // RFC 3339
	Trust        string `json:"trust,omitempty"`    // "full" | "limited" | "revoked"
}

// UserPrefs holds per-user delivery preferences. Q11 outbound relay
// treats an absent or empty PreferredChannel as "both" — the struct
// preserves operator intent rather than backfilling at load time.
type UserPrefs struct {
	PreferredChannel string `json:"preferred_channel,omitempty"` // "telegram" | "email" | "both"
}

// isZeroEmail reports whether an EmailConfig is at its zero value
// (no operator-configured fields). SaveThrumConfig uses this to skip
// emitting an empty email block — the absent-block tolerance in
// TestEmailConfig_AbsentEmailBlock depends on it.
func isZeroEmail(e EmailConfig) bool {
	if e.Enabled || e.AuthMethod != "" || e.Username != "" || e.FromAddress != "" ||
		e.FromDisplayNameFormat != "" || e.DaemonHandle != "" || e.TargetUser != "" ||
		e.TargetEmail != "" || e.DefaultMention != "" || e.PollIntervalSeconds != 0 ||
		e.EmbedShortID || e.UnknownRecipient != "" || e.MaxOutboundBytes != 0 {
		return false
	}
	if len(e.AllowFrom) > 0 || len(e.Peers) > 0 {
		return false
	}
	return e.IMAP == (EmailIMAP{}) && e.SMTP == (EmailSMTP{}) &&
		e.RateLimits == (EmailRateLimits{}) && e.Mesh == (EmailMesh{}) &&
		e.Queue == (EmailQueue{})
}
