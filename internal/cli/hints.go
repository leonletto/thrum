package cli

import (
	"math/rand"
	"time"
)

var (
	// Random source for hint rotation (not security-sensitive, just UI hint selection).
	hintRandom = rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // G404: non-security random for hint rotation
)

// Hint returns a contextual hint for the given command.
// Returns empty string if hints should be suppressed (quiet/JSON mode).
func Hint(command string, quiet, jsonMode bool) string {
	if quiet || jsonMode {
		return ""
	}

	hints, ok := commandHints[command]
	if !ok || len(hints) == 0 {
		return ""
	}

	// Select a random hint from the available options
	idx := hintRandom.Intn(len(hints))
	return "  " + hints[idx] + "\n"
}

// commandHints maps command names to possible contextual hints.
var commandHints = map[string][]string{
	"agent.register": {
		"Tip: Start a session with 'thrum agent start'",
		"Tip: Check your identity with 'thrum agent whoami'",
		"Tip: See all agents with 'thrum agent list'",
	},
	"session.start": {
		"Tip: Set your intent with 'thrum agent set-intent \"what you're working on\"'",
		"Tip: Link a task with 'thrum agent set-task beads:issue-id'",
		"Tip: See your status with 'thrum status'",
	},
	"session.end": {
		"Tip: Check agent contexts with 'thrum agent list --context'",
		"Tip: Start a new session with 'thrum agent start'",
	},
	"send": {
		"Tip: Check inbox with 'thrum inbox'",
		"Tip: Send to specific agent with '--mention @role'",
		"Tip: Add context with '--scope module:name'",
	},
	"inbox.empty": {
		"Tip: Send a message with 'thrum send \"hello\"'",
		"Tip: Check all agents with 'thrum agent list'",
	},
	"inbox": {
		"Tip: Filter by scope with '--scope type:value'",
		"Tip: See only unread with '--unread'",
		"Tip: Reply to a message with 'thrum reply msg_id \"text\"'",
	},
	"agent.list": {
		"Tip: See work contexts with 'thrum agent list --context'",
		"Tip: View agent details with 'thrum agent context @role'",
		"Tip: Get an overview with 'thrum overview'",
	},
	"agent.context": {
		"Tip: Check who's editing a file with 'thrum who-has filename.go'",
		"Tip: Ping an agent with 'thrum ping @role'",
	},
	"status": {
		"Tip: Get a full overview with 'thrum overview'",
		"Tip: See team activity with 'thrum agent list --context'",
		"Tip: Check inbox with 'thrum inbox'",
	},
	"quickstart": {
		"Tip: Send your first message with 'thrum send \"hello team\"'",
		"Tip: See who's online with 'thrum agent list'",
		"Tip: Get an overview with 'thrum overview'",
	},
	"overview": {
		"Tip: Dive deeper with 'thrum agent context @role' for specific agents",
		"Tip: Check messages with 'thrum inbox'",
	},
	"who-has": {
		"Tip: See all agent contexts with 'thrum agent list --context'",
		"Tip: Coordinate with 'thrum send \"message\" --mention @role'",
	},
	"ping": {
		"Tip: See all active agents with 'thrum agent list'",
		"Tip: Send a message with 'thrum send \"text\" --mention @role'",
	},
	"session.set-intent": {
		"Tip: Team can see your intent in 'thrum agent list --context'",
		"Tip: Link a task with 'thrum agent set-task beads:issue-id'",
	},
	"session.set-task": {
		"Tip: Set your work intent with 'thrum agent set-intent \"description\"'",
		"Tip: Update heartbeat with 'thrum session heartbeat'",
	},
	"session.heartbeat": {
		"Tip: Team sees your git context in 'thrum agent context'",
		"Tip: Check your status with 'thrum status'",
	},
	// Empty state hints
	"agent.list.empty": {
		"Tip: Register with 'thrum agent register --role X --module Y'",
	},
	"agent.context.empty": {
		"Tip: Start a session with 'thrum agent start'",
	},
	"subscriptions.empty": {
		"Tip: Subscribe with 'thrum subscribe --scope module:X'",
		"Tip: Subscribe to all with 'thrum subscribe --all'",
	},
}
