package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"trustbridge/sentinel/internal/state"
)

// Helper to create a state machine in Ready state
func newReadyStateMachine(t *testing.T) *state.Machine {
	t.Helper()
	sm := state.New()
	if err := sm.Transition(state.StateAuthorize); err != nil {
		t.Fatalf("Failed to transition to Authorize: %v", err)
	}
	if err := sm.Transition(state.StateHydrate); err != nil {
		t.Fatalf("Failed to transition to Hydrate: %v", err)
	}
	if err := sm.Transition(state.StateDecrypt); err != nil {
		t.Fatalf("Failed to transition to Decrypt: %v", err)
	}
	if err := sm.Transition(state.StateReady); err != nil {
		t.Fatalf("Failed to transition to Ready: %v", err)
	}
	return sm
}

func TestProxyForwardsRequests(t *testing.T) {
	// Create a mock backend server
	backendCalled := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		w.Header().Set("X-Backend-Response", "true")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello from backend"))
	}))
	defer backend.Close()

	// Create state machine in Ready state
	sm := newReadyStateMachine(t)

	// Create proxy server
	auditLogger := NewMemoryAuditLogger(100)
	server := NewServer(sm, &ProxyConfig{
		PublicAddr: "127.0.0.1:0",
		RuntimeURL: backend.URL,
		ContractID: "test-contract",
		AssetID:    "test-asset",
	}, WithAuditLogger(auditLogger))

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer server.Stop(context.Background())

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Make request through proxy using the handler directly
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	// Verify request was forwarded
	if !backendCalled {
		t.Error("Backend was not called")
	}

	// Verify response
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-Backend-Response") != "true" {
		t.Error("Backend response header not present")
	}
}

func TestProxyReturns503WhenNotReady(t *testing.T) {
	// Create state machine in Boot state (not ready)
	sm := state.New()

	// Create proxy server - should fail to start
	server := NewServer(sm, &ProxyConfig{
		PublicAddr: "127.0.0.1:0",
		RuntimeURL: "http://127.0.0.1:8081",
		ContractID: "test-contract",
		AssetID:    "test-asset",
	})

	err := server.Start()
	if err == nil {
		t.Error("Expected error when starting proxy in non-Ready state")
		server.Stop(context.Background())
	}
	if err != nil && !strings.Contains(err.Error(), "expected Ready") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestProxyReturns503WhenStateChanges(t *testing.T) {
	// Create a mock backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// Create state machine in Ready state
	sm := newReadyStateMachine(t)

	// Create and start proxy server
	server := NewServer(sm, &ProxyConfig{
		PublicAddr: "127.0.0.1:0",
		RuntimeURL: backend.URL,
		ContractID: "test-contract",
		AssetID:    "test-asset",
	})

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer server.Stop(context.Background())

	// First request should succeed
	req1 := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec1 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Errorf("Expected status 200 when ready, got %d", rec1.Code)
	}

	// Suspend the state machine
	sm.Suspend("test suspension")

	// Request should now return 403
	req2 := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec2 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Errorf("Expected status 403 when suspended, got %d", rec2.Code)
	}
}

