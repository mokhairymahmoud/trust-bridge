package billing

import (
	"bufio"
	"net"
	"net/http"
)

// Middleware captures request/response metrics for billing.
type Middleware struct {
	counter *Counter
}

// NewMiddleware creates middleware that records billing metrics.
func NewMiddleware(counter *Counter) *Middleware {
	return &Middleware{
		counter: counter,
	}
}

// Wrap returns an http.Handler that captures metrics before calling next.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Record request bytes
		var bytesIn int64
		if r.ContentLength > 0 {
			bytesIn = r.ContentLength
		}
		m.counter.RecordRequest(bytesIn)

		// Wrap response writer to capture bytes out and status
		brw := &billingResponseWriter{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		// Call next handler
		next.ServeHTTP(brw, r)

		// Record response
		m.counter.RecordResponse(brw.status, brw.bytesOut)
	})
}

// Handler returns the middleware as an http.Handler wrapping the given handler.
// This is an alias for Wrap for convenience.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return m.Wrap(next)
}

// billingResponseWriter wraps http.ResponseWriter to capture bytes written and status code.
type billingResponseWriter struct {
	http.ResponseWriter
	status      int
	bytesOut    int64
	wroteHeader bool
}

// WriteHeader captures the status code.
func (w *billingResponseWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write captures the number of bytes written.
func (w *billingResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytesOut += int64(n)
	return n, err
}

// Hijack implements http.Hijacker if the underlying ResponseWriter supports it.
// This is required for WebSocket connections and other protocols that take over the connection.
func (w *billingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

// Flush implements http.Flusher if the underlying ResponseWriter supports it.
// This is required for streaming responses.
func (w *billingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter.
// This supports Go 1.20+ ResponseController.
func (w *billingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Status returns the HTTP status code that was written.
func (w *billingResponseWriter) Status() int {
	return w.status
}

// BytesWritten returns the total bytes written to the response.
func (w *billingResponseWriter) BytesWritten() int64 {
	return w.bytesOut
}
