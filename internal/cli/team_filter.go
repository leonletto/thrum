package cli

import (
	"fmt"
	"sort"
	"strings"
)

// UnknownDaemonBucket is the label for team members whose origin_daemon is
// empty/NULL in the daemons aggregation (thrum-l2kxw). Legacy and
// not-yet-stamped records carry no origin_daemon; bucketing them keeps the
// per-daemon counts summing to the member total instead of silently dropping
// rows.
const UnknownDaemonBucket = "unknown"

// FilterByDaemon returns the members whose OriginDaemon equals daemonID
// (thrum-l2kxw). Exact match; an empty daemonID matches members with no
// origin_daemon (the legacy/unknown set) so `--daemon ""` is meaningful.
func FilterByDaemon(members []TeamMember, daemonID string) []TeamMember {
	out := make([]TeamMember, 0, len(members))
	for _, m := range members {
		if m.OriginDaemon == daemonID {
			out = append(out, m)
		}
	}
	return out
}

// FilterByHost returns the members whose Hostname equals hostname
// (thrum-l2kxw). Exact match; an empty hostname matches members with no
// hostname recorded.
func FilterByHost(members []TeamMember, hostname string) []TeamMember {
	out := make([]TeamMember, 0, len(members))
	for _, m := range members {
		if m.Hostname == hostname {
			out = append(out, m)
		}
	}
	return out
}

// NoAgentsForFilter returns the clean human message shown when a --daemon /
// --host / local filter matches nothing (thrum-l2kxw). The acceptance criterion
// is "a clean 'no agents on <x>' message, not an error/empty crash". kind is the
// filter dimension ("daemon" or "host"); value is the filter value (rendered as
// "unknown" when empty so the legacy/unknown set reads cleanly).
func NoAgentsForFilter(kind, value string) string {
	if value == "" {
		value = UnknownDaemonBucket
	}
	return fmt.Sprintf("No agents on %s %q.\n", kind, value)
}

// DaemonAggregate is one row of the `thrum team daemons` view: a distinct
// origin_daemon with its hostname and the count of members under it.
type DaemonAggregate struct {
	DaemonID   string `json:"daemon_id"`
	Hostname   string `json:"hostname"`
	AgentCount int    `json:"agent_count"`
}

// AggregateByDaemon groups members by origin_daemon into one row per distinct
// daemon (thrum-l2kxw). Members with an empty origin_daemon bucket under
// UnknownDaemonBucket so the counts always sum to len(members) — no row is
// silently dropped. The hostname shown is the first non-empty hostname seen
// for that daemon (members of one daemon share a host); this applies to the
// unknown bucket too — a member with no origin_daemon but a recorded hostname
// surfaces that hostname on the unknown row. Rows are sorted by daemon_id for
// stable output, with the unknown bucket last.
func AggregateByDaemon(members []TeamMember) []DaemonAggregate {
	type acc struct {
		hostname string
		count    int
	}
	byDaemon := map[string]*acc{}
	order := []string{}
	for _, m := range members {
		key := m.OriginDaemon
		if key == "" {
			key = UnknownDaemonBucket
		}
		a, ok := byDaemon[key]
		if !ok {
			a = &acc{}
			byDaemon[key] = a
			order = append(order, key)
		}
		a.count++
		if a.hostname == "" && m.Hostname != "" {
			a.hostname = m.Hostname
		}
	}

	// Stable order: daemon_id ascending, unknown bucket last.
	sort.Slice(order, func(i, j int) bool {
		if order[i] == UnknownDaemonBucket {
			return false
		}
		if order[j] == UnknownDaemonBucket {
			return true
		}
		return order[i] < order[j]
	})

	out := make([]DaemonAggregate, 0, len(order))
	for _, key := range order {
		out = append(out, DaemonAggregate{
			DaemonID:   key,
			Hostname:   byDaemon[key].hostname,
			AgentCount: byDaemon[key].count,
		})
	}
	return out
}

// FormatDaemonAggregates renders the `thrum team daemons` table (human
// output). Columns: DAEMON, HOST, AGENTS. A trailing total line mirrors the
// sum so an operator can eyeball that nothing was dropped.
func FormatDaemonAggregates(rows []DaemonAggregate) string {
	if len(rows) == 0 {
		return "No agents.\n"
	}
	var out strings.Builder
	// Width the DAEMON column to the longest id (min header width).
	w := len("DAEMON")
	for _, r := range rows {
		if len(r.DaemonID) > w {
			w = len(r.DaemonID)
		}
	}
	total := 0
	fmt.Fprintf(&out, "%-*s  %-20s  %s\n", w, "DAEMON", "HOST", "AGENTS")
	for _, r := range rows {
		host := r.Hostname
		if host == "" {
			host = "-"
		}
		fmt.Fprintf(&out, "%-*s  %-20s  %d\n", w, r.DaemonID, host, r.AgentCount)
		total += r.AgentCount
	}
	fmt.Fprintf(&out, "%-*s  %-20s  %d\n", w, "total", "", total)
	return out.String()
}
