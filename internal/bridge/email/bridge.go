package email

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	goImap "github.com/emersion/go-imap/v2"
	"github.com/leonletto/thrum/internal/bridge"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// BridgeStatus is the snapshot returned by Status(). All fields are safe to
// read concurrently — the values are copied under the bridge's status lock.
type BridgeStatus struct {
	Running          bool
	LastError        string
	StartedAt        time.Time
	HeartbeatCount   int64
	InboundProcessed int64
	OutboundEnqueued int64
}

// WSConn is the minimal interface over bridge.WSClient required by Bridge.
// Defined here so tests can inject a lightweight mock without standing up a
// real WebSocket server.
type WSConn interface {
	Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error)
	Notifications() <-chan bridge.Notification
	Close() error
}

// Bridge orchestrates all D-B1 email sub-components. It mirrors the structure
// of the Telegram bridge: outer retry loop, inner run with panic recovery,
// injectable sub-components, and atomic status counters.
type Bridge struct {
	cfg     config.EmailConfig
	secrets *config.EmailSecrets // nil when bridge is disabled
	wsPort  string
	logger  *log.Logger
	db      atomic.Pointer[sql.DB] // injected via SetDB; nil → DB-backed sub-components disabled

	mu       sync.Mutex
	cancelFn context.CancelFunc // cancels the current inner run; swapped on Restart

	running atomic.Bool

	// Exposed counters — updated atomically by sub-goroutines via methods on Bridge.
	heartbeatCount   atomic.Int64
	inboundProcessed atomic.Int64
	outboundEnqueued atomic.Int64

	startedAt atomic.Value // stores time.Time; zero until first successful run start
	lastError  atomic.Value // stores string

	// Test hooks — replaced by tests to compress wall-clock waits.
	HeartbeatInterval time.Duration // default 30s
	RetryBackoff      time.Duration // default 5s

	// dialFn replaces bridge.Dial in tests. Nil means production path.
	dialFn func(ctx context.Context, wsURL string) (WSConn, error)

	// stateDirFn / configDirFn resolve paths for state sidecar files and
	// config.json. Overridden in tests to use t.TempDir() so parallel tests
	// don't share the global os.TempDir() namespace.
	stateDirFn  func(cfg config.EmailConfig) string
	configDirFn func(cfg config.EmailConfig) string
}

// New constructs a Bridge. secrets may be nil when cfg.Enabled is false.
func New(cfg config.EmailConfig, secrets *config.EmailSecrets, wsPort string) *Bridge {
	return &Bridge{
		cfg:               cfg,
		secrets:           secrets,
		wsPort:            wsPort,
		logger:            log.New(os.Stderr, "[email/bridge] ", log.LstdFlags),
		HeartbeatInterval: 30 * time.Second,
		RetryBackoff:      5 * time.Second,
	}
}

// SetDB wires the shared *sql.DB. Mirror of telegram bridge's SetDB.
// Safe to call before or concurrently with Run; atomic store is race-free.
func (b *Bridge) SetDB(db *sql.DB) {
	b.db.Store(db)
}

// Status returns a snapshot of the current bridge runtime state.
func (b *Bridge) Status() BridgeStatus {
	s := BridgeStatus{
		Running:          b.running.Load(),
		HeartbeatCount:   b.heartbeatCount.Load(),
		InboundProcessed: b.inboundProcessed.Load(),
		OutboundEnqueued: b.outboundEnqueued.Load(),
	}
	if v := b.startedAt.Load(); v != nil {
		if t, ok := v.(time.Time); ok {
			s.StartedAt = t
		}
	}
	if v := b.lastError.Load(); v != nil {
		if s2, ok := v.(string); ok {
			s.LastError = s2
		}
	}
	return s
}

// Running reports whether the bridge is currently executing its inner run loop.
func (b *Bridge) Running() bool {
	return b.running.Load()
}