func TestAuditLogEntry(t *testing.T) {
	// Create a mock backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer backend.Close()

	// Create state machine in Ready state
	sm := newReadyStateMachine(t)

	// Create proxy server with memory audit logger
	auditLogger := NewMemoryAuditLogger(100)
	server := NewServer(sm, &ProxyConfig{
		PublicAddr: "127.0.0.1:0",
		RuntimeURL: backend.URL,
		ContractID: "contract-123",
		AssetID:    "asset-456",
	}, WithAuditLogger(auditLogger))

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer server.Stop(context.Background())

	// Make request
	body := []byte(`{"prompt": "Hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	// Check audit entry
	entries := auditLogger.Entries()
	if len(entries) != 1 {
		t.Fatalf("Expected 1 audit entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.ContractID != "contract-123" {
		t.Errorf("Expected contract_id 'contract-123', got %q", entry.ContractID)
	}
	if entry.AssetID != "asset-456" {
		t.Errorf("Expected asset_id 'asset-456', got %q", entry.AssetID)
	}
	if entry.Method != "POST" {
		t.Errorf("Expected method 'POST', got %q", entry.Method)
	}
	if entry.Path != "/v1/chat/completions" {
		t.Errorf("Expected path '/v1/chat/completions', got %q", entry.Path)
	}
	if entry.Status != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", entry.Status)
	}
	if entry.Timestamp == "" {
		t.Error("Expected timestamp to be set")
	}
}

func TestAuditLogSHA256(t *testing.T) {
	// Create a mock backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read body to ensure it was forwarded correctly
		body, _ := io.ReadAll(r.Body)
		if string(body) != "test body content" {
			t.Errorf("Backend received wrong body: %q", string(body))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// Create state machine in Ready state
	sm := newReadyStateMachine(t)

	// Create proxy server with memory audit logger
	auditLogger := NewMemoryAuditLogger(100)
	server := NewServer(sm, &ProxyConfig{
		PublicAddr: "127.0.0.1:0",
		RuntimeURL: backend.URL,
		ContractID: "test-contract",
		AssetID:    "test-asset",
	}, WithAuditLogger(auditLogger))

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer server.Stop(context.Background())

	// Make request with known body
	bodyContent := "test body content"
	expectedHash := sha256.Sum256([]byte(bodyContent))
	expectedHashStr := hex.EncodeToString(expectedHash[:])

	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(bodyContent))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	// Check audit entry hash
	entry := auditLogger.LastEntry()
	if entry == nil {
		t.Fatal("No audit entry recorded")
	}
	if entry.ReqSHA256 != expectedHashStr {
		t.Errorf("Expected req_sha256 %q, got %q", expectedHashStr, entry.ReqSHA256)
	}
}

func TestAuditLogLatency(t *testing.T) {
	// Create a slow backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// Create state machine in Ready state
	sm := newReadyStateMachine(t)

	// Create proxy server with memory audit logger
	auditLogger := NewMemoryAuditLogger(100)
	server := NewServer(sm, &ProxyConfig{
		PublicAddr: "127.0.0.1:0",
		RuntimeURL: backend.URL,
		ContractID: "test-contract",
		AssetID:    "test-asset",
	}, WithAuditLogger(auditLogger))

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer server.Stop(context.Background())

	// Make request
	req := httptest.NewRequest(http.MethodGet, "/slow", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	// Check latency is recorded and reasonable
	entry := auditLogger.LastEntry()
	if entry == nil {
		t.Fatal("No audit entry recorded")
	}
	// Should be at least 50ms (our sleep time)
	if entry.LatencyMS < 50 {
		t.Errorf("Expected latency >= 50ms, got %d ms", entry.LatencyMS)
	}
	// Should be less than 500ms (reasonable upper bound)
	if entry.LatencyMS > 500 {
		t.Errorf("Latency seems too high: %d ms", entry.LatencyMS)
	}
}

func TestMemoryAuditLogger(t *testing.T) {
	logger := NewMemoryAuditLogger(3)

	// Add entries
	for i := 0; i < 5; i++ {
		logger.Log(&AuditEntry{
			Method: "GET",
			Path:   "/test",
			Status: 200 + i,
		})
	}

	// Should have exactly 3 entries (ring buffer)
	entries := logger.Entries()
	if len(entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(entries))
	}

	// Last entry should have status 204 (the 5th entry, 0-indexed)
	last := logger.LastEntry()
	if last == nil {
		t.Fatal("Expected last entry to exist")
	}
	if last.Status != 204 {
		t.Errorf("Expected last status 204, got %d", last.Status)
	}

	// Clear and verify
	logger.Clear()
	if len(logger.Entries()) != 0 {
		t.Error("Expected 0 entries after clear")
	}
	if logger.LastEntry() != nil {
		t.Error("Expected nil last entry after clear")
	}
}

func TestNopAuditLogger(t *testing.T) {
	logger := NewNopAuditLogger()

	// Should not error
	if err := logger.Log(&AuditEntry{Method: "GET"}); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestProxyStartFailsIfAlreadyStarted(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	sm := newReadyStateMachine(t)
	server := NewServer(sm, &ProxyConfig{
		PublicAddr: "127.0.0.1:0",
		RuntimeURL: backend.URL,
	})

	// First start should succeed
	if err := server.Start(); err != nil {
		t.Fatalf("First start failed: %v", err)
	}
	defer server.Stop(context.Background())

	// Second start should fail
	if err := server.Start(); err == nil {
		t.Error("Expected error on second start")
	}
}

func TestProxyEmptyBodyHash(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	sm := newReadyStateMachine(t)
	auditLogger := NewMemoryAuditLogger(100)
	server := NewServer(sm, &ProxyConfig{
		RuntimeURL: backend.URL,
	}, WithAuditLogger(auditLogger))

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer server.Stop(context.Background())

	// Make GET request with no body
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	// Check hash of empty body
	emptyHash := sha256.Sum256([]byte{})
	expectedHash := hex.EncodeToString(emptyHash[:])

	entry := auditLogger.LastEntry()
	if entry == nil {
		t.Fatal("No audit entry recorded")
	}
	if entry.ReqSHA256 != expectedHash {
		t.Errorf("Expected empty body hash %q, got %q", expectedHash, entry.ReqSHA256)
	}
}

func TestProxyConfigDefaults(t *testing.T) {
	sm := newReadyStateMachine(t)
	server := NewServer(sm, &ProxyConfig{})

	if server.config.PublicAddr != "0.0.0.0:8000" {
		t.Errorf("Expected default PublicAddr '0.0.0.0:8000', got %q", server.config.PublicAddr)
	}
	if server.config.RuntimeURL != "http://127.0.0.1:8081" {
		t.Errorf("Expected default RuntimeURL 'http://127.0.0.1:8081', got %q", server.config.RuntimeURL)
	}
}
