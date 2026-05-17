package email

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

// MessageNotification is the data shape from the WSClient notification.message
// event. Field names match the JSON emitted by the daemon's RPC layer.
type MessageNotification struct {
	Body   string `json:"body"`
	Author struct {
		AgentID string `json:"agent_id"`
	} `json:"author"`
	To         string `json:"to"`          // recipient mention or Prefix:Name
	ReplyTo    string `json:"reply_to"`    // optional parent thrum_msg_id
	ThrumMsgID string `json:"thrum_msg_id"` // this message's own id (for msgmap)
	Subject    string `json:"subject"`
}

// WSSubscriber is the abstraction over WSClient.Subscribe. Bridge (D-B1.14)
// injects the real WSClient; tests inject a channel-backed stub.
type WSSubscriber interface {
	Subscribe(ctx context.Context, method string) (<-chan MessageNotification, error)
}

// OutboundConfig holds the static configuration for the outbound relay.
type OutboundConfig struct {
	MyDaemonID    string
	MyDaemonShort string // 8-hex-char short for plus-addressing and Message-Id generation

	// MyBridgeUserAgentID is the agent_id this bridge sends AS — when a
	// notification.message arrives with Author.AgentID == this value, it's
	// our own outbound being echoed back through the WS bus; suppress to
	// avoid an echo loop. Notif.Author.AgentID carries an AGENT NAME
	// (e.g. "coordinator_main"), NOT the daemon UUID — comparing against
	// MyDaemonID would never match (D-B1 brainstormer review BLOCKING-1).
	MyBridgeUserAgentID string

	Host                  string                      // mail-domain for Message-Ids
	FromAddress           string
	FromDisplayNameFormat string                      // "{agent} @ {handle}" template
	DaemonHandle          string                      // for {handle} substitution
	TargetUser            string                      // local user this mailbox bridges to (Q11 check)
	TargetEmail           string                      // recipient email for supervisor relay
	DefaultMention        string                      // fallback @mention when notification has none
	EmbedShortID          bool                        // include short-id in subject line
	KnownPeers            map[string]config.EmailPeer // prefix → peer
	UserPrefs             map[string]config.UserPrefs // username → prefs (Q11 preferred_channel lookup)
	Repo                  string                      // for X-Thrum-Repo header
}

// Outbound subscribes to notification.message events and enqueues outbound
// emails for each qualifying message.
type Outbound struct {
	cfg     OutboundConfig
	sub     WSSubscriber
	msgmap  *MsgMap
	limiter *Limiter
	queue   *Queue
}

// NewOutbound wires the relay's dependencies.
func NewOutbound(cfg OutboundConfig, sub WSSubscriber, msgmap *MsgMap, limiter *Limiter, queue *Queue) *Outbound {
	return &Outbound{
		cfg:     cfg,
		sub:     sub,
		msgmap:  msgmap,
		limiter: limiter,
		queue:   queue,
	}
}

// Run subscribes once and processes notifications until ctx is canceled.
func (o *Outbound) Run(ctx context.Context) error {
	ch, err := o.sub.Subscribe(ctx, "notification.message")
	if err != nil {
		return fmt.Errorf("outbound relay subscribe: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case notif, ok := <-ch:
			if !ok {
				return nil
			}
			o.handle(ctx, notif)
		}
	}
}

