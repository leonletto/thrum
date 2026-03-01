package websocket

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DisconnectFunc is called when a WebSocket client disconnects.
// It receives the sessionID of the disconnected client.
type DisconnectFunc func(sessionID string)

// Server represents the WebSocket RPC server.
type Server struct {
	addr         string
	httpServer   *http.Server
	upgrader     websocket.Upgrader
	registry     HandlerRegistry
	clients      *ClientRegistry
	onDisconnect DisconnectFunc
	mu           sync.RWMutex
	shutdown     bool
	wg           sync.WaitGroup
	startTime    time.Time
}

// NewServer creates a new WebSocket RPC server.
// Addr format: "host:port" (e.g., "localhost:9999").
// Registry provides the RPC handlers that will handle JSON-RPC requests.
// UiFS is an optional filesystem for serving the embedded web UI. When nil,
// the WebSocket handler is registered at "/" for backwards compatibility.
// When provided, WebSocket moves to "/ws" and the UI is served at "/".
func NewServer(addr string, registry HandlerRegistry, uiFS fs.FS) *Server {
	s := &Server{
		addr:      addr,
		registry:  registry,
		clients:   NewClientRegistry(),
		startTime: time.Now(),
		upgrader: websocket.Upgrader{
			// Allow all origins for local development
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}

	// Set up HTTP server with route handlers
	mux := http.NewServeMux()

	if uiFS != nil {
		// UI mode: WebSocket at /ws, static assets and SPA at /
		mux.HandleFunc("/ws", s.handleWebSocket)

		// Static assets with long cache headers
		assetServer := http.FileServerFS(uiFS)
		mux.HandleFunc("/assets/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "max-age=31536000, immutable")
			assetServer.ServeHTTP(w, r)
		})

		// SPA fallback for all other routes
		mux.HandleFunc("/", s.handleSPA(uiFS))
	} else {
		// No UI: WebSocket at root (backwards compatible)
		mux.HandleFunc("/", s.handleWebSocket)
	}

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s
}

// handleSPA returns an HTTP handler that serves the SPA. It reads index.html
// once at startup and serves it from memory for all non-asset paths.
func (s *Server) handleSPA(uiFS fs.FS) http.HandlerFunc {
	// Read index.html once at startup
	indexHTML, err := fs.ReadFile(uiFS, "index.html")
	if err != nil {
		// If index.html is missing, serve a helpful error
		indexHTML = []byte("<!DOCTYPE html><html><body>UI not found</body></html>")
	}

	fileServer := http.FileServerFS(uiFS)

	return func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the exact file from uiFS first (skip root path)
		path := r.URL.Path
		if path != "/" {
			// Strip leading slash for fs.Open
			filePath := path[1:]
			if f, openErr := uiFS.Open(filePath); openErr == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// SPA fallback: serve index.html
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexHTML)
	}
}

// GetRegistry returns the handler registry used by this server.
func (s *Server) GetRegistry() HandlerRegistry {
	return s.registry
}

// GetClients returns the client registry for accessing connected WebSocket clients.
func (s *Server) GetClients() *ClientRegistry {
	return s.clients
}

// SetDisconnectHook registers a callback that fires when a WebSocket client disconnects.
// Used to clean up subscriptions and other session-scoped resources.
func (s *Server) SetDisconnectHook(fn DisconnectFunc) {
	s.onDisconnect = fn
}

// Start starts the WebSocket server and begins accepting connections.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return fmt.Errorf("server is shutting down")
	}
	s.mu.Unlock()

	// Start HTTP server in a goroutine
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "WebSocket server error: %v\n", err)
		}
	}()

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	return nil
}

// Stop stops the WebSocket server and waits for all connections to finish.
func (s *Server) Stop() error {
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()

	// Close all client connections
	s.clients.CloseAll()

	// Shutdown HTTP server with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %w", err)
	}

	// Wait for all connections to finish
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All connections finished
	case <-time.After(5 * time.Second):
		// Timeout waiting for connections
	}

	return nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() string {
	return s.addr
}

// Port returns the port number the server is listening on.
// Returns 0 if the port cannot be parsed from the address.
func (s *Server) Port() int {
	_, portStr, err := splitHostPort(s.addr)
	if err != nil {
		return 0
	}
	port := 0
	for _, c := range portStr {
		if c < '0' || c > '9' {
			return 0
		}
		port = port*10 + int(c-'0')
	}
	return port
}

// splitHostPort splits an address into host and port.
// Similar to net.SplitHostPort but doesn't require net import.
func splitHostPort(addr string) (host, port string, err error) {
	// Find last colon
	lastColon := -1
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			lastColon = i
			break
		}
	}
	if lastColon < 0 {
		return "", "", fmt.Errorf("missing port in address")
	}
	return addr[:lastColon], addr[lastColon+1:], nil
}

// handleWebSocket handles the WebSocket upgrade and connection.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Hold the read lock across both the shutdown check and wg.Add to prevent
	// a race where Stop() calls wg.Wait() between our check and our Add.
	s.mu.RLock()
	if s.shutdown {
		s.mu.RUnlock()
		http.Error(w, "Server is shutting down", http.StatusServiceUnavailable)
		return
	}
	s.wg.Add(1)
	s.mu.RUnlock()

	// Upgrade HTTP connection to WebSocket
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.wg.Done()
		fmt.Fprintf(os.Stderr, "WebSocket upgrade error: %v\n", err)
		return
	}

	// Handle the WebSocket connection
	go s.handleConnection(context.Background(), conn)
}

// handleConnection manages a single WebSocket connection.
func (s *Server) handleConnection(ctx context.Context, conn *websocket.Conn) {
	defer s.wg.Done()
	defer func() {
		_ = conn.Close()
	}()

	// Create a connection wrapper
	wsConn := NewConnection(conn, s)

	// Track in the all-connections set so BroadcastAll can reach this client
	// even before it registers a session (e.g. the browser UI passive observer).
	s.clients.addConn(wsConn)
	defer s.clients.removeConn(wsConn)

	// Start read and write loops
	errCh := make(chan error, 2)

	// Start read loop
	go func() {
		errCh <- wsConn.ReadLoop(ctx)
	}()

	// Start write loop
	go func() {
		errCh <- wsConn.WriteLoop(ctx)
	}()

	// Wait for either loop to error
	<-errCh

	// Close connection
	_ = wsConn.Close()
}

// getHandler retrieves a registered handler by method name.
func (s *Server) getHandler(method string) (Handler, bool) {
	return s.registry.GetHandler(method)
}
