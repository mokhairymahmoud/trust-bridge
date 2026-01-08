// Package billing provides usage metering and billing integration for TrustBridge.
//
// The billing package implements usage tracking, periodic reporting to Azure Marketplace,
// and contract suspension enforcement when billing issues occur.
package billing

import (
	"sync"
	"sync/atomic"
	"time"
)

// UsageMetrics represents billable usage for a billing period.
type UsageMetrics struct {
	// Request counts
	RequestCount uint64 // Total number of requests
	SuccessCount uint64 // Requests with 2xx status
	ErrorCount   uint64 // Requests with 4xx/5xx status

	// Byte counts
	BytesIn  uint64 // Total request body bytes
	BytesOut uint64 // Total response body bytes

	// Period tracking
	PeriodStart time.Time
	PeriodEnd   time.Time
}

// Counter provides thread-safe usage metric collection.
// It uses atomic operations for high-performance increments on the hot path.
type Counter struct {
	// Atomic counters (hot path)
	requestCount uint64
	successCount uint64
	errorCount   uint64
	bytesIn      uint64
	bytesOut     uint64

	// Period tracking (protected by mutex for Snapshot)
	mu          sync.Mutex
	periodStart time.Time
}

// NewCounter creates a new billing counter with the period starting now.
func NewCounter() *Counter {
	return &Counter{
		periodStart: time.Now(),
	}
}

// RecordRequest atomically increments the request count and bytes in.
// This is called for every incoming request.
func (c *Counter) RecordRequest(bytesIn int64) {
	atomic.AddUint64(&c.requestCount, 1)
	if bytesIn > 0 {
		atomic.AddUint64(&c.bytesIn, uint64(bytesIn))
	}
}

// RecordResponse atomically increments response counters based on status code.
// This is called after each response is sent.
func (c *Counter) RecordResponse(status int, bytesOut int64) {
	if bytesOut > 0 {
		atomic.AddUint64(&c.bytesOut, uint64(bytesOut))
	}

	// Categorize by status code
	if status >= 200 && status < 300 {
		atomic.AddUint64(&c.successCount, 1)
	} else if status >= 400 {
		atomic.AddUint64(&c.errorCount, 1)
	}
	// 1xx and 3xx are informational/redirect, not counted as success or error
}

// Snapshot returns a copy of current metrics and resets the counter.
// This is the atomic read-and-reset operation used for billing periods.
// The returned metrics represent usage since the last Snapshot (or creation).
func (c *Counter) Snapshot() UsageMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	// Atomically swap all counters to zero and capture old values
	metrics := UsageMetrics{
		RequestCount: atomic.SwapUint64(&c.requestCount, 0),
		SuccessCount: atomic.SwapUint64(&c.successCount, 0),
		ErrorCount:   atomic.SwapUint64(&c.errorCount, 0),
		BytesIn:      atomic.SwapUint64(&c.bytesIn, 0),
		BytesOut:     atomic.SwapUint64(&c.bytesOut, 0),
		PeriodStart:  c.periodStart,
		PeriodEnd:    now,
	}

	// Start new period
	c.periodStart = now

	return metrics
}

// Peek returns a copy of current metrics without resetting.
// Useful for monitoring and debugging.
func (c *Counter) Peek() UsageMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()

	return UsageMetrics{
		RequestCount: atomic.LoadUint64(&c.requestCount),
		SuccessCount: atomic.LoadUint64(&c.successCount),
		ErrorCount:   atomic.LoadUint64(&c.errorCount),
		BytesIn:      atomic.LoadUint64(&c.bytesIn),
		BytesOut:     atomic.LoadUint64(&c.bytesOut),
		PeriodStart:  c.periodStart,
		PeriodEnd:    time.Now(),
	}
}

// IsZero returns true if no requests have been recorded.
func (m UsageMetrics) IsZero() bool {
	return m.RequestCount == 0 && m.BytesIn == 0 && m.BytesOut == 0
}

// TotalBytes returns the sum of bytes in and out.
func (m UsageMetrics) TotalBytes() uint64 {
	return m.BytesIn + m.BytesOut
}

// Duration returns the length of the billing period.
func (m UsageMetrics) Duration() time.Duration {
	return m.PeriodEnd.Sub(m.PeriodStart)
}
