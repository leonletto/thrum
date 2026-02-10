package daemon

import (
	"database/sql"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/subscriptions"
	"github.com/leonletto/thrum/internal/websocket"
)

// EventStreamingSetup encapsulates the event streaming infrastructure.
// It provides push notifications to connected clients (Unix socket + WebSocket).
type EventStreamingSetup struct {
	Broadcaster *Broadcaster
	Dispatcher  *subscriptions.Dispatcher
}

// NewEventStreamingSetup creates the complete event streaming infrastructure.
// This should be called once at daemon startup.
//
// Parameters:
//   - unixClients: Registry of Unix socket clients
//   - wsServer: WebSocket server (for accessing its client registry)
//   - db: SQLite database for subscription queries
//
// Returns a setup that can be used to configure handlers with push notification support.
func NewEventStreamingSetup(
	unixClients *ClientRegistry,
	wsServer *websocket.Server,
	db *sql.DB,
) *EventStreamingSetup {
	// Create broadcaster that pushes to both Unix and WebSocket clients
	broadcaster := NewBroadcaster(unixClients, wsServer.GetClients())

	// Create dispatcher with the broadcaster as the client notifier
	dispatcher := subscriptions.NewDispatcher(db)
	dispatcher.SetClientNotifier(broadcaster)

	return &EventStreamingSetup{
		Broadcaster: broadcaster,
		Dispatcher:  dispatcher,
	}
}

// NewEventStreamingSetupFromState creates event streaming infrastructure from daemon state.
// Convenience wrapper when you only have the state and client registries.
func NewEventStreamingSetupFromState(
	st *state.State,
	unixClients *ClientRegistry,
	wsServer *websocket.Server,
) *EventStreamingSetup {
	return NewEventStreamingSetup(unixClients, wsServer, st.DB())
}
