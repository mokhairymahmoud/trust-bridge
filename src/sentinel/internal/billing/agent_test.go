package billing

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockLogger captures log messages for testing.
type mockLogger struct {
	mu       sync.Mutex
	infos    []string
	errors   []string
	infoKVs  [][]interface{}
	errorKVs [][]interface{}
}

func (l *mockLogger) Info(msg string, keysAndValues ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, msg)
	l.infoKVs = append(l.infoKVs, keysAndValues)
}

func (l *mockLogger) Error(msg string, keysAndValues ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, msg)
	l.errorKVs = append(l.errorKVs, keysAndValues)
}

func (l *mockLogger) InfoCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.infos)
}

func (l *mockLogger) ErrorCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.errors)
}

// mockReporter tracks Report calls for testing.
type mockReporter struct {
	mu          sync.Mutex
	calls       []UsageMetrics
	returnError error
	callCount   int64
}

func (r *mockReporter) Report(ctx context.Context, metrics UsageMetrics) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, metrics)
	atomic.AddInt64(&r.callCount, 1)
	return r.returnError
}

func (r *mockReporter) CallCount() int {
	return int(atomic.LoadInt64(&r.callCount))
}

func (r *mockReporter) LastMetrics() UsageMetrics {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return UsageMetrics{}
	}
	return r.calls[len(r.calls)-1]
}

func TestNewAgent(t *testing.T) {
	counter := NewCounter()
	reporter := &mockReporter{}
	suspend := func(reason string) error { return nil }

	agent := NewAgent(counter, reporter, suspend)

	if agent == nil {
		t.Fatal("NewAgent returned nil")
	}

	// Check defaults
	if agent.config.Interval != DefaultInterval {
		t.Errorf("Interval = %v, want %v", agent.config.Interval, DefaultInterval)
	}
	if agent.config.Dimension != DefaultDimension {
		t.Errorf("Dimension = %q, want %q", agent.config.Dimension, DefaultDimension)
	}
}

func TestNewAgent_WithOptions(t *testing.T) {
	counter := NewCounter()
	reporter := &mockReporter{}
	suspend := func(reason string) error { return nil }
	logger := &mockLogger{}

	config := AgentConfig{
		Interval:   30 * time.Second,
		ContractID: "test-contract",
		AssetID:    "test-asset",
		ResourceID: "test-resource",
		Dimension:  "tokens",
	}

	agent := NewAgent(counter, reporter, suspend,
		WithConfig(config),
		WithLogger(logger),
	)

	if agent.config.Interval != 30*time.Second {
		t.Errorf("Interval = %v, want 30s", agent.config.Interval)
	}
	if agent.config.ContractID != "test-contract" {
		t.Errorf("ContractID = %q, want %q", agent.config.ContractID, "test-contract")
	}
	if agent.config.AssetID != "test-asset" {
		t.Errorf("AssetID = %q, want %q", agent.config.AssetID, "test-asset")
	}
	if agent.config.Dimension != "tokens" {
		t.Errorf("Dimension = %q, want %q", agent.config.Dimension, "tokens")
	}
}

func TestAgent_StartStop(t *testing.T) {
	counter := NewCounter()
	reporter := &mockReporter{}
	suspend := func(reason string) error { return nil }

	agent := NewAgent(counter, reporter, suspend,
		WithConfig(AgentConfig{Interval: 100 * time.Millisecond}),
	)

	// Start
	if err := agent.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	if !agent.IsRunning() {
		t.Error("IsRunning() = false after Start()")
	}

	// Start again should fail
	if err := agent.Start(); err == nil {
		t.Error("Second Start() should fail")
	}

	// Stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := agent.Stop(ctx); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	if agent.IsRunning() {
		t.Error("IsRunning() = true after Stop()")
	}

	// Stop again should be no-op
	if err := agent.Stop(ctx); err != nil {
		t.Fatalf("Second Stop() failed: %v", err)
	}
}