// handle processes one notification. Errors are logged; the relay never panics.
func (o *Outbound) handle(ctx context.Context, notif MessageNotification) {
	// Self-echo skip: notif.Author.AgentID is an agent NAME (e.g.
	// "coordinator_main"); MyBridgeUserAgentID is the same shape — the
	// agent_id this bridge speaks as. When they match, the notification
	// is our own outbound being echoed back through the WS bus and we'd
	// loop forever forwarding it.
	if o.cfg.MyBridgeUserAgentID != "" && notif.Author.AgentID == o.cfg.MyBridgeUserAgentID {
		slog.Debug("email outbound: self-echo skipped", "author", notif.Author.AgentID)
		return
	}

	// Q11 preferred_channel filter. "telegram" means the Telegram bridge owns
	// this user's notifications; we skip to avoid double-delivery. "email" or
	// "both" (or absent) → continue. Each bridge has its own queue so "both"
	// triggers both without shared state.
	if prefs, ok := o.cfg.UserPrefs[o.cfg.TargetUser]; ok {
		if prefs.PreferredChannel == "telegram" {
			slog.Debug("email outbound: preferred_channel=telegram, skipping", "user", o.cfg.TargetUser)
			return
		}
	}

	toAddr, peerDaemonID, classified := o.classify(notif.To)
	if !classified {
		slog.Debug("email outbound: recipient not routable, ignoring", "to", notif.To)
		return
	}

	subject := o.buildSubject(notif)
	fromDisplay := o.buildFromDisplay(notif.Author.AgentID)

	// Generate a fresh Message-Id for this outbound message.
	outMsgID := notif.ThrumMsgID
	if outMsgID == "" {
		// Notification didn't carry a thrum_msg_id; use a timestamp-based fallback
		// so we still get a unique Message-Id. This is a degenerate case.
		outMsgID = fmt.Sprintf("noid-%d", time.Now().UnixNano())
	}
	messageID := GenerateMessageId(o.cfg.MyDaemonShort, outMsgID, o.cfg.Host)

	// Threading: reconstruct the parent's Message-Id from the parent thrum_msg_id.
	// MsgMap is keyed Message-Id → ThrumMsgID (inbound direction); a full reverse
	// map would require a separate data structure. For now we reconstruct using
	// our own daemon-short, which is correct for supervisor-relay replies (the
	// common case — the parent outbound was also ours). Cross-daemon reverse
	// lookup is a v0.11.x extension once integration tests exercise it
	// (tracked under follow-up; see thrum-6qmf.8 sibling discussion).
	var inReplyTo string
	var references []string
	if notif.ReplyTo != "" {
		parentMsgID := GenerateMessageId(o.cfg.MyDaemonShort, notif.ReplyTo, o.cfg.Host)
		inReplyTo = parentMsgID
		references = []string{parentMsgID}
	}

	// Rate-limit check. peerKey is directional so the Limiter's per-peer
	// counters stay consistent: <myDaemonID>-><peerTarget>.
	peerKey := o.cfg.MyDaemonID + "->" + peerDaemonID
	allowed, paused, err := o.limiter.IncrementOutbound(ctx, peerKey)
	if err != nil {
		slog.Error("email outbound: rate-limit check error", "peer_key", peerKey, "err", err)
		return
	}
	if paused || !allowed {
		slog.Warn("email outbound: rate-limited, not enqueuing", "peer_key", peerKey)
		return
	}

	env := AgentMessageEnvelope{
		FromAddr:        o.cfg.FromAddress,
		FromDisplayName: fromDisplay,
		ToAddr:          toAddr,
		Subject:         subject,
		MessageID:       messageID,
		InReplyTo:       inReplyTo,
		References:      references,
		Date:            time.Now().UTC(),
		FromDaemonID:    o.cfg.MyDaemonID,
		ToDaemonID:      peerDaemonID,
		FromAgent:       notif.Author.AgentID,
		ToAgent:         notif.To,
		ShortMessageID:  outMsgID,
		HopCount:        0, // always 0 on origination
		Repo:            o.cfg.Repo,
		Body:            notif.Body,
	}

	raw, err := ComposeAgentMessage(env)
	if err != nil {
		slog.Error("email outbound: compose failed", "err", err)
		return
	}

	headersJSON, err := buildHeadersJSON(env)
	if err != nil {
		slog.Error("email outbound: build headers_json failed", "err", err)
		return
	}

	qe := QueueEnvelope{
		FromAgent:   notif.Author.AgentID,
		ToAddress:   toAddr,
		Subject:     subject,
		Body:        string(raw),
		HeadersJSON: headersJSON,
	}

	if _, err := o.queue.Enqueue(ctx, qe); err != nil {
		slog.Error("email outbound: enqueue failed", "to", toAddr, "err", err)
		return
	}

	slog.Info("email outbound: enqueued", "to", toAddr, "msg_id", messageID, "thrum_msg_id", outMsgID)
}

