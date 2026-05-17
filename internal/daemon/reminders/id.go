package reminders

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
)

// idPattern matches the phone-style reminder id format:
// reminder-<agent-name>-NNN-NNNN. Agent names are restricted to the
// lowercase-alphanum-underscore charset used by thrum's existing identity
// system (internal/identity/). Hyphens in agent names would be ambiguous
// against the separator hyphens, so they're disallowed at the parser.
var idPattern = regexp.MustCompile(`^reminder-([a-z0-9_]+)-(\d{3})-(\d{4})$`)

// idSpace bounds the random numeric body. Phone-style 7-digit space →
// 10,000,000 possible values per agent. Per-agent prefix means the
// birthday-paradox collision rate stays well under 1% even at 10k mints
// per agent (expected ~5 collisions; see TestMintID_UniqueAcrossManyMints).
var idSpace = big.NewInt(10_000_000)

// MintID produces a reminder id of the form
// `reminder-<agent>-NNN-NNNN`. Phone-style formatting is intentional:
// per Leon-brainstorm-Q3.3 the id needs to be verbally dictatable
// ("clear reminder one-two-three four-five-six-seven for docs-bot").
//
// Panics on crypto/rand failure (matches the precedent set by
// ulid.MustNew in internal/daemon/monitor/supervisor.go). A failing
// system entropy source would be a daemon-fatal condition anyway.
func MintID(agent string) string {
	n, err := rand.Int(rand.Reader, idSpace)
	if err != nil {
		panic(fmt.Sprintf("reminders: mint id from crypto/rand: %v", err))
	}
	body := n.Int64()
	return fmt.Sprintf("reminder-%s-%03d-%04d", agent, body/10_000, body%10_000)
}

// ParseID extracts (agent, numeric body) from a reminder id. The returned
// numeric body is the 7-digit string with the separating hyphen removed,
// matching what a verbal-dictation parser would yield.
func ParseID(id string) (agent string, num string, err error) {
	m := idPattern.FindStringSubmatch(id)
	if m == nil {
		return "", "", fmt.Errorf("invalid reminder id: %q", id)
	}
	return m[1], m[2] + m[3], nil
}
