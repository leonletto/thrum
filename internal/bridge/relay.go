package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// InboundMessage is the transport-agnostic representation of a message
// arriving from an external source (Telegram, peer, etc.).
type InboundMessage struct {
	Content     string         // Message text
	SenderID    string         // External sender identity
	SenderName  string         // Display name
	ExternalKey string         // Transport-specific key for MessageMap (e.g. "chatID:msgID")
	ReplyToKey  string         // ExternalKey of the message being replied to (empty if not a reply)
	GroupName   string         // Target group name (empty for DMs)
	Mentions    []string       // Extracted @mentions (transport-specific parsing happens before relay)
	Structured  map[string]any // Transport-specific metadata (passed through to message.send)
}

// OutboundRoute describes where an outbound message should be sent.
type OutboundRoute struct {
	Type        RouteType
	GroupName   string // For group/reply-to-group routes
	ProxyAgent  string // For proxy routes (e.g. "mock-sf:coordinator_main")
	ExternalKey string // For reply threading
}

// RouteType enumerates outbound routing paths.
type RouteType int

const (
	RouteSkip         RouteType = iota // Echo — don't relay
	RouteGroup                         // Send to a transport-specific group
	RouteReplyToGroup                  // Reply to a message in a group
	RouteProxy                         // Send via proxy agent path
	RouteDM                            // Direct message to bridge user
)

// ProxyConfig describes a proxy agent configuration for relay routing.
type ProxyConfig struct {
	Prefix    string // e.g. "mock-sf"
	AgentName string // e.g. "coordinator_main"
	GroupName string // Thrum group this proxy belongs to
}

// GroupConfig describes a group that the relay manages.
type GroupConfig struct {
	Name      string // Human-readable group name
	ThrumName string // Thrum group name (e.g. "tg:groupname" or "peer:groupname")
}

// RelayConfig holds the configuration for the common relay logic.
type RelayConfig struct {
	BridgeUserID string        // The bridge's agent/user ID (for echo prevention)
	Target       string        // Default mention target for DMs (e.g. "@coordinator_main")
	Groups       []GroupConfig // Configured groups
	Proxies      []ProxyConfig // Configured proxy agents
}

// Relay handles common inbound/outbound message relay logic.
// Transport-specific code (Telegram bot, peer WS) calls into this.
type Relay struct {
	ws     TransportBridge
	msgMap *MessageMap
	cfg    RelayConfig
	logger *log.Logger
}

// NewRelay creates a relay with the given transport bridge, message map, and config.
func NewRelay(ws TransportBridge, msgMap *MessageMap, cfg RelayConfig, logger *log.Logger) *Relay {
	return &Relay{ws: ws, msgMap: msgMap, cfg: cfg, logger: logger}
}

// RelayInbound routes an external message into the local Thrum daemon.
func (r *Relay) RelayInbound(ctx context.Context, msg InboundMessage) error {
	params := map[string]any{
		"content":         msg.Content,
		"caller_agent_id": r.cfg.BridgeUserID,
	}

	// Determine mentions target.
	if msg.GroupName != "" {
		thrumGroup := r.findGroupThrumName(msg.GroupName)
		if thrumGroup == "" {
			return fmt.Errorf("unknown group: %s", msg.GroupName)
		}
		params["mentions"] = []string{thrumGroup}
	} else {
		params["mentions"] = []string{r.cfg.Target}
	}

	// Attach structured metadata.
	if msg.Structured != nil {
		params["structured"] = msg.Structured
	}

	// Reply threading.
	if msg.ReplyToKey != "" {
		if thrumID, ok := r.msgMap.ThrumID(msg.ReplyToKey); ok {
			params["reply_to"] = thrumID
		}
	}

	result, err := r.ws.Call(ctx, "message.send", params)
	if err != nil {
		return fmt.Errorf("message.send: %w", err)
	}

	// Store mapping for reply threading.
	if msg.ExternalKey != "" {
		var resp struct {
			MessageID string `json:"message_id"`
		}
		if json.Unmarshal(result, &resp) == nil && resp.MessageID != "" {
			r.msgMap.Store(msg.ExternalKey, resp.MessageID)
		}
	}

	return nil
}

