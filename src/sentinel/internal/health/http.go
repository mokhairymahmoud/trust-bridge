// Package health provides HTTP health check endpoints for the TrustBridge Sentinel.
//
// The health server exposes endpoints for Kubernetes probes and status monitoring:
//   - GET /health    - Liveness probe (200 if Ready, 503 otherwise)
//   - GET /readiness - Readiness probe (200 if state >= Decrypt)
//   - GET /status    - JSON status with state, asset_id, and uptime
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"trustbridge/sentinel/internal/state"
)

// Server is the health check HTTP server.
type Server struct {
	machine    *state.Machine
	addr       string
	httpServer *http.Server
	mu         sync.Mutex
	started    bool
}

// ServerOption is a functional option for configuring the Server.
type ServerOption func(*Server)

// WithAddr sets the listen address for the health server.
func WithAddr(addr string) ServerOption {
	return func(s *Server) {
		s.addr = addr
	}
}

// NewServer creates a new health check server.
func NewServer(machine *state.Machine, opts ...ServerOption) *Server {
	s := &Server{
		machine: machine,
		addr:    "0.0.0.0:8001",
	}

	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/readiness", s.handleReadiness)
	mux.HandleFunc("/status", s.handleStatus)

	s.httpServer = &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

// Addr returns the configured listen address.
func (s *Server) Addr() string {
	return s.addr
}

// Start starts the health server in a goroutine.
// Returns an error if the server is already started.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("health server already started")
	}

	s.started = true

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Log error but don't crash - health server is non-critical
			fmt.Printf("Health server error: %v\n", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the health server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	s.started = false
	return s.httpServer.Shutdown(ctx)
}

// handleHealth handles the /health endpoint (liveness probe).
// Returns 200 OK if the state is Ready, 503 Service Unavailable otherwise.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.machine.IsReady() {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
		return
	}

	w.WriteHeader(http.StatusServiceUnavailable)
	w.Write([]byte(fmt.Sprintf("Not ready: %s", s.machine.CurrentState().String())))
}

// handleReadiness handles the /readiness endpoint (readiness probe).
// Returns 200 OK if the state is at least Decrypt (model weights are available),
// 503 Service Unavailable otherwise.
func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Ready for traffic if we're at least in Decrypt state
	// (model weights are being/have been decrypted)
	if s.machine.IsAtLeast(state.StateDecrypt) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Ready"))
		return
	}

	w.WriteHeader(http.StatusServiceUnavailable)
	w.Write([]byte(fmt.Sprintf("Not ready: %s", s.machine.CurrentState().String())))
}

// StatusResponse is the JSON response for the /status endpoint.
type StatusResponse struct {
	State     string `json:"state"`
	AssetID   string `json:"asset_id,omitempty"`
	Uptime    string `json:"uptime"`
	UptimeMs  int64  `json:"uptime_ms"`
	StartTime string `json:"start_time"`
	Ready     bool   `json:"ready"`
	Suspended bool   `json:"suspended"`
}

// handleStatus handles the /status endpoint.
// Returns JSON with current state, asset_id, and uptime.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := s.machine.Status()
	response := StatusResponse{
		State:     status.State,
		AssetID:   status.AssetID,
		Uptime:    status.UptimeStr,
		UptimeMs:  status.Uptime.Milliseconds(),
		StartTime: status.StartTime.Format(time.RFC3339),
		Ready:     s.machine.IsReady(),
		Suspended: s.machine.IsSuspended(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// Handler returns the HTTP handler for use in testing or embedding.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}