// Restart cancels the current inner run cycle and adopts the new config.
// The outer retry loop picks up the new config on the next iteration.
func (b *Bridge) Restart(newCfg config.EmailConfig, newSecrets *config.EmailSecrets) {
	b.mu.Lock()
	b.cfg = newCfg
	b.secrets = newSecrets
	cancel := b.cancelFn
	b.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// Run is the long-running entry point. Retries on inner-run failure with
// b.RetryBackoff (default 5s); recovers from panics. Returns when ctx is done.
//
// Designed to be called as: go bridge.Run(ctx).
func (b *Bridge) Run(ctx context.Context) {
	b.logger.Println("starting")
	defer b.logger.Println("stopped")

	for {
		runCtx, runCancel := context.WithCancel(ctx)
		b.mu.Lock()
		b.cancelFn = runCancel
		b.mu.Unlock()

		err := b.runWithRecover(runCtx)
		runCancel()

		if ctx.Err() != nil {
			return // clean shutdown
		}

		if err != nil {
			b.lastError.Store(err.Error())
			b.logger.Printf("inner run error: %v; retrying in %s", err, b.RetryBackoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(b.RetryBackoff):
		}
	}
}

// PollOnce performs a single IMAP fetch + process cycle without starting the
// full goroutine inventory. Satisfies the A-B1 RegisterInternal handler-shape
// contract from design-spec §13.
//
// Safe to call without Run() running. Returns promptly on ctx.Done.
func (b *Bridge) PollOnce(ctx context.Context) error {
	db := b.db.Load()
	if db == nil {
		return fmt.Errorf("email PollOnce: db not wired")
	}

	b.mu.Lock()
	cfg := b.cfg
	secrets := b.secrets
	b.mu.Unlock()

	if secrets == nil {
		return fmt.Errorf("email PollOnce: secrets not available")
	}

	imapCfg := buildIMAPConfig(cfg, secrets)
	imap := NewIMAPClient(imapCfg)
	if err := imap.Connect(ctx); err != nil {
		return fmt.Errorf("email PollOnce: imap connect: %w", err)
	}
	defer func() { _ = imap.Close() }()

	return imap.PollOnce(ctx)
}

// runWithRecover wraps run() so panics become errors that the outer retry
// loop handles rather than crashing the daemon.
func (b *Bridge) runWithRecover(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Printf("PANIC (recovered): %v", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return b.run(ctx)
}

// run is the inner lifecycle: dial WS, wire all sub-components, replay WAL,
// mark running, spawn goroutines, then block until ctx is done.
func (b *Bridge) run(ctx context.Context) error {
	b.mu.Lock()
	cfg := b.cfg
	secrets := b.secrets
	b.mu.Unlock()

	db := b.db.Load()
	if db == nil {
		return fmt.Errorf("db not wired (call SetDB before Run)")
	}

	// --- 1. Connect to daemon WebSocket. ---
	wsURL := fmt.Sprintf("ws://127.0.0.1:%s/ws", b.wsPort)
	var ws WSConn
	var err error
	if b.dialFn != nil {
		ws, err = b.dialFn(ctx, wsURL)
	} else {
		ws, err = dialEmailWS(ctx, wsURL)
	}
	if err != nil {
		return fmt.Errorf("ws connect: %w", err)
	}
	defer func() { _ = ws.Close() }()

	// --- 2. Register + start session. ---
	userID := "user:" + cfg.Username
	_, err = ws.Call(ctx, "user.register", map[string]any{
		"username": cfg.Username,
		"display":  "Email Bridge (" + cfg.Username + ")",
	})
	if err != nil {
		return fmt.Errorf("user.register: %w", err)
	}

	sessResult, err := ws.Call(ctx, "session.start", map[string]any{
		"agent_id": userID,
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
	defer func() {
		endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ws.Call(endCtx, "session.end", map[string]any{
			"session_id": sess.SessionID,
			"reason":     "bridge_shutdown",
		})
	}()

	// --- 3. Build shared sub-components. ---
	notifier := &wsCoordinatorNotifier{ws: ws}

	dedup := NewDedup(db)
	limiter := NewLimiter(db, buildLimiterConfig(cfg), notifier)
	if err := limiter.Init(ctx); err != nil {
		b.logger.Printf("limiter Init warning: %v (continuing with empty in-memory state)", err)
	}

	stateDirPath := b.stateDirPath(cfg)
	msgmap, err := NewMsgMap(filepath.Join(stateDirPath, "email-msgmap.jsonl"))
	if err != nil {
		return fmt.Errorf("msgmap open: %w", err)
	}
	defer func() { _ = msgmap.Close() }()

	walPath := filepath.Join(stateDirPath, "email-mesh-wal.jsonl")
	wal, err := state.NewPendingMeshUpdatesLog(walPath)
	if err != nil {
		return fmt.Errorf("mesh WAL open: %w", err)
	}
	defer func() { _ = wal.Close() }()

	configPath := filepath.Join(b.configDirPath(cfg), "config.json")
	knownPeers, peerMap := buildPeerMaps(cfg)

	meshCfg := MeshConfig{
		MyDaemonID:              cfg.DaemonHandle, // handle serves as stable mesh identity in v0.11
		MyDaemonShort:           shortID(cfg.DaemonHandle),
		VouchAcceptance:         cfg.Mesh.VouchAcceptance,
		AllowTransitiveVouching: cfg.Mesh.AllowTransitiveVouching,
		HopCountCeiling:         cfg.Mesh.HopCountCeiling,
		RevocationPropagation:   cfg.Mesh.RevocationPropagation,
		ConfigPath:              configPath,
	}
	meshHandler := NewMeshHandler(meshCfg, wal, notifier, nil)

	// --- 4. WAL replay — before goroutines start, idempotent re-apply. ---
	b.replayWAL(ctx, wal, meshHandler)

	queue := NewQueue(db)

	var smtpCl SMTPSubmitter
	if secrets != nil {
		smtpCfg := SMTPConfig{
			Host:        cfg.SMTP.Host,
			Port:        cfg.SMTP.Port,
			UseStartTLS: cfg.SMTP.UseStartTLS,
			Username:    cfg.Username,
			Password:    secrets.SMTPPassword,
		}
		smtpClient, err := NewSMTPClient(smtpCfg)
		if err != nil {
			return fmt.Errorf("smtp client: %w", err)
		}
		smtpCl = smtpClient
	}

	queueCfg := buildQueueConfig(cfg)
	worker := NewWorker(queue, smtpCl, notifier, queueCfg)

	outboundCfg := OutboundConfig{
		MyDaemonID:            cfg.DaemonHandle,
		MyDaemonShort:         shortID(cfg.DaemonHandle),
		Host:                  cfg.SMTP.Host,
		FromAddress:           cfg.FromAddress,
		FromDisplayNameFormat: cfg.FromDisplayNameFormat,
		DaemonHandle:          cfg.DaemonHandle,
		TargetUser:            cfg.TargetUser,
		TargetEmail:           cfg.TargetEmail,
		DefaultMention:        cfg.DefaultMention,
		EmbedShortID:          cfg.EmbedShortID,
		KnownPeers:            peerMap,
	}
	subAdapter := &wsNotifSubscriber{ws: ws}
	outbound := NewOutbound(outboundCfg, subAdapter, msgmap, limiter, queue)

	inboundCfg := InboundConfig{
		MyDaemonID:       cfg.DaemonHandle,
		HopCeiling:       cfg.Mesh.HopCountCeiling,
		UnknownRecipient: cfg.UnknownRecipient,
		KnownPeers:       knownPeers,
	}
	if inboundCfg.HopCeiling == 0 {
		inboundCfg.HopCeiling = 5
	}

	var imapClient *IMAPClient
	if secrets != nil {
		imapCfg := buildIMAPConfig(cfg, secrets)
		imapClient = NewIMAPClient(imapCfg)
		if err := imapClient.Connect(ctx); err != nil {
			return fmt.Errorf("imap connect: %w", err)
		}
		defer func() { _ = imapClient.Close() }()
	}

	dispatcher := &wsMessageDispatcher{ws: ws, imap: imapClient}
	inbound := NewInbound(inboundCfg, dedup, limiter, msgmap, dispatcher, meshHandler)

	// --- 5. Mark running. ---
	b.running.Store(true)
	b.startedAt.Store(time.Now())
	b.lastError.Store("")
	defer b.running.Store(false)

	b.logger.Printf("running (handle=%s, imap=%s, smtp=%s)", cfg.DaemonHandle, cfg.IMAP.Host, cfg.SMTP.Host)

	// --- 6. Spawn goroutines via safeGo (panic-recovering wrapper). ---
	var wg sync.WaitGroup

	if imapClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.safeGo("imap.IDLEloop", func() {
				if err := imapClient.IDLEloop(ctx); err != nil && ctx.Err() == nil {
					b.logger.Printf("IDLEloop exited: %v", err)
				}
			})
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		b.safeGo("outbound.Run", func() {
			if err := outbound.Run(ctx); err != nil && ctx.Err() == nil {
				b.logger.Printf("outbound.Run exited: %v", err)
			}
		})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		b.safeGo("queue.Worker.Run", func() {
			if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
				b.logger.Printf("queue worker exited: %v", err)
			}
		})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		b.safeGo("dedup.Sweeper", func() {
			b.dedupSweeper(ctx, dedup)
		})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		b.safeGo("ratelimit.WindowRoller", func() {
			if err := limiter.WindowRoller(ctx); err != nil && ctx.Err() == nil {
				b.logger.Printf("WindowRoller exited: %v", err)
			}
		})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		b.safeGo("heartbeat", func() {
			b.heartbeatLoop(ctx, ws, sess.SessionID)
		})
	}()

	// Inbound pump: IMAP poll triggers ProcessMessage on each raw message.
	// When no IMAP client is available (disabled / no secrets) this is a no-op.
	if imapClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.safeGo("inbound.poll", func() {
				b.inboundPumpLoop(ctx, imapClient, inbound)
			})
		}()
	}

	<-ctx.Done()
	wg.Wait()
	return nil
}

// safeGo runs fn with panic recovery. Panics are logged; the goroutine exits
// cleanly so the WaitGroup unblocks and the outer retry loop can restart.
func (b *Bridge) safeGo(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Printf("PANIC in %s (recovered): %v", name, r)
		}
	}()
	fn()
}

