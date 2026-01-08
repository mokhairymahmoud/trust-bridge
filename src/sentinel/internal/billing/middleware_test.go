package billing

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware_RecordsBytesIn(t *testing.T) {
	counter := NewCounter()
	middleware := NewMiddleware(counter)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read and discard body
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))

	body := bytes.NewReader([]byte("hello world"))
	req := httptest.NewRequest(http.MethodPost, "/test", body)
	req.ContentLength = 11

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	metrics := counter.Peek()
	if metrics.RequestCount != 1 {
		t.Errorf("RequestCount = %d, want 1", metrics.RequestCount)
	}
	if metrics.BytesIn != 11 {
		t.Errorf("BytesIn = %d, want 11", metrics.BytesIn)
	}
}

func TestMiddleware_RecordsBytesOut(t *testing.T) {
	counter := NewCounter()
	middleware := NewMiddleware(counter)

	responseBody := []byte("response body content")

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(responseBody)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	metrics := counter.Peek()
	if metrics.BytesOut != uint64(len(responseBody)) {
		t.Errorf("BytesOut = %d, want %d", metrics.BytesOut, len(responseBody))
	}
}

func TestMiddleware_RecordsStatusCodes(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		wantSuccess uint64
		wantError   uint64
	}{
		{
			name:        "200 OK",
			status:      http.StatusOK,
			wantSuccess: 1,
			wantError:   0,
		},
		{
			name:        "201 Created",
			status:      http.StatusCreated,
			wantSuccess: 1,
			wantError:   0,
		},
		{
			name:        "400 Bad Request",
			status:      http.StatusBadRequest,
			wantSuccess: 0,
			wantError:   1,
		},
		{
			name:        "404 Not Found",
			status:      http.StatusNotFound,
			wantSuccess: 0,
			wantError:   1,
		},
		{
			name:        "500 Internal Server Error",
			status:      http.StatusInternalServerError,
			wantSuccess: 0,
			wantError:   1,
		},
		{
			name:        "503 Service Unavailable",
			status:      http.StatusServiceUnavailable,
			wantSuccess: 0,
			wantError:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := NewCounter()
			middleware := NewMiddleware(counter)

			handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			metrics := counter.Peek()
			if metrics.SuccessCount != tt.wantSuccess {
				t.Errorf("SuccessCount = %d, want %d", metrics.SuccessCount, tt.wantSuccess)
			}
			if metrics.ErrorCount != tt.wantError {
				t.Errorf("ErrorCount = %d, want %d", metrics.ErrorCount, tt.wantError)
			}
		})
	}
}

func TestMiddleware_DefaultStatus(t *testing.T) {
	// When handler doesn't explicitly call WriteHeader, default is 200
	counter := NewCounter()
	middleware := NewMiddleware(counter)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok")) // Write without WriteHeader
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	metrics := counter.Peek()
	if metrics.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1 (default status 200)", metrics.SuccessCount)
	}
}

func TestMiddleware_NoContentLength(t *testing.T) {
	counter := NewCounter()
	middleware := NewMiddleware(counter)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Request without content length
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.ContentLength = -1 // Unknown content length

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	metrics := counter.Peek()
	if metrics.RequestCount != 1 {
		t.Errorf("RequestCount = %d, want 1", metrics.RequestCount)
	}
	if metrics.BytesIn != 0 {
		t.Errorf("BytesIn = %d, want 0 (unknown content length)", metrics.BytesIn)
	}
}

func TestMiddleware_MultipleWrites(t *testing.T) {
	counter := NewCounter()
	middleware := NewMiddleware(counter)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("first"))
		w.Write([]byte("second"))
		w.Write([]byte("third"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	metrics := counter.Peek()
	expectedBytes := uint64(len("first") + len("second") + len("third"))
	if metrics.BytesOut != expectedBytes {
		t.Errorf("BytesOut = %d, want %d", metrics.BytesOut, expectedBytes)
	}
}

func TestMiddleware_HandlerAlias(t *testing.T) {
	counter := NewCounter()
	middleware := NewMiddleware(counter)

	innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Test Handler method (alias for Wrap)
	handler := middleware.Handler(innerHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	metrics := counter.Peek()
	if metrics.RequestCount != 1 {
		t.Errorf("RequestCount = %d, want 1", metrics.RequestCount)
	}
}

func TestBillingResponseWriter_Status(t *testing.T) {
	rec := httptest.NewRecorder()
	brw := &billingResponseWriter{
		ResponseWriter: rec,
		status:         http.StatusOK,
	}

	brw.WriteHeader(http.StatusCreated)

	if brw.Status() != http.StatusCreated {
		t.Errorf("Status() = %d, want %d", brw.Status(), http.StatusCreated)
	}
}

func TestBillingResponseWriter_BytesWritten(t *testing.T) {
	rec := httptest.NewRecorder()
	brw := &billingResponseWriter{
		ResponseWriter: rec,
		status:         http.StatusOK,
	}

	brw.Write([]byte("hello"))
	brw.Write([]byte(" world"))

	if brw.BytesWritten() != 11 {
		t.Errorf("BytesWritten() = %d, want 11", brw.BytesWritten())
	}
}

func TestBillingResponseWriter_Flush(t *testing.T) {
	rec := httptest.NewRecorder()
	brw := &billingResponseWriter{
		ResponseWriter: rec,
		status:         http.StatusOK,
	}

	brw.Write([]byte("data"))
	brw.Flush() // Should not panic

	// Verify the data was written
	if rec.Body.String() != "data" {
		t.Errorf("Body = %q, want %q", rec.Body.String(), "data")
	}
}

func TestBillingResponseWriter_Unwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	brw := &billingResponseWriter{
		ResponseWriter: rec,
		status:         http.StatusOK,
	}

	unwrapped := brw.Unwrap()
	if unwrapped != rec {
		t.Error("Unwrap() did not return the underlying ResponseWriter")
	}
}

func TestBillingResponseWriter_WriteHeaderOnce(t *testing.T) {
	rec := httptest.NewRecorder()
	brw := &billingResponseWriter{
		ResponseWriter: rec,
		status:         http.StatusOK,
	}

	// First WriteHeader should be recorded
	brw.WriteHeader(http.StatusCreated)
	// Second WriteHeader should be ignored for our tracking
	brw.WriteHeader(http.StatusInternalServerError)

	if brw.Status() != http.StatusCreated {
		t.Errorf("Status() = %d, want %d (first WriteHeader)", brw.Status(), http.StatusCreated)
	}
}

func BenchmarkMiddleware(b *testing.B) {
	counter := NewCounter()
	middleware := NewMiddleware(counter)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader([]byte("request")))
	req.ContentLength = 7

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkMiddleware_Parallel(b *testing.B) {
	counter := NewCounter()
	middleware := NewMiddleware(counter)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response"))
	}))

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader([]byte("request")))
			req.ContentLength = 7
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}
	})
}
