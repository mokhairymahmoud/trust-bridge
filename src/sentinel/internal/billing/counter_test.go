package billing

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewCounter(t *testing.T) {
	before := time.Now()
	c := NewCounter()
	after := time.Now()

	if c == nil {
		t.Fatal("NewCounter returned nil")
	}

	// Period start should be between before and after
	if c.periodStart.Before(before) || c.periodStart.After(after) {
		t.Errorf("periodStart %v not between %v and %v", c.periodStart, before, after)
	}

	// All counters should be zero
	metrics := c.Peek()
	if metrics.RequestCount != 0 {
		t.Errorf("RequestCount = %d, want 0", metrics.RequestCount)
	}
	if metrics.SuccessCount != 0 {
		t.Errorf("SuccessCount = %d, want 0", metrics.SuccessCount)
	}
	if metrics.ErrorCount != 0 {
		t.Errorf("ErrorCount = %d, want 0", metrics.ErrorCount)
	}
	if metrics.BytesIn != 0 {
		t.Errorf("BytesIn = %d, want 0", metrics.BytesIn)
	}
	if metrics.BytesOut != 0 {
		t.Errorf("BytesOut = %d, want 0", metrics.BytesOut)
	}
}

func TestCounter_RecordRequest(t *testing.T) {
	c := NewCounter()

	// Record some requests
	c.RecordRequest(100)
	c.RecordRequest(200)
	c.RecordRequest(0) // Zero bytes
	c.RecordRequest(-1) // Negative bytes (should be ignored)

	metrics := c.Peek()
	if metrics.RequestCount != 4 {
		t.Errorf("RequestCount = %d, want 4", metrics.RequestCount)
	}
	if metrics.BytesIn != 300 {
		t.Errorf("BytesIn = %d, want 300", metrics.BytesIn)
	}
}

func TestCounter_RecordResponse(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		bytesOut     int64
		wantSuccess  uint64
		wantError    uint64
		wantBytesOut uint64
	}{
		{
			name:         "200 OK",
			status:       200,
			bytesOut:     100,
			wantSuccess:  1,
			wantError:    0,
			wantBytesOut: 100,
		},
		{
			name:         "201 Created",
			status:       201,
			bytesOut:     50,
			wantSuccess:  1,
			wantError:    0,
			wantBytesOut: 50,
		},
		{
			name:         "400 Bad Request",
			status:       400,
			bytesOut:     25,
			wantSuccess:  0,
			wantError:    1,
			wantBytesOut: 25,
		},
		{
			name:         "500 Internal Server Error",
			status:       500,
			bytesOut:     10,
			wantSuccess:  0,
			wantError:    1,
			wantBytesOut: 10,
		},
		{
			name:         "301 Redirect (not counted)",
			status:       301,
			bytesOut:     0,
			wantSuccess:  0,
			wantError:    0,
			wantBytesOut: 0,
		},
		{
			name:         "100 Continue (not counted)",
			status:       100,
			bytesOut:     0,
			wantSuccess:  0,
			wantError:    0,
			wantBytesOut: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCounter()
			c.RecordResponse(tt.status, tt.bytesOut)

			metrics := c.Peek()
			if metrics.SuccessCount != tt.wantSuccess {
				t.Errorf("SuccessCount = %d, want %d", metrics.SuccessCount, tt.wantSuccess)
			}
			if metrics.ErrorCount != tt.wantError {
				t.Errorf("ErrorCount = %d, want %d", metrics.ErrorCount, tt.wantError)
			}
			if metrics.BytesOut != tt.wantBytesOut {
				t.Errorf("BytesOut = %d, want %d", metrics.BytesOut, tt.wantBytesOut)
			}
		})
	}
}

