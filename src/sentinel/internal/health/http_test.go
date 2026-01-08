package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"trustbridge/sentinel/internal/state"
)

func TestNewServer(t *testing.T) {
	m := state.New()
	s := NewServer(m)

	if s.Addr() != "0.0.0.0:8001" {
		t.Errorf("Default addr = %q, want %q", s.Addr(), "0.0.0.0:8001")
	}

	// With custom addr
	s2 := NewServer(m, WithAddr("127.0.0.1:9000"))
	if s2.Addr() != "127.0.0.1:9000" {
		t.Errorf("Custom addr = %q, want %q", s2.Addr(), "127.0.0.1:9000")
	}
}

func TestHealthEndpoint_Boot(t *testing.T) {
	m := state.New()
	s := NewServer(m)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /health (Boot) status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHealthEndpoint_Ready(t *testing.T) {
	m := state.New()

	// Advance to Ready
	m.Transition(state.StateAuthorize)
	m.Transition(state.StateHydrate)
	m.Transition(state.StateDecrypt)
	m.Transition(state.StateReady)

	s := NewServer(m)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /health (Ready) status = %d, want %d", rec.Code, http.StatusOK)
	}

	if rec.Body.String() != "OK" {
		t.Errorf("GET /health (Ready) body = %q, want %q", rec.Body.String(), "OK")
	}
}

func TestHealthEndpoint_Suspended(t *testing.T) {
	m := state.New()
	m.Suspend("test error")

	s := NewServer(m)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /health (Suspended) status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHealthEndpoint_MethodNotAllowed(t *testing.T) {
	m := state.New()
	s := NewServer(m)

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/health", nil)
			rec := httptest.NewRecorder()

			s.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s /health status = %d, want %d", method, rec.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestReadinessEndpoint_BeforeDecrypt(t *testing.T) {
	tests := []struct {
		name  string
		state state.State
	}{
		{"Boot", state.StateBoot},
		{"Authorize", state.StateAuthorize},
		{"Hydrate", state.StateHydrate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := state.New()

			// Advance to the target state
			for s := state.StateBoot; s < tt.state; s++ {
				if err := m.Transition(s + 1); err != nil {
					t.Fatalf("Failed to reach state: %v", err)
				}
			}

			s := NewServer(m)

			req := httptest.NewRequest(http.MethodGet, "/readiness", nil)
			rec := httptest.NewRecorder()

			s.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("GET /readiness (%s) status = %d, want %d", tt.name, rec.Code, http.StatusServiceUnavailable)
			}
		})
	}
}

func TestReadinessEndpoint_AtLeastDecrypt(t *testing.T) {
	tests := []struct {
		name  string
		state state.State
	}{
		{"Decrypt", state.StateDecrypt},
		{"Ready", state.StateReady},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := state.New()

			// Advance to the target state
			for s := state.StateBoot; s < tt.state; s++ {
				if err := m.Transition(s + 1); err != nil {
					t.Fatalf("Failed to reach state: %v", err)
				}
			}

			s := NewServer(m)

			req := httptest.NewRequest(http.MethodGet, "/readiness", nil)
			rec := httptest.NewRecorder()

			s.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("GET /readiness (%s) status = %d, want %d", tt.name, rec.Code, http.StatusOK)
			}

			if rec.Body.String() != "Ready" {
				t.Errorf("GET /readiness (%s) body = %q, want %q", tt.name, rec.Body.String(), "Ready")
			}
		})
	}
}