// heartbeatLoop sends a session.heartbeat RPC at b.HeartbeatInterval (default 30s).
func (b *Bridge) heartbeatLoop(ctx context.Context, ws WSConn, sessionID string) {
	ticker := time.NewTicker(b.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = ws.Call(ctx, "session.heartbeat", map[string]any{
				"session_id": sessionID,
			})
			b.heartbeatCount.Add(1)
		}
	}
}

// dedupSweeper runs dedup.Sweep once every 24h, dropping rows older than 30d.
// A-B1 RegisterInternal adoption is a follow-up; D-B1 ships a bare ticker.
func (b *Bridge) dedupSweeper(ctx context.Context, d *Dedup) {
	ticker := time.NewTicker(DefaultDedupSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-DefaultDedupTTL)
			if n, err := d.Sweep(ctx, cutoff); err != nil && ctx.Err() == nil {
				b.logger.Printf("dedup sweep error: %v", err)
			} else if n > 0 {
				b.logger.Printf("dedup sweep: deleted %d stale rows", n)
			}
		}
	}
}

// inboundPumpLoop re-fetches from IMAP at cfg.PollInterval cadence so that
// the goroutine acts as an additional feed alongside IDLEloop. This is
// intentionally kept simple: fetch → ProcessMessage per uid.
//
// The 24-hour lookback window in PollOnce/Fetch means freshly IDLE-pushed
// messages are also caught on the next poll if IDLEloop delivered them
// already — the dedup table makes the second arrival a no-op.
func (b *Bridge) inboundPumpLoop(ctx context.Context, imap *IMAPClient, inbound *Inbound) {
	b.mu.Lock()
	interval := time.Duration(b.cfg.PollIntervalSeconds) * time.Second
	b.mu.Unlock()
	if interval <= 0 {
		interval = 60 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.pollInbound(ctx, imap, inbound)
		}
	}
}