// ClassifyOutbound determines how an outbound Thrum message should be routed
// to the external transport.
//
// Path priority (matches telegram/outbound.go ordering):
//  1. Echo prevention — skip messages from own bridge user
//  2. Group — recipient is a configured group
//  3. Reply-to-group — message replies to a message that was in a group
//  4. Proxy — recipient is a configured proxy agent
//  5. DM — recipient is the bridge user
func (r *Relay) ClassifyOutbound(authorAgentID string, recipients []string, replyTo string) OutboundRoute {
	// Path 1: Echo prevention.
	if authorAgentID == r.cfg.BridgeUserID {
		return OutboundRoute{Type: RouteSkip}
	}

	// Path 2: Group.
	for _, recip := range recipients {
		if g := r.findGroupByThrumName(recip); g != nil {
			return OutboundRoute{Type: RouteGroup, GroupName: g.Name}
		}
	}

	// Path 3: Reply-to-group.
	if replyTo != "" {
		if extKey, ok := r.msgMap.ExternalKey(replyTo); ok {
			return OutboundRoute{Type: RouteReplyToGroup, ExternalKey: extKey}
		}
	}

	// Path 4: Proxy.
	for _, recip := range recipients {
		if p := r.findProxy(recip); p != nil {
			return OutboundRoute{Type: RouteProxy, ProxyAgent: fmt.Sprintf("%s:%s", p.Prefix, p.AgentName), GroupName: p.GroupName}
		}
	}

	// Path 5: DM.
	for _, recip := range recipients {
		if recip == r.cfg.BridgeUserID {
			return OutboundRoute{Type: RouteDM}
		}
	}

	return OutboundRoute{Type: RouteSkip}
}

// EnsureProxies registers proxy agents and creates groups on the local daemon.
// Idempotent — safe to call on reconnect.
func (r *Relay) EnsureProxies(ctx context.Context) error {
	// Fetch existing groups once, not per-group.
	groupListResult, err := r.ws.Call(ctx, "group.list", nil)
	if err != nil {
		return fmt.Errorf("group.list: %w", err)
	}

	for _, g := range r.cfg.Groups {
		if !groupExists(groupListResult, g.ThrumName) {
			_, err = r.ws.Call(ctx, "group.create", map[string]any{
				"name":        g.ThrumName,
				"description": fmt.Sprintf("Mirrored group: %s", g.Name),
			})
			if err != nil {
				r.logger.Printf("relay: group.create %s: %v", g.ThrumName, err)
			}
		}

		// Add bridge user to group.
		// Note: group.member.add requires "member_type" + "member_value", NOT "agent".
		_, _ = r.ws.Call(ctx, "group.member.add", map[string]any{
			"group":        g.ThrumName,
			"member_type":  "agent",
			"member_value": r.cfg.BridgeUserID,
		})

		// Add target agent if configured.
		if r.cfg.Target != "" {
			target := strings.TrimPrefix(r.cfg.Target, "@")
			_, _ = r.ws.Call(ctx, "group.member.add", map[string]any{
				"group":        g.ThrumName,
				"member_type":  "agent",
				"member_value": target,
			})
		}
	}

	// Register proxy agents.
	for _, p := range r.cfg.Proxies {
		proxyName := fmt.Sprintf("%s:%s", p.Prefix, p.AgentName)
		_, err := r.ws.Call(ctx, "agent.register", map[string]any{
			"name":    proxyName,
			"role":    "remote",
			"module":  p.Prefix,
			"display": fmt.Sprintf("%s %s (via peer)", p.Prefix, p.AgentName),
		})
		if err != nil {
			r.logger.Printf("relay: agent.register %s: %v", proxyName, err)
			continue
		}

		if p.GroupName != "" {
			thrumGroup := r.findGroupThrumName(p.GroupName)
			if thrumGroup != "" {
				_, _ = r.ws.Call(ctx, "group.member.add", map[string]any{
					"group":        thrumGroup,
					"member_type":  "agent",
					"member_value": proxyName,
				})
			}
		}
	}

	return nil
}

func (r *Relay) findGroupThrumName(name string) string {
	for _, g := range r.cfg.Groups {
		if g.Name == name {
			return g.ThrumName
		}
	}
	return ""
}

func (r *Relay) findGroupByThrumName(thrumName string) *GroupConfig {
	for i := range r.cfg.Groups {
		if r.cfg.Groups[i].ThrumName == thrumName {
			return &r.cfg.Groups[i]
		}
	}
	return nil
}

func (r *Relay) findProxy(recipientID string) *ProxyConfig {
	for i := range r.cfg.Proxies {
		proxyName := fmt.Sprintf("%s:%s", r.cfg.Proxies[i].Prefix, r.cfg.Proxies[i].AgentName)
		if proxyName == recipientID {
			return &r.cfg.Proxies[i]
		}
	}
	return nil
}

// groupExists checks if a group name exists in a group.list response.
// group.list returns {"groups": [...]}, NOT a flat array.
func groupExists(result json.RawMessage, name string) bool {
	var resp struct {
		Groups []struct {
			Name string `json:"name"`
		} `json:"groups"`
	}
	if json.Unmarshal(result, &resp) != nil {
		return false
	}
	for _, g := range resp.Groups {
		if g.Name == name {
			return true
		}
	}
	return false
}
