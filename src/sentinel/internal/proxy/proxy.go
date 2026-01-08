package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"trustbridge/sentinel/internal/state"
)

// ProxyConfig holds configuration for the proxy server.
type ProxyConfig struct {
	PublicAddr string // Listen address (default: 0.0.0.0:8000)
	RuntimeURL string // Backend URL (default: http://127.0.0.1:8081)
	ContractID string // Contract ID for audit logging
	AssetID    string // Asset ID for audit logging
}

// BillingMiddleware defines the interface for billing metrics collection middleware.
type BillingMiddleware interface {
	Wrap(next http.Handler) http.Handler
}

// Server is the reverse proxy HTTP server.
type Server struct {
	machine           *state.Machine
	config            *ProxyConfig
	httpServer        *http.Server
	auditLogger       AuditLogger
	billingMiddleware BillingMiddleware
	logger            *slog.Logger
	mu                sync.Mutex
	started           bool
}

// ServerOption is a functional option for configuring the Server.
type ServerOption func(*Server)

// WithLogger sets the logger for the proxy server.
func WithLogger(logger *slog.Logger) ServerOption {
	return func(s *Server) {
		s.logger = logger
	}
}

// WithAuditLogger sets the audit logger for the proxy server.
func WithAuditLogger(logger AuditLogger) ServerOption {
	return func(s *Server) {
		s.auditLogger = logger
	}
}

// WithBillingMiddleware sets the billing metrics middleware.
func WithBillingMiddleware(middleware BillingMiddleware) ServerOption {
	return func(s *Server) {
		s.billingMiddleware = middleware
	}
}

// NewServer creates a new reverse proxy server.
func NewServer(machine *state.Machine, config *ProxyConfig, opts ...ServerOption) *Server {
	s := &Server{
		machine:     machine,
		config:      config,
		auditLogger: NewNopAuditLogger(),
		logger:      slog.Default(),
	}

	// Apply defaults
	if s.config.PublicAddr == "" {
		s.config.PublicAddr = "0.0.0.0:8000"
	}
	if s.config.RuntimeURL == "" {
		s.config.RuntimeURL = "http://127.0.0.1:8081"
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Start starts the proxy server.
// Returns an error if the state machine is not in the Ready state.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("proxy server already started")
	}

	if !s.machine.IsReady() {
		return fmt.Errorf("cannot start proxy: state is %s, expected Ready", s.machine.CurrentState().String())
	}

	// Parse runtime URL
	target, err := url.Parse(s.config.RuntimeURL)
	if err != nil {
		return fmt.Errorf("invalid runtime URL: %w", err)
	}

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Customize error handler
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		s.logger.Error("Proxy error", "error", err.Error(), "path", r.URL.Path)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}

	// Build handler chain: state check -> billing -> audit -> proxy
	var handler http.Handler = proxy
	handler = AuditMiddleware(s.auditLogger, s.config.ContractID, s.config.AssetID)(handler)
	if s.billingMiddleware != nil {
		handler = s.billingMiddleware.Wrap(handler)
	}
	handler = s.stateCheckMiddleware(handler)

	s.httpServer = &http.Server{
		Addr:         s.config.PublicAddr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	s.started = true

	go func() {
		s.logger.Info("Proxy server starting", "addr", s.config.PublicAddr, "runtime", s.config.RuntimeURL)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Proxy server error", "error", err.Error())
		}
	}()

	return nil
}

// Stop gracefully shuts down the proxy server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	s.started = false

	// Close audit logger
	if s.auditLogger != nil {
		s.auditLogger.Close()
	}

	return s.httpServer.Shutdown(ctx)
}

// Addr returns the configured listen address.
func (s *Server) Addr() string {
	return s.config.PublicAddr
}

// stateCheckMiddleware returns middleware that checks the state machine before proxying.
func (s *Server) stateCheckMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if suspended (billing issue)
		if s.machine.IsSuspended() {
			s.logger.Warn("Request rejected: contract suspended", "path", r.URL.Path)
			http.Error(w, "Forbidden: Contract suspended", http.StatusForbidden)
			return
		}

		// Check if ready
		if !s.machine.IsReady() {
			s.logger.Warn("Request rejected: not ready", "state", s.machine.CurrentState().String(), "path", r.URL.Path)
			http.Error(w, "Service Unavailable: Not ready", http.StatusServiceUnavailable)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Handler returns the HTTP handler for use in testing.
func (s *Server) Handler() http.Handler {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Handler
}