func TestCounter_Snapshot(t *testing.T) {
	c := NewCounter()

	// Record some activity
	c.RecordRequest(100)
	c.RecordRequest(200)
	c.RecordResponse(200, 500)
	c.RecordResponse(400, 50)

	// Take snapshot
	metrics := c.Snapshot()

	// Verify metrics
	if metrics.RequestCount != 2 {
		t.Errorf("RequestCount = %d, want 2", metrics.RequestCount)
	}
	if metrics.BytesIn != 300 {
		t.Errorf("BytesIn = %d, want 300", metrics.BytesIn)
	}
	if metrics.BytesOut != 550 {
		t.Errorf("BytesOut = %d, want 550", metrics.BytesOut)
	}
	if metrics.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", metrics.SuccessCount)
	}
	if metrics.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", metrics.ErrorCount)
	}

	// Verify counter is reset
	afterSnapshot := c.Peek()
	if afterSnapshot.RequestCount != 0 {
		t.Errorf("After snapshot, RequestCount = %d, want 0", afterSnapshot.RequestCount)
	}
	if afterSnapshot.BytesIn != 0 {
		t.Errorf("After snapshot, BytesIn = %d, want 0", afterSnapshot.BytesIn)
	}
	if afterSnapshot.BytesOut != 0 {
		t.Errorf("After snapshot, BytesOut = %d, want 0", afterSnapshot.BytesOut)
	}
	if afterSnapshot.SuccessCount != 0 {
		t.Errorf("After snapshot, SuccessCount = %d, want 0", afterSnapshot.SuccessCount)
	}
	if afterSnapshot.ErrorCount != 0 {
		t.Errorf("After snapshot, ErrorCount = %d, want 0", afterSnapshot.ErrorCount)
	}

	// Verify period times
	if metrics.PeriodEnd.Before(metrics.PeriodStart) {
		t.Errorf("PeriodEnd %v is before PeriodStart %v", metrics.PeriodEnd, metrics.PeriodStart)
	}

	// New period should start from snapshot time
	if afterSnapshot.PeriodStart.Before(metrics.PeriodEnd) {
		t.Errorf("New PeriodStart %v should be >= old PeriodEnd %v",
			afterSnapshot.PeriodStart, metrics.PeriodEnd)
	}
}

func TestCounter_Peek(t *testing.T) {
	c := NewCounter()

	c.RecordRequest(100)
	c.RecordResponse(200, 500)

	// Peek should return current values
	metrics1 := c.Peek()
	if metrics1.RequestCount != 1 {
		t.Errorf("First Peek RequestCount = %d, want 1", metrics1.RequestCount)
	}

	// Peek again - values should not change
	metrics2 := c.Peek()
	if metrics2.RequestCount != 1 {
		t.Errorf("Second Peek RequestCount = %d, want 1", metrics2.RequestCount)
	}

	// Add more activity
	c.RecordRequest(50)

	// Peek should show updated values
	metrics3 := c.Peek()
	if metrics3.RequestCount != 2 {
		t.Errorf("Third Peek RequestCount = %d, want 2", metrics3.RequestCount)
	}
}

func TestCounter_Concurrent(t *testing.T) {
	c := NewCounter()

	const numGoroutines = 100
	const numIterations = 1000

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // Half for requests, half for responses

	// Spawn goroutines to record requests
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				c.RecordRequest(10)
			}
		}()
	}

	// Spawn goroutines to record responses
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				c.RecordResponse(200, 20)
			}
		}()
	}

	wg.Wait()

	metrics := c.Snapshot()

	expectedRequests := uint64(numGoroutines * numIterations)
	expectedBytesIn := expectedRequests * 10
	expectedSuccess := uint64(numGoroutines * numIterations)
	expectedBytesOut := expectedSuccess * 20

	if metrics.RequestCount != expectedRequests {
		t.Errorf("RequestCount = %d, want %d", metrics.RequestCount, expectedRequests)
	}
	if metrics.BytesIn != expectedBytesIn {
		t.Errorf("BytesIn = %d, want %d", metrics.BytesIn, expectedBytesIn)
	}
	if metrics.SuccessCount != expectedSuccess {
		t.Errorf("SuccessCount = %d, want %d", metrics.SuccessCount, expectedSuccess)
	}
	if metrics.BytesOut != expectedBytesOut {
		t.Errorf("BytesOut = %d, want %d", metrics.BytesOut, expectedBytesOut)
	}
}

