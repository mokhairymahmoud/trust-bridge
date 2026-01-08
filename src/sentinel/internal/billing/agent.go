package billing

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Default configuration values
const (
	DefaultInterval  = 60 * time.Second
	DefaultDimension = "requests"
)

// MeterReporter defines the interface for reporting usage metrics.
type MeterReporter interface {
	// Report sends usage metrics to the billing backend.
	// Returns nil on success, or an error indicating a problem.
	// Errors that should trigger suspension (ErrQuotaExceeded, ErrSubscriptionInactive)
	// will be detected by the agent and handled appropriately.
	Report(ctx context.Context, metrics UsageMetrics) error
}

// SuspendFunc is called when billing indicates the contract should be suspended.
type SuspendFunc func(reason string) error

// Logger interface for agent logging.
type Logger interface {
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}

// defaultLogger is a no-op logger used when no logger is provided.
type defaultLogger struct{}

func (defaultLogger) Info(msg string, keysAndValues ...interface{})  {}
func (defaultLogger) Error(msg string, keysAndValues ...interface{}) {}

// AgentConfig holds configuration for the billing agent.
type AgentConfig struct {
	Interval   time.Duration // Reporting interval (default: 60s)
	ContractID string        // Contract identifier
	AssetID    string        // Asset identifier
	ResourceID string        // Azure resource ID for metering
	Dimension  string        // Billing dimension (e.g., "requests", "tokens")
}

// Agent periodically reports usage metrics to the billing backend.
type Agent struct {
	config   AgentConfig
	counter  *Counter
	reporter MeterReporter
	suspend  SuspendFunc
	logger   Logger

	mu       sync.Mutex
	running  bool
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// AgentOption configures the Agent.
type AgentOption func(*Agent)

// WithConfig sets the agent configuration.
func WithConfig(cfg AgentConfig) AgentOption {
	return func(a *Agent) {
		a.config = cfg
	}
}

// WithLogger sets the logger.
func WithLogger(logger Logger) AgentOption {
	return func(a *Agent) {
		if logger != nil {
			a.logger = logger
		}
	}
}

// NewAgent creates a new billing agent.
// The counter is used to collect metrics, reporter sends them to the billing backend,
// and suspend is called when billing errors require contract suspension.
func NewAgent(counter *Counter, reporter MeterReporter, suspend SuspendFunc, opts ...AgentOption) *Agent {
	a := &Agent{
		config: AgentConfig{
			Interval:  DefaultInterval,
			Dimension: DefaultDimension,
		},
		counter:  counter,
		reporter: reporter,
		suspend:  suspend,
		logger:   defaultLogger{},
	}

	for _, opt := range opts {
		opt(a)
	}

	// Apply defaults if not set
	if a.config.Interval <= 0 {
		a.config.Interval = DefaultInterval
	}
	if a.config.Dimension == "" {
		a.config.Dimension = DefaultDimension
	}

	return a
}

// Start begins the periodic reporting loop.
// Returns an error if the agent is already running.
func (a *Agent) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.running {
		return errors.New("billing agent already running")
	}

	a.running = true
	a.stopCh = make(chan struct{})
	a.doneCh = make(chan struct{})

	go a.runLoop()

	a.logger.Info("Billing agent started",
		"interval", a.config.Interval.String(),
		"dimension", a.config.Dimension,
		"contract_id", a.config.ContractID,
		"asset_id", a.config.AssetID,
	)

	return nil
}

// Stop gracefully stops the agent, completing any in-flight report.
// The context can be used to set a deadline for shutdown.
func (a *Agent) Stop(ctx context.Context) error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return nil
	}
	a.running = false
	close(a.stopCh)
	doneCh := a.doneCh
	a.mu.Unlock()

	// Wait for the run loop to finish
	select {
	case <-doneCh:
		a.logger.Info("Billing agent stopped")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReportNow triggers an immediate report.
// This is useful for graceful shutdown to ensure the final metrics are reported.
func (a *Agent) ReportNow(ctx context.Context) error {
	return a.doReport(ctx)
}

// IsRunning returns whether the agent is currently running.
func (a *Agent) IsRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

// runLoop is the main periodic reporting loop.
func (a *Agent) runLoop() {
	defer close(a.doneCh)

	ticker := time.NewTicker(a.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := a.doReport(ctx); err != nil {
				a.handleReportError(err)
			}
			cancel()

		case <-a.stopCh:
			// Final report before shutdown
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := a.doReport(ctx); err != nil {
				a.logger.Error("Final billing report failed", "error", err.Error())
			}
			cancel()
			return
		}
	}
}

// doReport takes a snapshot and reports metrics.
func (a *Agent) doReport(ctx context.Context) error {
	metrics := a.counter.Snapshot()

	// Skip reporting if no activity
	if metrics.IsZero() {
		a.logger.Info("No billing activity to report",
			"period_start", metrics.PeriodStart.Format(time.RFC3339),
			"period_end", metrics.PeriodEnd.Format(time.RFC3339),
		)
		return nil
	}

	a.logger.Info("Reporting billing metrics",
		"requests", metrics.RequestCount,
		"success", metrics.SuccessCount,
		"errors", metrics.ErrorCount,
		"bytes_in", metrics.BytesIn,
		"bytes_out", metrics.BytesOut,
		"period_duration", metrics.Duration().String(),
	)

	if err := a.reporter.Report(ctx, metrics); err != nil {
		return err
	}

	return nil
}

// handleReportError processes errors from the reporter and triggers suspension if needed.
func (a *Agent) handleReportError(err error) {
	if IsSuspendableError(err) {
		a.logger.Error("Billing error requires suspension",
			"error", err.Error(),
			"contract_id", a.config.ContractID,
		)
		if a.suspend != nil {
			if suspendErr := a.suspend(err.Error()); suspendErr != nil {
				a.logger.Error("Failed to suspend contract", "error", suspendErr.Error())
			}
		}
	} else {
		// Log non-fatal errors but continue operation
		a.logger.Error("Billing report failed",
			"error", err.Error(),
			"contract_id", a.config.ContractID,
		)
	}
}

// LogReporter is a stub reporter that logs metrics without calling external API.
// Useful for testing and development.
type LogReporter struct {
	logger Logger
}

// NewLogReporter creates a new log-based reporter.
func NewLogReporter(logger Logger) *LogReporter {
	if logger == nil {
		logger = defaultLogger{}
	}
	return &LogReporter{logger: logger}
}

// Report logs the metrics without making external API calls.
func (r *LogReporter) Report(ctx context.Context, metrics UsageMetrics) error {
	r.logger.Info("Billing report (stub)",
		"requests", metrics.RequestCount,
		"success", metrics.SuccessCount,
		"errors", metrics.ErrorCount,
		"bytes_in", metrics.BytesIn,
		"bytes_out", metrics.BytesOut,
		"total_bytes", metrics.TotalBytes(),
		"period_start", metrics.PeriodStart.Format(time.RFC3339),
		"period_end", metrics.PeriodEnd.Format(time.RFC3339),
		"duration", metrics.Duration().String(),
	)
	return nil
}
