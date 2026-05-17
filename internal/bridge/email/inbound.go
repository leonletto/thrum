package email

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
)

// ActionKind identifies the outcome of ProcessMessage.
type ActionKind int

const (
	ActionDropped ActionKind = iota
	ActionRouted
	ActionPending
)

// ProcessAction is the outcome of ProcessMessage. Callers use Kind to
// branch; the other fields are populated per-kind.
type ProcessAction struct {
	Kind   ActionKind
	Reason string // populated for Dropped and Pending

	// Routed-specific fields:
	RoutedKind string // "protocol" | "message"
	RoutedTo   string // agent name (message) or peer daemon-id (protocol)
}

// MessageDispatcher abstracts the daemon's message.send RPC and the
// IMAP server-side post-processing operations so bridge and tests inject
// different implementations without touching inbound routing logic.
type MessageDispatcher interface {
	SendMessage(ctx context.Context, fromAgent, toAgent, body, replyTo string) error
	MarkSeen(ctx context.Context, uid imap.UID) error
	MoveToFolder(ctx context.Context, uid imap.UID, folder string) error
}

// MeshHandler abstracts the mesh coordination layer (D-B1.13). The bridge
// orchestrator wires the real mesh.go; tests use recording stubs.
type MeshHandler interface {
	HandleProtocol(ctx context.Context, verb string, headers map[string]string, body []byte) error
	HandleStrangerPair(ctx context.Context, headers map[string]string, body []byte) (ProcessAction, error)
}

// InboundConfig holds all routing policy for an Inbound instance.
type InboundConfig struct {
	MyDaemonID       string
	HopCeiling       int    // X-Thrum-Hop-Count > this → drop (L2)
	UnknownRecipient string // "drop" is the only implemented policy in D-B1
	MoveAfterProcess bool   // true: MoveToFolder; false: MarkSeen
	MoveFolder       string // folder when MoveAfterProcess=true
	KnownPeers       map[string]bool // daemon-id → known
	LocalAgents      map[string]bool // local agent names
}

// Inbound implements the 11-step inbound routing pipeline from design-spec §9.
// All injected dependencies are required; nil values will panic at runtime.
type Inbound struct {
	cfg        InboundConfig
	dedup      *Dedup
	limiter    *Limiter
	msgmap     *MsgMap
	dispatcher MessageDispatcher
	mesh       MeshHandler

	// unknownRecipientCount surfaces via email.status RPC so operators
	// can spot mis-routed mailbox traffic without scraping logs. Bumped
	// each time step-10 drops a message because to_agent is unmapped.
	unknownRecipientCount atomic.Int64
}

// UnknownRecipientCount returns the running count of inbound messages
// dropped because the X-Thrum-To-Agent header named an unknown local
// agent. Exposed for email.status RPC reporting.
func (in *Inbound) UnknownRecipientCount() int64 {
	return in.unknownRecipientCount.Load()
}

// NewInbound constructs a ready-to-use Inbound router.
func NewInbound(cfg InboundConfig, dedup *Dedup, limiter *Limiter, msgmap *MsgMap, dispatcher MessageDispatcher, mesh MeshHandler) *Inbound {
	return &Inbound{
		cfg:        cfg,
		dedup:      dedup,
		limiter:    limiter,
		msgmap:     msgmap,
		dispatcher: dispatcher,
		mesh:       mesh,
	}
}

// ProcessMessage runs the spec §9 11-step pipeline on a raw MIME message.
// uid is the IMAP UID used for MarkSeen/MoveToFolder on success. On any
// mid-flow error the message is NOT marked seen so the next IDLE/poll cycle
// retries it; the dedup table prevents actual reprocessing.
func (in *Inbound) ProcessMessage(ctx context.Context, raw []byte, uid imap.UID) (ProcessAction, error) {
	// Step 1-2: parse.
	msg, err := in.step2Parse(raw)
	if err != nil {
		return drop("parse_error"), fmt.Errorf("inbound parse: %w", err)
	}

	// Step 3 [L4 dedup]: check-and-record atomically; duplicate → drop without error.
	action, stop, err := in.step3Dedup(ctx, msg)
	if err != nil {
		return drop("dedup_error"), fmt.Errorf("inbound dedup: %w", err)
	}
	if stop {
		return action, nil
	}

	// Step 4: X-Thrum-To-Daemon must be us.
	if action, stop := in.step4ToDaemon(msg); stop {
		return action, nil
	}

	// Step 5 [L1 self-echo]: X-Thrum-From-Daemon == my daemon-id → drop.
	if action, stop := in.step5SelfEcho(msg); stop {
		return action, nil
	}

	// Step 6 [L2 hop-count]: hop count ceiling check.
	if action, stop := in.step6HopCount(msg); stop {
		return action, nil
	}

	// Step 7: sender-known check.
	action, stop, err = in.step7SenderKnown(ctx, msg)
	if err != nil {
		return drop("sender_check_error"), fmt.Errorf("inbound sender check: %w", err)
	}
	if stop {
		return action, nil
	}

	// Step 8 [L3 rate-limit]: per-peer and global ceiling.
	peerKey := msg.Headers["X-Thrum-From-Daemon"]
	action, stop, err = in.step8RateLimit(ctx, peerKey, msg)
	if err != nil {
		return drop("rate_limit_error"), fmt.Errorf("inbound rate-limit: %w", err)
	}
	if stop {
		return action, nil
	}

	// Steps 9-11: kind-based routing + dispatch.
	action, err = in.step9Route(ctx, msg)
	if err != nil {
		// Do NOT mark seen — let the retry cycle pick it up.
		return drop("route_error"), fmt.Errorf("inbound route: %w", err)
	}

	// Success: mark or move on the server.
	in.postProcessSuccess(ctx, uid)
	return action, nil
}

