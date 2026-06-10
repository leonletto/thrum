package daemon

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// WebSocketServer is an interface for the WebSocket server to avoid import cycles.
type WebSocketServer interface {
	Start(ctx context.Context) error
	Stop() error
	Port() int
}

// tsnetShutdownTimeout bounds how long graceful shutdown waits for the inbound
// tsnet peer-RPC node to be released. The release must complete before the PID
// file is removed (thrum-oqao), but it must not be able to wedge the stop
// forever — if the node-release blocks past this budget, shutdown falls through
// and proceeds (logged). tsnet.Server.Close() normally completes in well under
// a second; this is generous headroom that still leaves margin under the
// caller's stop-wait timeout (see internal/cli/daemon.go DaemonStop). A var (not
// const) so tests can shrink it to exercise the fall-through path quickly.
var tsnetShutdownTimeout = 6 * time.Second

// Lifecycle manages the daemon lifecycle including signal handling and shutdown.
type Lifecycle struct {
	server        *Server
	wsServer      WebSocketServer
	pidFile       string
	wsPortFile    string
	repoPath      string    // Repository path this daemon serves
	socketPath    string    // Unix socket path
	lockFile      string    // Lock file path for flock
	lock          *FileLock // File lock held for lifetime of daemon
	shutdownCh    chan struct{}
	shutdownOnce  sync.Once
	preShutdownMu sync.Mutex                  // guards tsnetShutdown against a shutdown/Set race
	tsnetShutdown func(context.Context) error // releases the inbound tsnet node; called before PID removal
}

// NewLifecycle creates a new lifecycle manager.
// WsServer and wsPortFile are optional - pass nil and "" if WebSocket is not used.
func NewLifecycle(server *Server, pidFile string, wsServer WebSocketServer, wsPortFile string) *Lifecycle {
	return &Lifecycle{
		server:     server,
		wsServer:   wsServer,
		pidFile:    pidFile,
		wsPortFile: wsPortFile,
		shutdownCh: make(chan struct{}),
	}
}

// SetRepoInfo sets the repository path and socket path for PID file metadata.
// This should be called before Run().
func (l *Lifecycle) SetRepoInfo(repoPath, socketPath string) {
	l.repoPath = repoPath
	l.socketPath = socketPath
}

// SetLockFile sets the lock file path for flock-based process detection.
// This should be called before Run().
func (l *Lifecycle) SetLockFile(lockFile string) {
	l.lockFile = lockFile
}

// SetTsnetShutdown registers the inbound tsnet peer-RPC node release hook
// (thrum-oqao). Graceful shutdown invokes it BEFORE removing the PID file so a
// restart's new process cannot re-bind the same tsnet state dir while the old
// node is still registered. The tsnet listener starts lazily (boot or
// peer.join), so this may be called after Run() has begun — access is mutex
// guarded. Safe to leave unset (no-op) when tsnet sync is disabled.
func (l *Lifecycle) SetTsnetShutdown(fn func(context.Context) error) {
	l.preShutdownMu.Lock()
	l.tsnetShutdown = fn
	l.preShutdownMu.Unlock()
}

// Run starts the server and handles signals until shutdown.
func (l *Lifecycle) Run(ctx context.Context) error {
	// 1. Acquire file lock for SIGKILL resilience (if configured)
	// The OS automatically releases this lock when the process dies (even SIGKILL)
	if l.lockFile != "" {
		lock, err := AcquireLock(l.lockFile)
		if err != nil {
			return fmt.Errorf("failed to acquire daemon lock: %w", err)
		}
		l.lock = lock
		// Register lock release immediately — covers ALL subsequent return paths
		defer func() {
			if l.lock != nil {
				if err := l.lock.Release(); err != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to release lock: %v\n", err)
				}
			}
		}()
	}

	// 2. Pre-startup validation: check for existing daemon
	existing, existingInfo, err := CheckPIDFileJSON(l.pidFile)
	if err != nil {
		// Error reading PID file (corrupted, permission issue, etc.)
		fmt.Fprintf(os.Stderr, "Warning: failed to read existing PID file: %v\n", err)
		// Continue with startup - we'll overwrite the bad PID file
	} else if existing {
		// Process is running - check repo affinity
		if ValidatePIDRepo(existingInfo, l.repoPath) {
			// Daemon already running for THIS repo - error
			return fmt.Errorf("daemon already running (PID %d) for repo %s", existingInfo.PID, l.repoPath)
		}
		// Different repo - log warning and proceed
		fmt.Fprintf(os.Stderr, "WARNING: Daemon PID %d is running for different repo %s, overwriting\n",
			existingInfo.PID, existingInfo.RepoPath)
	}
	// If process not running, PID file is stale - we'll overwrite it

	// 3. Write PID file with metadata
	pidInfo := PIDInfo{
		PID:        os.Getpid(),
		RepoPath:   l.repoPath,
		StartedAt:  time.Now().UTC(),
		SocketPath: l.socketPath,
	}
	if err := WritePIDFileJSON(l.pidFile, pidInfo); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	// 4. Safety net: clean up PID/socket/port files on ANY exit path
	// This defer catches panics, early returns, and any unexpected exits
	// Note: lock release is handled by the defer above (registered immediately after acquisition)
	var shutdownComplete atomic.Bool
	defer func() {
		if !shutdownComplete.Load() {
			// shutdown() didn't run — clean up manually
			// These operations are idempotent (ignore "not exists" errors)
			_ = l.server.Stop() // Stops server and removes socket
			if l.wsServer != nil {
				_ = l.wsServer.Stop()
				if l.wsPortFile != "" {
					_ = RemovePortFile(l.wsPortFile)
				}
			}
			_ = RemovePIDFile(l.pidFile)
		}
	}()

	// 5. Start Unix socket server
	if err := l.server.Start(ctx); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// 6. Start WebSocket server if configured
	if l.wsServer != nil {
		if err := l.wsServer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start WebSocket server: %w", err)
		}

		// Write WebSocket port file
		if l.wsPortFile != "" {
			port := l.wsServer.Port()
			if err := WritePortFile(l.wsPortFile, port); err != nil {
				return fmt.Errorf("failed to write WebSocket port file: %w", err)
			}
		}
	}

	// Handle signals
	go l.handleSignals(ctx)

	// Wait for shutdown signal
	<-l.shutdownCh

	// Perform graceful shutdown
	shutdownComplete.Store(true)
	return l.shutdown()
}

