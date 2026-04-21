package mcp

import (
	"context"
	"fmt"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/identity"
)

// Server is the Thrum MCP server that exposes agent messaging tools.
type Server struct {
	socketPath string
	agentName  string
	agentRole  string
	agentID    string // composite: agent:{role}:{hash}
	version    string
	server     *gomcp.Server
	waiter     *Waiter // WebSocket waiter for wait_for_message tool
}

// Option configures the MCP server.
type Option func(*Server)

// WithVersion sets the server version string.
func WithVersion(v string) Option {
	return func(s *Server) {
		s.version = v
	}
}

// NewServer creates a new MCP server for the given repository path.
// It resolves the daemon socket path (following .thrum/redirect in worktrees)
// and loads the agent identity from .thrum/identities/{name}.json.
func NewServer(repoPath string, opts ...Option) (*Server, error) {
	// Load agent identity
	cfg, err := config.LoadWithPath(repoPath, "", "")
	if err != nil {
		return nil, fmt.Errorf("load identity: %w", err)
	}
	if cfg.Agent.Name == "" {
		return nil, fmt.Errorf("agent name not configured; register with 'thrum quickstart' first")
	}
	if cfg.Agent.Role == "" {
		return nil, fmt.Errorf("agent role not configured; register with 'thrum quickstart' first")
	}

	// Resolve daemon socket path (follows .thrum/redirect in feature worktrees)
	socketPath := cli.DefaultSocketPath(repoPath)

	// Generate agent ID using standard identity function (consistent with daemon RPC handlers)
	agentID := identity.GenerateAgentID(cfg.RepoID, cfg.Agent.Role, cfg.Agent.Module, cfg.Agent.Name)

	s := &Server{
		socketPath: socketPath,
		agentName:  cfg.Agent.Name,
		agentRole:  cfg.Agent.Role,
		agentID:    agentID,
		version:    "dev",
	}

	for _, opt := range opts {
		opt(s)
	}

	// Create MCP server instance
	s.server = gomcp.NewServer(
		&gomcp.Implementation{
			Name:    "thrum",
			Version: s.version,
		},
		nil,
	)

	// Register all tools
	s.registerTools()

	return s, nil
}

// SetWaiter sets the waiter used by the wait_for_message tool.
func (s *Server) SetWaiter(w *Waiter) {
	s.waiter = w
}

// InitWaiter attaches a polling Waiter for the wait_for_message tool.
// The returned Waiter lives for the duration of ctx.
func (s *Server) InitWaiter(ctx context.Context) {
	s.waiter = NewWaiter(ctx, s.socketPath, s.agentID, s.agentRole)
}

// Run starts the MCP server on stdin/stdout. It blocks until the client
// disconnects or the context is canceled. Cleans up the Waiter on exit.
func (s *Server) Run(ctx context.Context) error {
	defer func() {
		if s.waiter != nil {
			_ = s.waiter.Close()
		}
	}()
	return s.server.Run(ctx, &gomcp.StdioTransport{})
}

// newDaemonClient creates a new per-call daemon RPC client.
// Cli.Client is not concurrent-safe, so we create a fresh connection each time.
func (s *Server) newDaemonClient() (*cli.Client, error) {
	return cli.NewClient(s.socketPath)
}

// registerTools registers all MCP tool handlers with the server.
func (s *Server) registerTools() {
	gomcp.AddTool(s.server, &gomcp.Tool{
		Name:        "send_message",
		Description: "Send a message to another agent via Thrum. Use to=@agent_name for direct messages or to=@everyone for broadcast to all agents",
	}, s.handleSendMessage)

	gomcp.AddTool(s.server, &gomcp.Tool{
		Name:        "check_messages",
		Description: "Check for new messages addressed to this agent",
	}, s.handleCheckMessages)

	gomcp.AddTool(s.server, &gomcp.Tool{
		Name:        "wait_for_message",
		Description: "Block until a message arrives or timeout expires. Designed for background listener sub-agents.",
	}, s.handleWaitForMessage)

	gomcp.AddTool(s.server, &gomcp.Tool{
		Name:        "list_agents",
		Description: "List all registered agents and their status",
	}, s.handleListAgents)

	gomcp.AddTool(s.server, &gomcp.Tool{
		Name:        "broadcast_message",
		Description: "Deprecated: use send_message with to=@everyone instead. Sends a message to all agents via the @everyone group",
	}, s.handleBroadcast)

	// Group management MCP tools removed — groups are no longer user-facing.
	// Telegram bridge still uses group.* RPC handlers directly (not MCP).
}
