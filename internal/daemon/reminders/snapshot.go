package reminders

// MaxSnapshotBytes is the 16KB hard cap from substrate-canonical-reference
// §3.11 Implementation Guard 5. Pane snapshots feed condition-triggered
// reminders; without a cap, busy panes can balloon the reminders table to
// tens of MB and grow the SQLite WAL. Truncation runs at INSERT.
const MaxSnapshotBytes = 16 * 1024

const truncationMarker = " [TRUNCATED]"

// TruncateSnapshot enforces the 16KB cap with a trailing marker. Inputs at
// or below the cap are returned unchanged. Inputs over the cap are
// truncated such that the returned string fits within MaxSnapshotBytes
// including the marker, so the observable bytes-on-disk never exceed the
// cap.
func TruncateSnapshot(s string) string {
	if len(s) <= MaxSnapshotBytes {
		return s
	}
	head := s[:MaxSnapshotBytes-len(truncationMarker)]
	return head + truncationMarker
}
