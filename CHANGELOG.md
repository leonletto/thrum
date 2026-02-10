# Changelog

All notable changes to the Thrum daemon project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added - Epic: Auto-detect worktree for identity resolution

#### Identity Resolution Improvements

- **Worktree-based identity filtering**: When multiple identity files exist,
  thrum now automatically filters by the current git worktree name
- **Git-based worktree detection**: Uses `git rev-parse --show-toplevel` for
  accurate worktree name detection regardless of current directory
- **Improved error messages**: Clear error messages listing available identity
  names and suggesting fixes when auto-selection fails
- **Backward compatibility**: Solo-agent repos and THRUM_NAME env var continue
  to work as before

#### Migration Notes

If you have existing identity files with incorrect worktree fields (created
before this update), you can fix them by:

1. Navigate to the correct worktree: `cd ~/.workspaces/thrum/<worktree-name>`
2. Re-register the agent: `thrum quickstart --role <role> --module <module>`

This will update the identity file with the correct worktree name, enabling
auto-selection without THRUM_NAME.

### Added - Epic 11: UI Core

#### Component System

- **AppShell**: Main application layout with fixed header and sidebar
- **Sidebar**: Navigation component with Live Feed, My Inbox, and Agent List
  sections
- **AgentCard**: Individual agent display with status, unread count, and last
  checkin time
- **AgentList**: Scrollable list of agents sorted by most recent activity
- **LiveFeed**: Real-time activity stream showing all messages and events
- **FeedItem**: Individual feed item component with sender, receiver, and
  message preview
- **InboxView**: Inbox display supporting both user inbox and agent-specific
  inboxes
- **SidebarItem**: Reusable navigation button with icon, label, badge, and
  active states

#### State Management

- TanStack Store integration for global UI state
- UI store managing view selection and agent navigation
- Store actions: `selectLiveFeed()`, `selectMyInbox()`, `selectAgent()`
- Prepared for TanStack Query integration for server state

#### Routing & Navigation

- Content area routing based on UI store state
- Three main views: Live Feed, My Inbox, Agent Inbox
- Active state highlighting in sidebar
- Agent selection flow with visual feedback

#### Testing Infrastructure

- 81 comprehensive tests for web-app components
- 33 integration tests covering:
  - Navigation flow between views
  - Layout structure and semantic HTML
  - Agent list display and interactions
  - Live feed functionality
  - Inbox view behavior
- Test utilities with React Testing Library
- 104 total tests passing across all packages

#### Documentation

- Complete component architecture documentation
- State management patterns guide
- Real-time update strategy documentation
- Styling guidelines (Tailwind CSS + shadcn/ui)
- Component API reference with TypeScript types
- Usage examples for all components

#### Styling System

- Tailwind CSS utility-first styling
- shadcn/ui component library integration
- Design tokens and spacing scale
- Consistent color scheme with semantic tokens
- Responsive layout patterns

### Added - Epic 10: UI Foundation

#### Project Structure

- Turborepo monorepo with three packages:
  - `@thrum/shared-logic`: Framework-agnostic business logic
  - `@thrum/web-app`: React web application
  - `@thrum/tui-app`: Terminal UI (placeholder)
- pnpm workspace configuration
- Shared TypeScript configuration

#### Web Application Foundation

- Vite development server with HMR
- React 19 with TypeScript
- React Router for navigation
- Tailwind CSS styling system
- shadcn/ui component library
- ESLint + Prettier configuration

#### Shared Logic Package

- WebSocket client for daemon communication
- JSON-RPC 2.0 request/response handling
- TanStack Store for state management
- Type-safe API definitions
- Zod schema validation

#### Testing Setup

- Vitest test runner
- React Testing Library
- jsdom environment for component testing
- Test coverage reporting
- Unit and integration test patterns

#### Development Tools

- TypeScript strict mode
- Hot module replacement
- Build optimization
- Linting and formatting
- Type checking

### Added - Epic 9: WebSocket Bridge

#### WebSocket Server

- WebSocket server with JSON-RPC 2.0 support on `ws://localhost:9999`
- Concurrent client support with goroutine-based connection handling
- Client registry for tracking connected clients by session ID
- Graceful shutdown with configurable timeout
- Integration with daemon RPC handler registry

