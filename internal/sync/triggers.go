package sync

// Triggers provides methods to trigger sync operations on specific events.
type Triggers struct {
	loop *SyncLoop
}

// NewTriggers creates a new Triggers instance.
func NewTriggers(loop *SyncLoop) *Triggers {
	return &Triggers{
		loop: loop,
	}
}

// SyncOnWrite triggers a sync after a local write operation.
// This should be called by the message/event writing code after successfully
// appending to the JSONL file, to ensure changes are quickly synced to remote.
//
// Example usage in the daemon:
//
//	func (s *State) WriteEvent(event any) error {
//	    // Write to JSONL
//	    if err := s.jsonlWriter.Append(event); err != nil {
//	        return err
//	    }
//
//	    // Trigger sync after successful write
//	    s.syncTriggers.SyncOnWrite()
//
//	    return nil
//	}
func (t *Triggers) SyncOnWrite() {
	if t.loop != nil {
		t.loop.TriggerSync()
	}
}

// SyncManual triggers a manual sync (same as SyncOnWrite, but more explicit).
// Used when the user explicitly requests a sync via RPC or CLI.
func (t *Triggers) SyncManual() {
	if t.loop != nil {
		t.loop.TriggerSync()
	}
}