func TestAgent_PeriodicReport(t *testing.T) {
	counter := NewCounter()
	reporter := &mockReporter{}
	suspend := func(reason string) error { return nil }

	agent := NewAgent(counter, reporter, suspend,
		WithConfig(AgentConfig{Interval: 50 * time.Millisecond}),
	)

	// Generate some activity
	counter.RecordRequest(100)
	counter.RecordResponse(200, 500)

	if err := agent.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Wait for at least one report
	time.Sleep(150 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agent.Stop(ctx)

	// Should have at least one report call (possibly including final report)
	if reporter.CallCount() == 0 {
		t.Error("Expected at least one Report call")
	}
}

func TestAgent_ReportNow(t *testing.T) {
	counter := NewCounter()
	reporter := &mockReporter{}
	suspend := func(reason string) error { return nil }

	agent := NewAgent(counter, reporter, suspend)

	// Generate activity
	counter.RecordRequest(100)
	counter.RecordRequest(200)
	counter.RecordResponse(200, 500)

	ctx := context.Background()
	if err := agent.ReportNow(ctx); err != nil {
		t.Fatalf("ReportNow() failed: %v", err)
	}

	if reporter.CallCount() != 1 {
		t.Errorf("CallCount = %d, want 1", reporter.CallCount())
	}

	metrics := reporter.LastMetrics()
	if metrics.RequestCount != 2 {
		t.Errorf("RequestCount = %d, want 2", metrics.RequestCount)
	}
	if metrics.BytesIn != 300 {
		t.Errorf("BytesIn = %d, want 300", metrics.BytesIn)
	}
	if metrics.BytesOut != 500 {
		t.Errorf("BytesOut = %d, want 500", metrics.BytesOut)
	}
}

func TestAgent_SkipsEmptyReport(t *testing.T) {
	counter := NewCounter()
	reporter := &mockReporter{}
	logger := &mockLogger{}
	suspend := func(reason string) error { return nil }

	agent := NewAgent(counter, reporter, suspend, WithLogger(logger))

	// No activity - report should be skipped
	ctx := context.Background()
	if err := agent.ReportNow(ctx); err != nil {
		t.Fatalf("ReportNow() failed: %v", err)
	}

	if reporter.CallCount() != 0 {
		t.Errorf("Expected 0 Report calls for empty metrics, got %d", reporter.CallCount())
	}
}

func TestAgent_SuspendOnError(t *testing.T) {
	counter := NewCounter()
	reporter := &mockReporter{returnError: ErrQuotaExceeded}

	var suspendCalled bool
	var suspendReason string
	suspend := func(reason string) error {
		suspendCalled = true
		suspendReason = reason
		return nil
	}

	agent := NewAgent(counter, reporter, suspend,
		WithConfig(AgentConfig{Interval: 50 * time.Millisecond}),
	)

	// Generate activity
	counter.RecordRequest(100)

	if err := agent.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Wait for report attempt
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agent.Stop(ctx)

	if !suspendCalled {
		t.Error("Expected suspend to be called on ErrQuotaExceeded")
	}
	if suspendReason != ErrQuotaExceeded.Error() {
		t.Errorf("suspendReason = %q, want %q", suspendReason, ErrQuotaExceeded.Error())
	}
}

func TestAgent_SuspendOnSubscriptionInactive(t *testing.T) {
	counter := NewCounter()
	reporter := &mockReporter{returnError: ErrSubscriptionInactive}

	var suspendCalled bool
	suspend := func(reason string) error {
		suspendCalled = true
		return nil
	}

	agent := NewAgent(counter, reporter, suspend,
		WithConfig(AgentConfig{Interval: 50 * time.Millisecond}),
	)

	counter.RecordRequest(100)

	if err := agent.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agent.Stop(ctx)

	if !suspendCalled {
		t.Error("Expected suspend to be called on ErrSubscriptionInactive")
	}
}

func TestAgent_NoSuspendOnTransientError(t *testing.T) {
	counter := NewCounter()
	transientErr := errors.New("network timeout")
	reporter := &mockReporter{returnError: transientErr}

	var suspendCalled bool
	suspend := func(reason string) error {
		suspendCalled = true
		return nil
	}

	agent := NewAgent(counter, reporter, suspend,
		WithConfig(AgentConfig{Interval: 50 * time.Millisecond}),
	)

	counter.RecordRequest(100)

	if err := agent.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agent.Stop(ctx)

	if suspendCalled {
		t.Error("Suspend should not be called for transient errors")
	}
}

func TestAgent_GracefulShutdown(t *testing.T) {
	counter := NewCounter()
	reporter := &mockReporter{}
	suspend := func(reason string) error { return nil }

	agent := NewAgent(counter, reporter, suspend,
		WithConfig(AgentConfig{Interval: 10 * time.Second}), // Long interval
	)

	// Generate activity
	counter.RecordRequest(100)

	if err := agent.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Stop immediately (before first periodic report)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := agent.Stop(ctx); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// Should have called Report during shutdown
	if reporter.CallCount() == 0 {
		t.Error("Expected final report during graceful shutdown")
	}
}

// slowReporter is a reporter that delays before reporting
type slowReporter struct {
	delay time.Duration
}

func (r *slowReporter) Report(ctx context.Context, metrics UsageMetrics) error {
	time.Sleep(r.delay)
	return nil
}

func TestAgent_StopTimeout(t *testing.T) {
	counter := NewCounter()

	// Reporter that takes a long time
	reporter := &slowReporter{delay: 5 * time.Second}

	suspend := func(reason string) error { return nil }

	agent := NewAgent(counter, reporter, suspend,
		WithConfig(AgentConfig{Interval: 10 * time.Millisecond}),
	)

	counter.RecordRequest(100)

	if err := agent.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Wait for report to start
	time.Sleep(50 * time.Millisecond)

	// Stop with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	err := agent.Stop(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("Expected DeadlineExceeded, got %v", err)
	}
}

func TestLogReporter(t *testing.T) {
	logger := &mockLogger{}
	reporter := NewLogReporter(logger)

	metrics := UsageMetrics{
		RequestCount: 100,
		SuccessCount: 95,
		ErrorCount:   5,
		BytesIn:      1000,
		BytesOut:     5000,
		PeriodStart:  time.Now().Add(-time.Minute),
		PeriodEnd:    time.Now(),
	}

	ctx := context.Background()
	if err := reporter.Report(ctx, metrics); err != nil {
		t.Fatalf("Report() failed: %v", err)
	}

	if logger.InfoCount() != 1 {
		t.Errorf("Expected 1 info log, got %d", logger.InfoCount())
	}
}

func TestLogReporter_NilLogger(t *testing.T) {
	reporter := NewLogReporter(nil)

	metrics := UsageMetrics{RequestCount: 1}

	ctx := context.Background()
	// Should not panic with nil logger
	if err := reporter.Report(ctx, metrics); err != nil {
		t.Fatalf("Report() failed: %v", err)
	}
}
