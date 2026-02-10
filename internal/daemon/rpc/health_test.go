package rpc

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestHealthHandler(t *testing.T) {
	startTime := time.Now()
	version := "1.0.0"
	repoID := "test-repo-123"

	handler := NewHealthHandler(startTime, version, repoID)

	// Wait a bit to accumulate uptime
	time.Sleep(10 * time.Millisecond)

	// Call handler
	ctx := context.Background()
	result, err := handler.Handle(ctx, json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	// Check result type
	response, ok := result.(HealthResponse)
	if !ok {
		t.Fatalf("expected HealthResponse, got %T", result)
	}

	// Verify status
	if response.Status != "ok" {
		t.Errorf("expected status 'ok', got %s", response.Status)
	}

	// Verify uptime is positive
	if response.Uptime <= 0 {
		t.Errorf("expected positive uptime, got %d", response.Uptime)
	}

	// Verify uptime is at least the sleep duration
	if response.Uptime < 10 {
		t.Errorf("expected uptime >= 10ms, got %d", response.Uptime)
	}

	// Verify version
	if response.Version != version {
		t.Errorf("expected version %s, got %s", version, response.Version)
	}

	// Verify repo ID
	if response.RepoID != repoID {
		t.Errorf("expected repo ID %s, got %s", repoID, response.RepoID)
	}

	// Verify sync state
	if response.SyncState != "synced" {
		t.Errorf("expected sync state 'synced', got %s", response.SyncState)
	}
}

func TestHealthHandlerUptime(t *testing.T) {
	startTime := time.Now().Add(-5 * time.Second)
	handler := NewHealthHandler(startTime, "1.0.0", "test-repo")

	ctx := context.Background()
	result, err := handler.Handle(ctx, json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	response, ok := result.(HealthResponse)
	if !ok {
		t.Fatalf("expected HealthResponse, got %T", result)
	}

	// Verify uptime is approximately 5 seconds (5000ms)
	if response.Uptime < 5000 {
		t.Errorf("expected uptime >= 5000ms, got %d", response.Uptime)
	}

	if response.Uptime > 6000 {
		t.Errorf("expected uptime < 6000ms, got %d", response.Uptime)
	}
}

func TestHealthHandlerJSON(t *testing.T) {
	startTime := time.Now()
	handler := NewHealthHandler(startTime, "1.0.0", "test-repo")

	ctx := context.Background()
	result, err := handler.Handle(ctx, json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	// Marshal result to JSON
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	// Unmarshal back
	var response HealthResponse
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Verify fields are present
	if response.Status == "" {
		t.Error("status field is empty")
	}
	if response.Version == "" {
		t.Error("version field is empty")
	}
	if response.RepoID == "" {
		t.Error("repo_id field is empty")
	}
	if response.SyncState == "" {
		t.Error("sync_state field is empty")
	}
}