// pollInbound fetches messages from the IMAP server and runs them through
// the inbound pipeline. Errors on individual messages are logged and skipped.
func (b *Bridge) pollInbound(ctx context.Context, imap *IMAPClient, inbound *Inbound) {
	msgs, err := imap.Fetch(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		if ctx.Err() == nil {
			b.logger.Printf("inbound poll fetch: %v", err)
		}
		return
	}
	for _, msg := range msgs {
		if ctx.Err() != nil {
			return
		}
		action, err := inbound.ProcessMessage(ctx, msg.Bytes, msg.UID)
		if err != nil && ctx.Err() == nil {
			b.logger.Printf("inbound process uid=%d: %v", msg.UID, err)
			continue
		}
		if action.Kind == ActionRouted {
			b.inboundProcessed.Add(1)
		}
	}
}

// replayWAL replays any pending (uncommitted) WAL entries through the mesh
// handler's idempotent HandleProtocol path. Called once at run-start, before
// goroutines spawn, so no concurrent access to the mesh state.
func (b *Bridge) replayWAL(ctx context.Context, wal *state.PendingMeshUpdatesLog, mesh *MeshHandlerImpl) {
	pending, err := wal.Pending()
	if err != nil {
		b.logger.Printf("WAL replay: read pending error: %v (skipping replay)", err)
		return
	}
	for _, p := range pending {
		if ctx.Err() != nil {
			return
		}
		if err := mesh.HandleProtocol(ctx, p.Verb, nil, p.Payload); err != nil {
			b.logger.Printf("WAL replay: HandleProtocol verb=%s update=%s error: %v", p.Verb, p.UpdateID, err)
			continue
		}
		// Emit the committed marker so the entry won't replay again.
		if err := wal.AppendCommitted(p.UpdateID); err != nil {
			b.logger.Printf("WAL replay: AppendCommitted update=%s error: %v", p.UpdateID, err)
		}
	}
}