// --- step functions ---

// step2Parse parses the raw MIME bytes. Step 1 (IMAP fetch) is the caller's
// responsibility; we receive already-fetched bytes.
func (in *Inbound) step2Parse(raw []byte) (*ParsedMessage, error) {
	return ParseInbound(raw)
}

// step3Dedup runs the L4 dedup check. Returns (action, stop=true) on a
// duplicate hit so the caller short-circuits.
func (in *Inbound) step3Dedup(ctx context.Context, msg *ParsedMessage) (ProcessAction, bool, error) {
	msgID := msg.Headers["Message-Id"]
	if msgID == "" {
		// No Message-Id — treat as untrackable; drop to avoid dedup table noise.
		log.Printf("[email/inbound] L4 drop: missing Message-Id")
		return drop("missing_message_id"), true, nil
	}
	fromDaemon := msg.Headers["X-Thrum-From-Daemon"]
	alreadySeen, err := in.dedup.SeenOrInsert(ctx, msgID, fromDaemon, "", time.Now().UTC())
	if err != nil {
		return ProcessAction{}, false, err
	}
	if alreadySeen {
		log.Printf("[email/inbound] L4 drop: dedup hit message_id=%s", msgID)
		return drop("dedup_hit"), true, nil
	}
	return ProcessAction{}, false, nil
}

// step4ToDaemon verifies X-Thrum-To-Daemon matches our daemon ID.
func (in *Inbound) step4ToDaemon(msg *ParsedMessage) (ProcessAction, bool) {
	toDaemon := msg.Headers["X-Thrum-To-Daemon"]
	if toDaemon != in.cfg.MyDaemonID {
		log.Printf("[email/inbound] drop: not_for_me to_daemon=%q my_daemon=%q", toDaemon, in.cfg.MyDaemonID)
		return drop("not_for_me"), true
	}
	return ProcessAction{}, false
}

// step5SelfEcho is the L1 loop-protection layer: discard messages we sent.
func (in *Inbound) step5SelfEcho(msg *ParsedMessage) (ProcessAction, bool) {
	fromDaemon := msg.Headers["X-Thrum-From-Daemon"]
	if fromDaemon == in.cfg.MyDaemonID {
		log.Printf("[email/inbound] L1 drop: self-echo from_daemon=%s", fromDaemon)
		return drop("self_echo"), true
	}
	return ProcessAction{}, false
}

// step6HopCount is the L2 loop-protection layer: discard messages that have
// exceeded the hop ceiling. Absent or unparseable hop counts are treated as 0.
func (in *Inbound) step6HopCount(msg *ParsedMessage) (ProcessAction, bool) {
	hopStr := msg.Headers["X-Thrum-Hop-Count"]
	hopCount := 0
	if hopStr != "" {
		if n, err := strconv.Atoi(hopStr); err == nil {
			hopCount = n
		}
	}
	if hopCount > in.cfg.HopCeiling {
		log.Printf("[email/inbound] L2 drop: hop_count=%d ceiling=%d", hopCount, in.cfg.HopCeiling)
		return drop("hop_ceiling"), true
	}
	return ProcessAction{}, false
}

// step7SenderKnown checks whether the sender daemon-id is a known peer. Unknown
// senders with a peer.pair protocol verb are forwarded to the mesh for operator
// confirmation; all other unknown senders are dropped.
func (in *Inbound) step7SenderKnown(ctx context.Context, msg *ParsedMessage) (ProcessAction, bool, error) {
	fromDaemon := msg.Headers["X-Thrum-From-Daemon"]
	if in.cfg.KnownPeers[fromDaemon] {
		// Known peer — proceed.
		return ProcessAction{}, false, nil
	}

	// Unknown sender.
	kind := msg.Kind
	verb := msg.Verb
	if kind == "protocol" && verb == "peer.pair" {
		log.Printf("[email/inbound] unknown sender peer.pair: from_daemon=%s → pending operator confirm", fromDaemon)
		action, err := in.mesh.HandleStrangerPair(ctx, msg.Headers, []byte(msg.Body))
		if err != nil {
			return ProcessAction{}, false, fmt.Errorf("stranger pair: %w", err)
		}
		return action, true, nil
	}

	log.Printf("[email/inbound] drop: unknown_sender from_daemon=%s kind=%s", fromDaemon, kind)
	return drop("unknown_sender"), true, nil
}

