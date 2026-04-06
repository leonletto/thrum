package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/bridge"
)

// BridgeConfig holds all configuration for the peer bridge.
type BridgeConfig struct {
	LocalWSPort  string
	PeerName     string
	PeerAddress  string // For network peers (mutually exclusive with PeerRepoPath)
	PeerRepoPath string // For local peers (mutually exclusive with PeerAddress)
	PeerToken    string
	BridgeUserID string   // e.g. "user:peer-mock-sf"
	ProxyPrefix  string   // e.g. "mock-sf"
	RemoteAgents []string // e.g. ["coordinator_main"]
	Target       string   // Default mention target (optional)
}

// Bridge orchestrates the peer↔Thrum bridge lifecycle.
// It connects to both the local daemon WebSocket and the remote peer daemon,
// then relays messages bidirectionally.
type Bridge struct {
	cfg    BridgeConfig
	logger *log.Logger
}

// NewBridge creates a new peer Bridge with the given config and logger.
func NewBridge(cfg BridgeConfig, logger *log.Logger) *Bridge {
	if logger == nil {
		logger = log.New(os.Stderr, "peer bridge: ", log.LstdFlags)
	}
	return &Bridge{cfg: cfg, logger: logger}
}

// Run starts the bridge and blocks until ctx is cancelled or an unrecoverable
// error occurs.
func (b *Bridge) Run(ctx context.Context) error {
	// 1. Connect to local daemon via loopback-only WSClient.
	localURL := fmt.Sprintf("ws://127.0.0.1:%s/ws", b.cfg.LocalWSPort)
	localWS := bridge.NewWSClient(localURL,
		bridge.WithAddressValidator(bridge.LoopbackValidator),
		bridge.WithPeerName("local"),
	)
	if err := localWS.Connect(ctx); err != nil {
		return fmt.Errorf("local ws connect: %w", err)
	}
	defer localWS.Close()

	// 2. Register as bridge user.
	username := strings.TrimPrefix(b.cfg.BridgeUserID, "user:")
	_, err := localWS.Call(ctx, "user.register", map[string]any{
		"username": username,
		"display":  "Peer Bridge (" + username + ")",
	})
	if err != nil {
		return fmt.Errorf("user.register: %w", err)
	}

	// 3. Start session.
	sessResult, err := localWS.Call(ctx, "session.start", map[string]any{
		"agent_id": b.cfg.BridgeUserID,
	})
	if err != nil {
		return fmt.Errorf("session.start: %w", err)
	}
	var sess struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(sessResult, &sess); err != nil || sess.SessionID == "" {
		return fmt.Errorf("session.start: could not extract session_id")
	}

	// 4. Defer session.end cleanup.
	defer func() {
		endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = localWS.Call(endCtx, "session.end", map[string]any{
			"session_id": sess.SessionID,
			"reason":     "bridge_shutdown",
		})
	}()

	// 5. Build relay config with groups and proxies from BridgeConfig.
	relayCfg := b.buildRelayConfig()
	msgMap := bridge.NewMessageMap(10000)
	relay := bridge.NewRelay(localWS, msgMap, relayCfg, b.logger)

	// 6. Register proxy agents via common relay.
	if err := relay.EnsureProxies(ctx); err != nil {
		return fmt.Errorf("ensure proxies: %w", err)
	}

	// 7. Connect PeerTransport to remote daemon.
	var remote *PeerTransport
	if b.cfg.PeerRepoPath != "" {
		remote = NewLocalPeerTransport(b.cfg.PeerName, b.cfg.PeerRepoPath, b.cfg.PeerToken)
	} else {
		remote = NewPeerTransport(b.cfg.PeerName, b.cfg.PeerAddress, b.cfg.PeerToken)
	}
	if err := remote.Connect(ctx); err != nil {
		return fmt.Errorf("remote connect: %w", err)
	}
	defer remote.Close()

	b.logger.Printf("peer bridge connected (user: %s, peer: %s)", b.cfg.BridgeUserID, b.cfg.PeerName)

	// 8. Spawn goroutines for relay loops and heartbeat.
	errCh := make(chan error, 3)

	go func() {
		errCh <- b.runOutbound(ctx, localWS, remote, relay, msgMap)
	}()
	go func() {
		errCh <- b.runInbound(ctx, remote, relay)
	}()
	go func() {
		b.heartbeatLoop(ctx, localWS, sess.SessionID)
		errCh <- nil
	}()

	// 9. Wait for ctx cancellation or error.
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

// buildRelayConfig constructs a RelayConfig from BridgeConfig.
func (b *Bridge) buildRelayConfig() bridge.RelayConfig {
	var proxies []bridge.ProxyConfig
	for _, agentName := range b.cfg.RemoteAgents {
		proxies = append(proxies, bridge.ProxyConfig{
			Prefix:    b.cfg.ProxyPrefix,
			AgentName: agentName,
		})
	}
	return bridge.RelayConfig{
		BridgeUserID: b.cfg.BridgeUserID,
		Target:       b.cfg.Target,
		Proxies:      proxies,
	}
}

// runOutbound listens for local daemon notifications and forwards eligible
// outbound messages to the remote peer.
func (b *Bridge) runOutbound(
	ctx context.Context,
	localWS bridge.TransportBridge,
	remote *PeerTransport,
	relay *bridge.Relay,
	msgMap *bridge.MessageMap,
) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case notif, ok := <-localWS.Notifications():
			if !ok {
				return fmt.Errorf("local ws notifications channel closed")
			}
			if notif.Method != "notification.message" {
				continue
			}

			// Parse the notification to get message_id and author.
			var params struct {
				MessageID string `json:"message_id"`
				Author    struct {
					AgentID string `json:"agent_id"`
				} `json:"author"`
			}
			if err := json.Unmarshal(notif.Params, &params); err != nil {
				b.logger.Printf("outbound: unmarshal notification params: %v", err)
				continue
			}

			if params.MessageID == "" {
				continue
			}

			// Fetch full message from local daemon.
			msgResult, err := localWS.Call(ctx, "message.get", map[string]any{
				"message_id": params.MessageID,
			})
			if err != nil {
				b.logger.Printf("outbound: message.get %s: %v", params.MessageID, err)
				continue
			}

			var msg struct {
				Content    string   `json:"content"`
				Recipients []string `json:"recipients"`
				ReplyTo    string   `json:"reply_to"`
				Author     struct {
					AgentID string `json:"agent_id"`
				} `json:"author"`
			}
			if err := json.Unmarshal(msgResult, &msg); err != nil {
				b.logger.Printf("outbound: unmarshal message: %v", err)
				continue
			}

			authorAgentID := msg.Author.AgentID
			if authorAgentID == "" {
				authorAgentID = params.Author.AgentID
			}

			// Classify the route.
			route := relay.ClassifyOutbound(authorAgentID, msg.Recipients, msg.ReplyTo)

			switch route.Type {
			case bridge.RouteSkip:
				continue

			case bridge.RouteProxy:
				// Strip prefix from proxy agent name to get remote agent name.
				// route.ProxyAgent is "prefix:agent_name"
				agentName := route.ProxyAgent
				if idx := strings.Index(agentName, ":"); idx >= 0 {
					agentName = agentName[idx+1:]
				}

				sendResult, err := remote.Call(ctx, "message.send", map[string]any{
					"content":         msg.Content,
					"caller_agent_id": agentName,
					"mentions":        []string{agentName},
				})
				if err != nil {
					b.logger.Printf("outbound: remote message.send (proxy): %v", err)
					continue
				}
				// Store mapping for reply threading.
				var sendResp struct {
					MessageID string `json:"message_id"`
				}
				if json.Unmarshal(sendResult, &sendResp) == nil && sendResp.MessageID != "" {
					msgMap.Store(params.MessageID, sendResp.MessageID)
				}

			case bridge.RouteGroup, bridge.RouteReplyToGroup:
				mentions := msg.Recipients
				if len(mentions) == 0 && b.cfg.Target != "" {
					mentions = []string{strings.TrimPrefix(b.cfg.Target, "@")}
				}

				sendParams := map[string]any{
					"content":         msg.Content,
					"caller_agent_id": b.cfg.BridgeUserID,
				}
				if len(mentions) > 0 {
					sendParams["mentions"] = mentions
				}
				if route.Type == bridge.RouteReplyToGroup && route.ExternalKey != "" {
					sendParams["reply_to"] = route.ExternalKey
				}

				_, err := remote.Call(ctx, "message.send", sendParams)
				if err != nil {
					b.logger.Printf("outbound: remote message.send (group): %v", err)
				}
			}
		}
	}
}