// handleSignals listens for OS signals and triggers shutdown.
func (l *Lifecycle) handleSignals(_ context.Context) {
	sigCh := make(chan os.Signal, 1)

	// Register for SIGTERM and SIGINT (graceful shutdown)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Note: SIGHUP for reload config is reserved for future implementation

	// Wait for signal
	sig := <-sigCh

	fmt.Fprintf(os.Stderr, "Received signal %v, initiating graceful shutdown...\n", sig)

	// Trigger shutdown (protected by sync.Once to prevent double-close)
	l.shutdownOnce.Do(func() {
		close(l.shutdownCh)
	})
}

// shutdown performs graceful shutdown sequence.
func (l *Lifecycle) shutdown() error {
	fmt.Fprintln(os.Stderr, "Starting graceful shutdown...")

	// Step 1: Stop accepting new connections
	// This is handled by server.Stop() which closes the listener

	// Step 2: Complete in-flight requests (with timeout)
	// The server.Stop() method already waits for connections with a timeout

	// Step 3: Run sync if needed (future - Epic 5)
	// TODO: Add sync functionality in Epic 5

	// Step 4: Stop WebSocket server if configured
	if l.wsServer != nil {
		if err := l.wsServer.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Error stopping WebSocket server: %v\n", err)
			// Continue with cleanup even if stop fails
		}

		// Remove port file so stale port data doesn't mislead clients
		if l.wsPortFile != "" {
			if err := RemovePortFile(l.wsPortFile); err != nil {
				fmt.Fprintf(os.Stderr, "Error removing WebSocket port file: %v\n", err)
			}
		}
	}

	// Step 5: Close socket and stop Unix server
	if err := l.server.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "Error stopping server: %v\n", err)
		// Continue with cleanup even if stop fails
	}

	// Step 5b (thrum-oqao): release the inbound tsnet peer-RPC node BEFORE
	// removing the PID file. DaemonStop treats a missing PID file as "stopped",
	// so if the tsnet node is still bound here, a restart's freshly-exec'd
	// process re-binds the same tsnet state dir while the old node is still
	// registered on the control plane — the two netstacks share one node key,
	// and inbound peer-RPC (:9177) flaps (RST/EOF) until the overlap clears.
	// Closing the node here, and blocking until it is released, makes the
	// PID-file-gone signal truthfully mean "node released".
	l.releaseTsnetNode()

	// Step 6: Remove PID file
	if err := RemovePIDFile(l.pidFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing PID file: %v\n", err)
		return err
	}

	// Step 7: Release file lock
	// Release here for clean shutdown; the defer in Run() is the safety net
	// for non-graceful exits. Release() is idempotent (nil-safe).
	if l.lock != nil {
		if err := l.lock.Release(); err != nil {
			fmt.Fprintf(os.Stderr, "Error releasing lock: %v\n", err)
		}
	}

	fmt.Fprintln(os.Stderr, "Graceful shutdown complete")
	return nil
}

// releaseTsnetNode invokes the registered tsnet-node release hook (thrum-oqao)
// and blocks until it completes, bounded by tsnetShutdownTimeout. The hook is
// run in a goroutine so a stuck node-release cannot wedge the daemon stop
// forever — on timeout we log and fall through, letting shutdown proceed. A
// nil hook (tsnet sync disabled) is a no-op.
func (l *Lifecycle) releaseTsnetNode() {
	l.preShutdownMu.Lock()
	hook := l.tsnetShutdown
	l.preShutdownMu.Unlock()
	if hook == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), tsnetShutdownTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- hook(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error releasing tsnet peer node: %v\n", err)
		}
	case <-ctx.Done():
		fmt.Fprintf(os.Stderr, "Timed out releasing tsnet peer node after %s; proceeding with shutdown\n", tsnetShutdownTimeout)
	}
}

// Shutdown triggers a graceful shutdown (can be called programmatically).
func (l *Lifecycle) Shutdown() {
	l.shutdownOnce.Do(func() {
		close(l.shutdownCh)
	})
}

// ShutdownWithTimeout triggers a shutdown and waits with a timeout
// Note: This should only be called when using Run() to manage the lifecycle.
func (l *Lifecycle) ShutdownWithTimeout(timeout time.Duration) error {
	// Trigger shutdown
	l.Shutdown()

	// Wait for shutdown channel to be processed or timeout
	// The actual shutdown happens in Run()
	select {
	case <-l.shutdownCh:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("shutdown signal not processed after %v", timeout)
	}
}