// --- dial helper (replaced in tests via Bridge.dialFn) ---

// dialEmailWS opens the loopback WebSocket to the daemon.
func dialEmailWS(ctx context.Context, wsURL string) (WSConn, error) {
	client := bridge.NewWSClient(wsURL, bridge.WithAddressValidator(bridge.LoopbackValidator))
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

// --- adapters ---

// wsCoordinatorNotifier implements CoordinatorNotifier by calling message.send
// on the WebSocket. All three components (Limiter, Worker, MeshHandler) share
// the same notifier instance so alerts converge on a single WS call path.
type wsCoordinatorNotifier struct {
	ws WSConn
}

func (n *wsCoordinatorNotifier) Notify(ctx context.Context, message string) error {
	_, err := n.ws.Call(ctx, "message.send", map[string]any{
		"to":   "@coordinator_main",
		"body": message,
	})
	return err
}

// wsNotifSubscriber implements WSSubscriber for Outbound by filtering
// bridge.Notification frames from the daemon's WebSocket.
type wsNotifSubscriber struct {
	ws WSConn
}

// Subscribe returns a channel of MessageNotification decoded from the WS
// notification stream. Only frames matching method are forwarded.
func (s *wsNotifSubscriber) Subscribe(ctx context.Context, method string) (<-chan MessageNotification, error) {
	out := make(chan MessageNotification, 64)
	go func() {
		defer close(out)
		notifCh := s.ws.Notifications()
		for {
			select {
			case <-ctx.Done():
				return
			case n, ok := <-notifCh:
				if !ok {
					return
				}
				if n.Method != method {
					continue
				}
				var mn MessageNotification
				if err := json.Unmarshal(n.Params, &mn); err != nil {
					continue
				}
				select {
				case out <- mn:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// wsMessageDispatcher implements MessageDispatcher by forwarding message.send
// calls via the WS client and delegating IMAP operations to IMAPClient.
type wsMessageDispatcher struct {
	ws   WSConn
	imap *IMAPClient // nil when IMAP is unavailable
}

func (d *wsMessageDispatcher) SendMessage(ctx context.Context, fromAgent, toAgent, body, replyTo string) error {
	params := map[string]any{
		"from": fromAgent,
		"to":   toAgent,
		"body": body,
	}
	if replyTo != "" {
		params["reply_to"] = replyTo
	}
	_, err := d.ws.Call(ctx, "message.send", params)
	return err
}

func (d *wsMessageDispatcher) MarkSeen(ctx context.Context, uid goImap.UID) error {
	if d.imap == nil {
		return nil
	}
	return d.imap.MarkSeen(ctx, uid)
}

func (d *wsMessageDispatcher) MoveToFolder(_ context.Context, _ goImap.UID, _ string) error {
	// MoveToFolder is a v0.11.x follow-up; D-B1 ships MarkSeen-only post-processing.
	return nil
}

// --- helper: build sub-component configs from EmailConfig ---

func buildIMAPConfig(cfg config.EmailConfig, secrets *config.EmailSecrets) IMAPConfig {
	pollInterval := time.Duration(cfg.PollIntervalSeconds) * time.Second
	if pollInterval <= 0 {
		pollInterval = 60 * time.Second
	}
	return IMAPConfig{
		Host:         cfg.IMAP.Host,
		Port:         cfg.IMAP.Port,
		UseStartTLS:  cfg.IMAP.UseStartTLS,
		UseIDLE:      cfg.IMAP.UseIDLE,
		Username:     cfg.Username,
		Password:     secrets.IMAPPassword,
		PollInterval: pollInterval,
	}
}

func buildLimiterConfig(cfg config.EmailConfig) LimiterConfig {
	lc := LimiterConfig{
		InboundPerPeerPerHour:  cfg.RateLimits.InboundPerPeerPerHour,
		OutboundPerPeerPerHour: cfg.RateLimits.OutboundPerPeerPerHour,
		GlobalInboundPerMinute: cfg.RateLimits.GlobalInboundPerMinute,
	}
	if lc.InboundPerPeerPerHour == 0 {
		lc.InboundPerPeerPerHour = 200
	}
	if lc.OutboundPerPeerPerHour == 0 {
		lc.OutboundPerPeerPerHour = 200
	}
	if lc.GlobalInboundPerMinute == 0 {
		lc.GlobalInboundPerMinute = 60
	}
	return lc
}

func buildQueueConfig(cfg config.EmailConfig) QueueConfig {
	qc := QueueConfig{
		MaxAttempts: cfg.Queue.MaxAttempts,
	}
	if cfg.Queue.BackoffInitialSeconds > 0 {
		qc.BackoffInitial = time.Duration(cfg.Queue.BackoffInitialSeconds) * time.Second
	}
	if cfg.Queue.BackoffCapSeconds > 0 {
		qc.BackoffCap = time.Duration(cfg.Queue.BackoffCapSeconds) * time.Second
	}
	return qc
}

// buildPeerMaps returns (knownPeers for InboundConfig, peerMap for OutboundConfig).
func buildPeerMaps(cfg config.EmailConfig) (map[string]bool, map[string]config.EmailPeer) {
	known := make(map[string]bool, len(cfg.Peers))
	peerMap := make(map[string]config.EmailPeer, len(cfg.Peers))
	for _, p := range cfg.Peers {
		if p.DaemonID != "" {
			known[p.DaemonID] = true
		}
		if p.Handle != "" {
			peerMap[p.Handle] = p
		}
	}
	return known, peerMap
}

// stateDirPath resolves the state directory. In production it lives under
// os.TempDir() namespaced by DaemonHandle; tests override via stateDirFn.
func (b *Bridge) stateDirPath(cfg config.EmailConfig) string {
	if b.stateDirFn != nil {
		return b.stateDirFn(cfg)
	}
	return filepath.Join(os.TempDir(), "thrum-email-state-"+cfg.DaemonHandle)
}

// configDirPath resolves the directory that contains config.json. In
// production the daemon wires this via DaemonHandle; tests override via
// configDirFn.
func (b *Bridge) configDirPath(cfg config.EmailConfig) string {
	if b.configDirFn != nil {
		return b.configDirFn(cfg)
	}
	return filepath.Join(os.TempDir(), "thrum-email-config-"+cfg.DaemonHandle)
}
