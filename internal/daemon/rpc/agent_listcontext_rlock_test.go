package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// TestHandleListContext_TakesReadLock_NotSerializedBehindReaders is the
// thrum-5988 regression. HandleListContext is a read-only handler (it only
// runs a SELECT + row scan; nothing under the lock mutates State or the DB),
// but it historically acquired the global write Lock(). On a busy daemon that
// serialized every concurrent read behind one another (and behind structural
// writers), producing the observed multi-second `thrum prime` stall (prime's
// cosmetic active-count calls listContext on every SessionStart).
//
// This test is DETERMINISTIC — no timing-margin guesswork. The test goroutine
// holds the shared RLock for the whole body (and does NO DB work, so it never
// contends for the single SQLite connection — this isolates the LOCK behavior
// from the orthogonal MaxOpenConns=1 connection serialization). It then invokes
// HandleListContext from another goroutine:
//   - With RLock() (fixed): the handler acquires the read lock concurrently
//     with ours and returns promptly → the call completes before the deadline.
//   - With Lock() (pre-fix): sync.RWMutex refuses the write lock while a reader
//     holds RLock, so the handler blocks until we RUnlock → the call does NOT
//     complete within the deadline and the test fails.
//
// (Release line carries the legacy handler signature — params json.RawMessage
// — so the call passes an empty JSON object instead of a typed request struct;
// the lock behaviour under test is identical.)
func TestHandleListContext_TakesReadLock_NotSerializedBehindReaders(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "r_5988_rlock", "")
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	h := NewAgentHandler(st)

	// Simulate a concurrent in-flight reader holding the shared RLock. We hold
	// it for the whole test body; the deferred RUnlock also unblocks a pre-fix
	// (write-Lock) handler so the spawned goroutine can't leak past the test.
	st.RLock()
	defer st.RUnlock()

	done := make(chan error, 1)
	go func() {
		_, callErr := h.HandleListContext(context.Background(), json.RawMessage(`{}`))
		done <- callErr
	}()

	select {
	case callErr := <-done:
		if callErr != nil {
			t.Fatalf("HandleListContext returned error: %v", callErr)
		}
		// PASS: completed while we still hold RLock → it took the read lock
		// concurrently rather than blocking for the write lock.
	case <-time.After(3 * time.Second):
		t.Fatal("HandleListContext did not complete within 3s while a concurrent reader held RLock — " +
			"it must take RLock(), not the write Lock() (thrum-5988 priority inversion)")
	}
}