#### User Registration

- `user.register` RPC method for WebSocket-only user authentication
- Automatic user ID generation based on username (format: `user:{username}`)
- Session auto-creation on user registration
- Username validation (lowercase alphanumeric + hyphens)

#### Event Streaming

- Real-time event notifications to connected clients
- Subscription-based event filtering (scope, mention, all)
- Client buffer management with configurable limits
- Broadcaster pattern for multi-transport notification (Unix socket + WebSocket)
- Event types: `message.created`, `message.edited`, `message.deleted`,
  `agent.registered`, `session.started`, `session.ended`

#### Impersonation Support

- Users can impersonate agents to send messages "as" an agent
- Authorization validation (only users can impersonate, only agents can be
  impersonated)
- Audit trail with `authored_by` and `disclosed` fields
- Schema migration (v3 → v4) for impersonation metadata

#### API Documentation

- Comprehensive WebSocket API reference (`docs/api/websocket.md`)
- Event types and payloads reference (`docs/api/events.md`)
- Authentication and authorization guide (`docs/api/authentication.md`)
- Working code examples:
  - TypeScript/JavaScript client (`docs/api/examples/ws-client.ts`)
  - Go client (`docs/api/examples/ws-client.go`)

#### Testing

- Integration tests for WebSocket server functionality
- Multi-client concurrent request testing
- Batch JSON-RPC request handling
- Error handling verification
- Connection lifecycle management tests

### Changed

- Database schema upgraded to version 4 (added `authored_by` and `disclosed`
  columns)
- Message handler now supports impersonation via `acting_as` parameter
- Event streaming integrated with subscription dispatcher
- Daemon now supports dual transport: Unix socket and WebSocket

### Fixed

- Schema migration logic handles incremental upgrades from v3 to v4

## [0.1.0] - Earlier Development

### Added - Core Infrastructure

#### Messaging System

- Event-sourced messaging with JSONL append-only log
- SQLite projection for queries
- Message scopes and references (tags, mentions)
- Thread support for conversation organization
- Message editing with full edit history

#### Agent System

- Agent registration and identity management
- Session lifecycle management (start, end, crash recovery)
- Agent list with filtering by role and module

#### RPC Server

- Unix socket JSON-RPC server
- Handler registry for RPC methods
- Transport context for authorization
- Batch request support

#### Synchronization

- Git-based message synchronization
- Automatic sync loop with configurable interval
- Branch management for sync operations
- Conflict resolution with timestamp-based ordering
- Sync status reporting

#### Subscriptions

- Event subscription system with filtering
- Notification dispatcher
- Buffer management for slow clients

#### Testing

- Comprehensive test suite with >70% coverage
- Unit tests for all core components
- Integration tests for RPC methods
- Test utilities for temporary databases

## Migration Guide

### Upgrading to WebSocket Support

If you're upgrading from an earlier version:

1. **Database Migration**: The daemon automatically migrates the database schema
   from v3 to v4 on startup
2. **API Compatibility**: All existing Unix socket RPC methods remain unchanged
3. **WebSocket Access**: WebSocket server starts automatically on port 9999
4. **User Registration**: New `user.register` method available for WebSocket
   clients

### Breaking Changes

None. WebSocket support is additive and backward compatible.

## Security Notes

### Current Security Posture (MVP)

- **Local-only**: Daemon binds to localhost (127.0.0.1)
- **No encryption**: WebSocket traffic is unencrypted (ws://, not wss://)
- **No authentication**: Trust-based security for same-machine clients
- **Unix socket**: File system permissions for access control

### Future Security Enhancements

Planned for future releases:

- TLS/SSL support for WebSocket (wss://)
- Token-based authentication for users
- Role-based access control (RBAC)
- API keys for programmatic access
- Comprehensive audit logging

## Links

- [WebSocket API Documentation](docs/api/websocket.md)
- [Event Reference](docs/api/events.md)
- [Authentication Guide](docs/api/authentication.md)

---

**Note**: This changelog documents changes since the beginning of Epic 9.
Earlier work is summarized in the [0.1.0] section.