// step8RateLimit runs the L3 per-peer and global flood checks.
func (in *Inbound) step8RateLimit(ctx context.Context, peerKey string, _ *ParsedMessage) (ProcessAction, bool, error) {
	allowed, paused, err := in.limiter.IncrementInbound(ctx, peerKey)
	if err != nil {
		return ProcessAction{}, false, err
	}
	if !allowed {
		reason := "global_flood"
		if paused {
			reason = "rate_paused"
		}
		log.Printf("[email/inbound] L3 drop: %s peer=%s", reason, peerKey)
		return drop(reason), true, nil
	}
	return ProcessAction{}, false, nil
}

// step9Route dispatches the message based on kind header.
func (in *Inbound) step9Route(ctx context.Context, msg *ParsedMessage) (ProcessAction, error) {
	switch msg.Kind {
	case "protocol":
		return in.routeProtocol(ctx, msg)
	case "message":
		return in.routeMessage(ctx, msg)
	default:
		log.Printf("[email/inbound] drop: unknown_kind kind=%q", msg.Kind)
		return drop("unknown_kind"), nil
	}
}

// routeProtocol hands a protocol message to the mesh handler.
func (in *Inbound) routeProtocol(ctx context.Context, msg *ParsedMessage) (ProcessAction, error) {
	verb := msg.Verb
	fromDaemon := msg.Headers["X-Thrum-From-Daemon"]
	if err := in.mesh.HandleProtocol(ctx, verb, msg.Headers, []byte(msg.Body)); err != nil {
		return ProcessAction{}, fmt.Errorf("mesh protocol: %w", err)
	}
	return ProcessAction{
		Kind:       ActionRouted,
		RoutedKind: "protocol",
		RoutedTo:   fromDaemon,
	}, nil
}

// routeMessage dispatches a kind=message payload to the message.send RPC.
func (in *Inbound) routeMessage(ctx context.Context, msg *ParsedMessage) (ProcessAction, error) {
	toAgent := msg.Headers["X-Thrum-To-Agent"]
	if !in.cfg.LocalAgents[toAgent] {
		// Unknown-recipient policy: D-B1 ships "drop" only. Forward/bounce
		// policies are reserved for v0.11.x/v0.12; log a warning if configured.
		pol := in.cfg.UnknownRecipient
		if pol != "" && pol != "drop" {
			log.Printf("[email/inbound] warning: unknown_recipient policy %q not implemented in D-B1; dropping", pol)
		}
		in.unknownRecipientCount.Add(1)
		log.Printf("[email/inbound] drop: unknown_recipient to_agent=%q", toAgent)
		return drop("unknown_recipient"), nil
	}

	fromAgent := msg.Headers["X-Thrum-From-Agent"]
	body := msg.Body

	// Resolve In-Reply-To for threading.
	replyTo := ""
	if inReplyTo := msg.Headers["In-Reply-To"]; inReplyTo != "" {
		if thrumID, ok := in.msgmap.Lookup(inReplyTo); ok {
			replyTo = thrumID
		}
	}

	if err := in.dispatcher.SendMessage(ctx, fromAgent, toAgent, body, replyTo); err != nil {
		return ProcessAction{}, fmt.Errorf("dispatch message: %w", err)
	}

	return ProcessAction{
		Kind:       ActionRouted,
		RoutedKind: "message",
		RoutedTo:   toAgent,
	}, nil
}

// postProcessSuccess marks or moves the message on the IMAP server after a
// successful route. MarkSeen failures are logged but not propagated — the
// message was successfully routed; a re-delivery will be deduplicated.
func (in *Inbound) postProcessSuccess(ctx context.Context, uid imap.UID) {
	if in.cfg.MoveAfterProcess {
		if err := in.dispatcher.MoveToFolder(ctx, uid, in.cfg.MoveFolder); err != nil {
			log.Printf("[email/inbound] MoveToFolder uid=%d folder=%q: %v (message was routed; dedup prevents reprocess)", uid, in.cfg.MoveFolder, err)
		}
		return
	}
	if err := in.dispatcher.MarkSeen(ctx, uid); err != nil {
		log.Printf("[email/inbound] MarkSeen uid=%d: %v (message was routed; dedup prevents reprocess)", uid, err)
	}
}

// drop is a convenience constructor for ActionDropped outcomes.
func drop(reason string) ProcessAction {
	return ProcessAction{Kind: ActionDropped, Reason: reason}
}
