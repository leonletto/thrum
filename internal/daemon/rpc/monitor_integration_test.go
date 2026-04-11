package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/monitor"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMonitorIntegration_SubmitMatchDebounceStop exercises the full monitor
// pipeline end-to-end:
//
//  1. Spin up a real daemon.NewServer, register monitor.stop, connect via unix socket.
//  2. Pre-insert a MonitorJob directly (DebounceSeconds=2) to bypass the 30s minimum
//     enforced by supervisor.Add — same reload path the daemon uses on restart.
//  3. Launch supervisor.Start; let it reload and start the tail -F runner.
//  4. Write 5 ERROR lines → assert leading-edge message arrives in DB.
//  5. Sleep 2.5s past the debounce window → assert trailing summary arrives.
//  6. Send monitor.stop RPC → assert row deleted from monitors table.
func TestMonitorIntegration_SubmitMatchDebounceStop(t *testing.T) {
	// macOS enforces a 104-char limit on unix socket paths. t.TempDir() produces
	// paths like /var/folders/.../TestMonitorIntegration.../001/ which exceed
	// that limit, so we create a short-named temp dir under /tmp instead.
	tmpDir, err := os.MkdirTemp("", "tmon")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	socketPath := filepath.Join(tmpDir, "t.sock")
	tempfile := filepath.Join(tmpDir, "dev.log")

	// Touch the tempfile so tail -F has something to open immediately.
	f, err := os.Create(tempfile)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// Build state.
	st, err := state.NewState(tmpDir, tmpDir, "test-repo", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	// The monitor name + caller ID used for both the synthetic agent and the
	// monitor job target. Use an empty target so HandleSend sends a broadcast
	// (no mention recipient validation) — avoids needing a second synthetic agent.
	const monitorName = "int-test"
	const callerID = "monitor:" + monitorName
	const targetAgent = "" // broadcast; recipient validation skipped

	// Pre-insert a synthetic agent + open session so HandleSend's
	// resolveAgentAndSession lookup succeeds for the "monitor:int-test" sender.
	// This mirrors the pattern in internal/daemon/monitor/delivery_test.go
	// TestDelivery_MessageLandsInDB.
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = st.DB().ExecContext(context.Background(), `
		INSERT INTO agents (agent_id, kind, role, module, display, hostname, agent_pid, registered_at, last_seen_at)
		VALUES (?, 'monitor', 'monitor', 'monitor', ?, '', 0, ?, ?)
	`, callerID, monitorName, now, now)
	require.NoError(t, err, "insert synthetic monitor agent")

	sessionID := fmt.Sprintf("ses_monitor_%d", time.Now().UnixNano())
	_, err = st.DB().ExecContext(context.Background(), `
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES (?, ?, ?, ?)
	`, sessionID, callerID, now, now)
	require.NoError(t, err, "insert synthetic monitor session")

	// Build the full pipeline.
	store := monitor.NewMonitorStore(st.DB())
	msgHandler := NewMessageHandler(st)
	delivery := monitor.NewDelivery(msgHandler)
	supervisor := monitor.NewMonitorSupervisor(store, delivery)
	monHandler := NewMonitorHandler(supervisor, store)

	// Spin up the daemon server and register only what the test needs.
	server := daemon.NewServer(socketPath)
	server.RegisterHandler("monitor.stop", monHandler.HandleStop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, server.Start(ctx))
	t.Cleanup(func() { _ = server.Stop() })
	waitForSocketReady(t, socketPath)

	// Pre-insert a MonitorJob with DebounceSeconds=2 directly to bypass the
	// 30s minimum enforced by supervisor.Add. Start() will reload StatusRunning
	// monitors via store.ListByStatus and call launch() — which does NOT re-validate.
	jobNow := time.Now().UTC()
	const jobID = "mon_int_test_001"
	job := &monitor.MonitorJob{
		ID:              jobID,
		Name:            monitorName,
		Argv:            []string{"tail", "-F", tempfile},
		MatchPattern:    "ERROR|WARN",
		Target:          targetAgent,
		Cwd:             tmpDir,
		Env:             map[string]string{},
		DebounceSeconds: 2,
		Status:          monitor.StatusRunning,
		CreatedAt:       jobNow,
		UpdatedAt:       jobNow,
	}
	require.NoError(t, store.Insert(context.Background(), job))

	// Launch supervisor in a goroutine. Start() reloads all StatusRunning
	// monitors and launches their runners, then blocks until ctx is canceled.
	go supervisor.Start(ctx)

	// Give the reload path time to launch the runner and for tail -F to open the file.
	time.Sleep(200 * time.Millisecond)

	// Write 5 "ERROR: boom N" lines with ~100ms gaps to the tempfile.
	logFile, err := os.OpenFile(tempfile, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = logFile.Close() })

	for i := 1; i <= 5; i++ {
		_, err = fmt.Fprintf(logFile, "ERROR: boom %d\n", i)
		require.NoError(t, err)
		time.Sleep(80 * time.Millisecond)
	}

	// Poll the messages table for the leading-edge emit (from_agent = callerID).
	// Timeout 10s to accommodate slow CI.
	require.Eventually(t, func() bool {
		var count int
		row := st.DB().QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM messages WHERE agent_id = ?`,
			callerID,
		)
		_ = row.Scan(&count)
		return count >= 1
	}, 10*time.Second, 200*time.Millisecond, "leading-edge message should appear in DB within 10s")

	// Verify the leading-edge message contains "ERROR: boom 1".
	var firstContent string
	row := st.DB().QueryRowContext(context.Background(),
		`SELECT body_content FROM messages WHERE agent_id = ? ORDER BY created_at ASC LIMIT 1`,
		callerID,
	)
	require.NoError(t, row.Scan(&firstContent))
	assert.Contains(t, firstContent, "ERROR: boom 1", "leading-edge message should contain first match")

	// Sleep 2.5s so the flushTimer (2s window) fires and delivers the trailing summary.
	time.Sleep(2500 * time.Millisecond)

	// Poll for 2 total messages: leading-edge + trailing summary.
	require.Eventually(t, func() bool {
		var count int
		row := st.DB().QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM messages WHERE agent_id = ?`,
			callerID,
		)
		_ = row.Scan(&count)
		return count >= 2
	}, 10*time.Second, 200*time.Millisecond, "trailing summary message should appear in DB within 10s")

	// Assert the trailing summary contains the suppression notice.
	// The debounce window is 2s, and we wrote 5 matching lines.
	// Leading edge consumed line 1; lines 2-5 are suppressed (pendingCount=4).
	// extra = pendingCount - 1 = 3, so the format is:
	//   "<pendingFirst>\n(+3 more matches suppressed in the last 2s)"
	var trailingContent string
	row2 := st.DB().QueryRowContext(context.Background(),
		`SELECT body_content FROM messages WHERE agent_id = ? ORDER BY created_at DESC LIMIT 1`,
		callerID,
	)
	require.NoError(t, row2.Scan(&trailingContent))
	assert.Contains(t, trailingContent, "more matches suppressed in the last",
		"trailing summary should contain suppression notice")

	// Send monitor.stop RPC via the unix socket.
	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	stopRequest := map[string]any{
		"jsonrpc": "2.0",
		"method":  "monitor.stop",
		"params":  map[string]any{"id": jobID},
		"id":      42,
	}
	reqJSON, err := json.Marshal(stopRequest)
	require.NoError(t, err)
	reqJSON = append(reqJSON, '\n')

	_, err = conn.Write(reqJSON)
	require.NoError(t, err)

	// Read and parse the stop response.
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	require.NoError(t, err)

	var rpcResp struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		ID json.RawMessage `json:"id"`
	}
	require.NoError(t, json.Unmarshal(buf[:n], &rpcResp))
	require.Nil(t, rpcResp.Error, "monitor.stop should succeed, got error: %v", rpcResp.Error)

	// Verify the monitor row is gone from the monitors table.
	var monCount int
	monRow := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM monitors WHERE id = ?`, jobID,
	)
	require.NoError(t, monRow.Scan(&monCount))
	assert.Equal(t, 0, monCount, "monitors row should be deleted after stop")
}