// runInbound listens for remote peer notifications and relays them into the
// local daemon.
func (b *Bridge) runInbound(
	ctx context.Context,
	remote *PeerTransport,
	relay *bridge.Relay,
) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case notif, ok := <-remote.Notifications():
			if !ok {
				return fmt.Errorf("remote notifications channel closed")
			}
			if notif.Method != "notification.message" {
				continue
			}

			// Parse notification params.
			var params struct {
				MessageID string `json:"message_id"`
				Author    struct {
					AgentID string `json:"agent_id"`
					Name    string `json:"name"`
				} `json:"author"`
			}
			if err := json.Unmarshal(notif.Params, &params); err != nil {
				b.logger.Printf("inbound: unmarshal notification params: %v", err)
				continue
			}
			if params.MessageID == "" {
				continue
			}

			// Fetch full message from remote daemon.
			msgResult, err := remote.Call(ctx, "message.get", map[string]any{
				"message_id": params.MessageID,
			})
			if err != nil {
				b.logger.Printf("inbound: remote message.get %s: %v", params.MessageID, err)
				continue
			}

			var msg struct {
				Content string `json:"content"`
				ReplyTo string `json:"reply_to"`
				Author  struct {
					AgentID string `json:"agent_id"`
					Name    string `json:"name"`
				} `json:"author"`
			}
			if err := json.Unmarshal(msgResult, &msg); err != nil {
				b.logger.Printf("inbound: unmarshal message: %v", err)
				continue
			}

			senderID := msg.Author.AgentID
			if senderID == "" {
				senderID = params.Author.AgentID
			}
			senderName := msg.Author.Name
			if senderName == "" {
				senderName = params.Author.AgentID
			}

			inboundMsg := bridge.InboundMessage{
				Content:     msg.Content,
				SenderID:    senderID,
				SenderName:  senderName,
				ExternalKey: params.MessageID,
				ReplyToKey:  msg.ReplyTo,
				Structured: map[string]any{
					"source":     "peer",
					"peer_name":  b.cfg.PeerName,
					"message_id": params.MessageID,
					"author_id":  senderID,
				},
			}

			if err := relay.RelayInbound(ctx, inboundMsg); err != nil {
				b.logger.Printf("inbound: relay error: %v", err)
			}
		}
	}
}

// heartbeatLoop sends session.heartbeat every 30 seconds until ctx is cancelled.
func (b *Bridge) heartbeatLoop(ctx context.Context, localWS bridge.TransportBridge, sessionID string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = localWS.Call(ctx, "session.heartbeat", map[string]any{
				"session_id": sessionID,
			})
		}
	}
}