// classify determines the delivery target from the notification's To field.
// Returns (toAddress, peerDaemonIDForRateKey, ok).
//
// Two paths:
//   - Supervisor path: To is an agent mention (@name) or absent → send to
//     cfg.TargetEmail; peerDaemonID = cfg.TargetEmail (stable per-peer key).
//   - Cross-daemon path: To is "Prefix:Name" with Prefix in KnownPeers →
//     send to peer's ContactEmail with plus-addressing.
func (o *Outbound) classify(to string) (toAddr, peerDaemonID string, ok bool) {
	// Cross-daemon path: Prefix:Name form where Prefix is a known peer.
	if idx := strings.Index(to, ":"); idx > 0 {
		prefix := to[:idx]
		if peer, found := o.cfg.KnownPeers[prefix]; found {
			if peer.ContactEmail == "" {
				return "", "", false
			}
			// Plus-addressing: <local>+<daemonShort>--<prefix>@<domain>
			plusAddr := buildPlusAddress(peer.ContactEmail, o.cfg.MyDaemonShort, prefix)
			peerID := peer.DaemonID
			if peerID == "" {
				peerID = peer.ContactEmail
			}
			return plusAddr, peerID, true
		}
	}

	// Supervisor path: any @mention (or missing To) routes to the operator mailbox.
	// The bridge user's own email is the single trusted recipient for local messages.
	if o.cfg.TargetEmail == "" {
		return "", "", false
	}
	return o.cfg.TargetEmail, o.cfg.TargetEmail, true
}

// buildSubject formats the email subject per the EmbedShortID flag.
func (o *Outbound) buildSubject(notif MessageNotification) string {
	orig := notif.Subject
	if orig == "" {
		orig = "(no subject)"
	}
	agent := notif.Author.AgentID
	prefix := fmt.Sprintf("[thrum:%s/%s]", o.cfg.DaemonHandle, agent)
	if o.cfg.EmbedShortID && notif.ThrumMsgID != "" {
		return fmt.Sprintf("%s %s %s", prefix, notif.ThrumMsgID, orig)
	}
	return fmt.Sprintf("%s %s", prefix, orig)
}

// buildFromDisplay substitutes {agent} and {handle} in the display-name template.
func (o *Outbound) buildFromDisplay(agentID string) string {
	s := o.cfg.FromDisplayNameFormat
	s = strings.ReplaceAll(s, "{agent}", agentID)
	s = strings.ReplaceAll(s, "{handle}", o.cfg.DaemonHandle)
	return s
}

// buildPlusAddress inserts a plus-address label into the local part of addr.
// E.g. "user@example.com" → "user+<short>--<prefix>@example.com".
// If addr contains no '@', returns addr unchanged (degenerate input guard).
func buildPlusAddress(addr, daemonShort, prefix string) string {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return addr
	}
	local := addr[:at]
	domain := addr[at+1:]
	return fmt.Sprintf("%s+%s--%s@%s", local, daemonShort, prefix, domain)
}

// buildHeadersJSON serialises the X-Thrum-* routing headers into a compact
// JSON object so the queue row's headers_json column carries them for the
// worker to forward downstream. The raw MIME body in QueueEnvelope.Body
// already contains these headers; this JSON copy is for indexed lookup by
// future tooling (D-B1.x).
func buildHeadersJSON(env AgentMessageEnvelope) (string, error) {
	m := map[string]string{
		"X-Thrum-From-Daemon": env.FromDaemonID,
		"X-Thrum-To-Daemon":   env.ToDaemonID,
		"X-Thrum-From-Agent":  env.FromAgent,
		"X-Thrum-To-Agent":    env.ToAgent,
		"X-Thrum-Message-Id":  env.ShortMessageID,
		"X-Thrum-Kind":        "message",
		"X-Thrum-Hop-Count":   fmt.Sprintf("%d", env.HopCount),
		"X-Thrum-Repo":        env.Repo,
	}
	if env.InReplyTo != "" {
		m["In-Reply-To"] = env.InReplyTo
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