func TestCounter_ConcurrentWithSnapshot(t *testing.T) {
	c := NewCounter()

	const numGoroutines = 50
	const numIterations = 500

	var wg sync.WaitGroup
	wg.Add(numGoroutines + 10) // Request goroutines + snapshot goroutines

	var totalSnapshots sync.Map
	var snapshotCount int64

	// Spawn goroutines to record requests
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				c.RecordRequest(1)
				c.RecordResponse(200, 1)
			}
		}()
	}

	// Spawn goroutines to take snapshots
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				metrics := c.Snapshot()
				key := atomic.AddInt64(&snapshotCount, 1)
				totalSnapshots.Store(key, metrics)
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	wg.Wait()

	// Sum all snapshots - should equal total activity
	var totalRequests, totalSuccess uint64
	totalSnapshots.Range(func(key, value interface{}) bool {
		m := value.(UsageMetrics)
		totalRequests += m.RequestCount
		totalSuccess += m.SuccessCount
		return true
	})

	// Get any remaining counts
	final := c.Snapshot()
	totalRequests += final.RequestCount
	totalSuccess += final.SuccessCount

	expected := uint64(numGoroutines * numIterations)
	if totalRequests != expected {
		t.Errorf("Total RequestCount across snapshots = %d, want %d", totalRequests, expected)
	}
	if totalSuccess != expected {
		t.Errorf("Total SuccessCount across snapshots = %d, want %d", totalSuccess, expected)
	}
}

func TestUsageMetrics_IsZero(t *testing.T) {
	tests := []struct {
		name    string
		metrics UsageMetrics
		want    bool
	}{
		{
			name:    "empty metrics",
			metrics: UsageMetrics{},
			want:    true,
		},
		{
			name:    "has requests",
			metrics: UsageMetrics{RequestCount: 1},
			want:    false,
		},
		{
			name:    "has bytes in",
			metrics: UsageMetrics{BytesIn: 1},
			want:    false,
		},
		{
			name:    "has bytes out",
			metrics: UsageMetrics{BytesOut: 1},
			want:    false,
		},
		{
			name:    "has success count only (still zero by IsZero definition)",
			metrics: UsageMetrics{SuccessCount: 1},
			want:    true, // IsZero only checks RequestCount, BytesIn, BytesOut
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.metrics.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUsageMetrics_TotalBytes(t *testing.T) {
	m := UsageMetrics{
		BytesIn:  100,
		BytesOut: 250,
	}

	if got := m.TotalBytes(); got != 350 {
		t.Errorf("TotalBytes() = %d, want 350", got)
	}
}

func TestUsageMetrics_Duration(t *testing.T) {
	start := time.Now()
	end := start.Add(5 * time.Second)

	m := UsageMetrics{
		PeriodStart: start,
		PeriodEnd:   end,
	}

	if got := m.Duration(); got != 5*time.Second {
		t.Errorf("Duration() = %v, want 5s", got)
	}
}

func BenchmarkCounter_RecordRequest(b *testing.B) {
	c := NewCounter()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c.RecordRequest(100)
	}
}

func BenchmarkCounter_RecordResponse(b *testing.B) {
	c := NewCounter()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c.RecordResponse(200, 100)
	}
}

func BenchmarkCounter_RecordRequestParallel(b *testing.B) {
	c := NewCounter()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.RecordRequest(100)
		}
	})
}

func BenchmarkCounter_Snapshot(b *testing.B) {
	c := NewCounter()

	// Pre-populate with some data
	for i := 0; i < 1000; i++ {
		c.RecordRequest(100)
		c.RecordResponse(200, 100)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c.Snapshot()
	}
}