func TestReadinessEndpoint_Suspended(t *testing.T) {
	m := state.New()

	// Advance to Ready first
	m.Transition(state.StateAuthorize)
	m.Transition(state.StateHydrate)
	m.Transition(state.StateDecrypt)
	m.Transition(state.StateReady)

	// Then suspend
	m.Suspend("test error")

	s := NewServer(m)

	req := httptest.NewRequest(http.MethodGet, "/readiness", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /readiness (Suspended) status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestReadinessEndpoint_MethodNotAllowed(t *testing.T) {
	m := state.New()
	s := NewServer(m)

	req := httptest.NewRequest(http.MethodPost, "/readiness", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /readiness status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestStatusEndpoint(t *testing.T) {
	m := state.New(state.WithAssetID("test-asset-123"))

	s := NewServer(m)

	// Wait a bit to ensure uptime > 0
	time.Sleep(time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /status status = %d, want %d", rec.Code, http.StatusOK)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}

	var response StatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if response.State != "Boot" {
		t.Errorf("Response.State = %q, want %q", response.State, "Boot")
	}

	if response.AssetID != "test-asset-123" {
		t.Errorf("Response.AssetID = %q, want %q", response.AssetID, "test-asset-123")
	}

	if response.UptimeMs <= 0 {
		t.Error("Response.UptimeMs should be positive")
	}

	if response.Ready {
		t.Error("Response.Ready should be false in Boot state")
	}

	if response.Suspended {
		t.Error("Response.Suspended should be false in Boot state")
	}
}

func TestStatusEndpoint_Ready(t *testing.T) {
	m := state.New(state.WithAssetID("ready-asset"))

	// Advance to Ready
	m.Transition(state.StateAuthorize)
	m.Transition(state.StateHydrate)
	m.Transition(state.StateDecrypt)
	m.Transition(state.StateReady)

	s := NewServer(m)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	var response StatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if response.State != "Ready" {
		t.Errorf("Response.State = %q, want %q", response.State, "Ready")
	}

	if !response.Ready {
		t.Error("Response.Ready should be true")
	}

	if response.Suspended {
		t.Error("Response.Suspended should be false")
	}
}

func TestStatusEndpoint_Suspended(t *testing.T) {
	m := state.New()
	m.Suspend("test error")

	s := NewServer(m)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	var response StatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if response.State != "Suspended" {
		t.Errorf("Response.State = %q, want %q", response.State, "Suspended")
	}

	if response.Ready {
		t.Error("Response.Ready should be false when Suspended")
	}

	if !response.Suspended {
		t.Error("Response.Suspended should be true")
	}
}

func TestStatusEndpoint_MethodNotAllowed(t *testing.T) {
	m := state.New()
	s := NewServer(m)

	req := httptest.NewRequest(http.MethodPost, "/status", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /status status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestServer_StartStop(t *testing.T) {
	m := state.New()
	s := NewServer(m, WithAddr("127.0.0.1:0")) // Use random port

	// Start server
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Second start should fail
	if err := s.Start(); err == nil {
		t.Error("Second Start() should return error")
	}

	// Give server time to start
	time.Sleep(10 * time.Millisecond)

	// Stop server
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := s.Stop(ctx); err != nil {
		t.Errorf("Stop() error = %v", err)
	}

	// Second stop should be OK (idempotent)
	if err := s.Stop(ctx); err != nil {
		t.Errorf("Second Stop() error = %v", err)
	}
}

func TestHealthEndpoint_AllStates(t *testing.T) {
	tests := []struct {
		name           string
		setupFunc      func(*state.Machine)
		healthStatus   int
		readyStatus    int
	}{
		{
			name:         "Boot",
			setupFunc:    func(m *state.Machine) {},
			healthStatus: http.StatusServiceUnavailable,
			readyStatus:  http.StatusServiceUnavailable,
		},
		{
			name: "Authorize",
			setupFunc: func(m *state.Machine) {
				m.Transition(state.StateAuthorize)
			},
			healthStatus: http.StatusServiceUnavailable,
			readyStatus:  http.StatusServiceUnavailable,
		},
		{
			name: "Hydrate",
			setupFunc: func(m *state.Machine) {
				m.Transition(state.StateAuthorize)
				m.Transition(state.StateHydrate)
			},
			healthStatus: http.StatusServiceUnavailable,
			readyStatus:  http.StatusServiceUnavailable,
		},
		{
			name: "Decrypt",
			setupFunc: func(m *state.Machine) {
				m.Transition(state.StateAuthorize)
				m.Transition(state.StateHydrate)
				m.Transition(state.StateDecrypt)
			},
			healthStatus: http.StatusServiceUnavailable,
			readyStatus:  http.StatusOK,
		},
		{
			name: "Ready",
			setupFunc: func(m *state.Machine) {
				m.Transition(state.StateAuthorize)
				m.Transition(state.StateHydrate)
				m.Transition(state.StateDecrypt)
				m.Transition(state.StateReady)
			},
			healthStatus: http.StatusOK,
			readyStatus:  http.StatusOK,
		},
		{
			name: "Suspended",
			setupFunc: func(m *state.Machine) {
				m.Suspend("error")
			},
			healthStatus: http.StatusServiceUnavailable,
			readyStatus:  http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := state.New()
			tt.setupFunc(m)
			s := NewServer(m)

			// Test /health
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.healthStatus {
				t.Errorf("/health status = %d, want %d", rec.Code, tt.healthStatus)
			}

			// Test /readiness
			req = httptest.NewRequest(http.MethodGet, "/readiness", nil)
			rec = httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.readyStatus {
				t.Errorf("/readiness status = %d, want %d", rec.Code, tt.readyStatus)
			}
		})
	}
}

func BenchmarkHealthEndpoint(b *testing.B) {
	m := state.New()
	m.Transition(state.StateAuthorize)
	m.Transition(state.StateHydrate)
	m.Transition(state.StateDecrypt)
	m.Transition(state.StateReady)

	s := NewServer(m)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
	}
}

func BenchmarkStatusEndpoint(b *testing.B) {
	m := state.New(state.WithAssetID("benchmark-asset"))
	s := NewServer(m)
	req := httptest.NewRequest(http.MethodGet, "/status", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
	}
}
