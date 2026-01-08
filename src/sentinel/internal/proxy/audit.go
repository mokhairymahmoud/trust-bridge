// Package proxy provides HTTP reverse proxy and audit logging for the TrustBridge Sentinel.
//
// The proxy forwards consumer requests to the runtime inference server while
// maintaining a comprehensive audit trail of all requests.
package proxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// AuditEntry represents a single audit log entry for a proxied request.
type AuditEntry struct {
	Timestamp  string `json:"ts"`
	ContractID string `json:"contract_id"`
	AssetID    string `json:"asset_id"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	ReqSHA256  string `json:"req_sha256"`
	Status     int    `json:"status"`
	LatencyMS  int64  `json:"latency_ms"`
}

// AuditLogger defines the interface for audit log writers.
type AuditLogger interface {
	// Log writes an audit entry to the log.
	Log(entry *AuditEntry) error
	// Close closes the logger and releases resources.
	Close() error
}

// FileAuditLogger writes audit entries as JSON lines to a file.
type FileAuditLogger struct {
	file *os.File
	mu   sync.Mutex
}

// NewFileAuditLogger creates a new file-based audit logger.
// The file is created with append mode if it doesn't exist.
func NewFileAuditLogger(path string) (*FileAuditLogger, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &FileAuditLogger{file: file}, nil
}

// Log writes an audit entry as a JSON line to the file.
func (l *FileAuditLogger) Log(entry *AuditEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = l.file.Write(data)
	return err
}

// Close closes the underlying file.
func (l *FileAuditLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// MemoryAuditLogger stores audit entries in a ring buffer.
// Useful for testing and in-memory audit storage.
type MemoryAuditLogger struct {
	entries   []*AuditEntry
	maxSize   int
	position  int
	lastIndex int // tracks the index of the most recently added entry
	mu        sync.RWMutex
}

// NewMemoryAuditLogger creates a new in-memory audit logger with the specified capacity.
func NewMemoryAuditLogger(maxSize int) *MemoryAuditLogger {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &MemoryAuditLogger{
		entries:   make([]*AuditEntry, 0, maxSize),
		maxSize:   maxSize,
		lastIndex: -1,
	}
}

// Log adds an audit entry to the ring buffer.
func (l *MemoryAuditLogger) Log(entry *AuditEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.entries) < l.maxSize {
		l.lastIndex = len(l.entries)
		l.entries = append(l.entries, entry)
	} else {
		l.lastIndex = l.position
		l.entries[l.position] = entry
		l.position = (l.position + 1) % l.maxSize
	}
	return nil
}

// Close is a no-op for the memory logger.
func (l *MemoryAuditLogger) Close() error {
	return nil
}

// Entries returns a copy of all stored audit entries.
func (l *MemoryAuditLogger) Entries() []*AuditEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]*AuditEntry, len(l.entries))
	copy(result, l.entries)
	return result
}

// LastEntry returns the most recent audit entry, or nil if none exist.
func (l *MemoryAuditLogger) LastEntry() *AuditEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if len(l.entries) == 0 || l.lastIndex < 0 {
		return nil
	}
	return l.entries[l.lastIndex]
}

// Clear removes all entries from the logger.
func (l *MemoryAuditLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries = l.entries[:0]
	l.position = 0
	l.lastIndex = -1
}

// NopAuditLogger is a no-op audit logger that discards all entries.
type NopAuditLogger struct{}

// NewNopAuditLogger creates a new no-op audit logger.
func NewNopAuditLogger() *NopAuditLogger {
	return &NopAuditLogger{}
}

// Log discards the entry.
func (l *NopAuditLogger) Log(entry *AuditEntry) error {
	return nil
}

// Close is a no-op.
func (l *NopAuditLogger) Close() error {
	return nil
}

// responseRecorder wraps http.ResponseWriter to capture the status code.
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

func (r *responseRecorder) WriteHeader(code int) {
	if !r.written {
		r.statusCode = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.written {
		r.written = true
	}
	return r.ResponseWriter.Write(b)
}

// AuditMiddleware creates middleware that logs audit entries for each request.
func AuditMiddleware(logger AuditLogger, contractID, assetID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Read and hash request body
			var reqHash string
			if r.Body != nil {
				bodyBytes, err := io.ReadAll(r.Body)
				if err == nil {
					hash := sha256.Sum256(bodyBytes)
					reqHash = hex.EncodeToString(hash[:])
					// Restore the body for the next handler
					r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				}
			}
			if reqHash == "" {
				// Empty body hash
				hash := sha256.Sum256([]byte{})
				reqHash = hex.EncodeToString(hash[:])
			}

			// Wrap response writer to capture status code
			recorder := newResponseRecorder(w)

			// Call the next handler
			next.ServeHTTP(recorder, r)

			// Calculate latency
			latency := time.Since(start)

			// Create audit entry
			entry := &AuditEntry{
				Timestamp:  start.UTC().Format(time.RFC3339),
				ContractID: contractID,
				AssetID:    assetID,
				Method:     r.Method,
				Path:       r.URL.Path,
				ReqSHA256:  reqHash,
				Status:     recorder.statusCode,
				LatencyMS:  latency.Milliseconds(),
			}

			// Log the entry (errors are intentionally ignored to not affect request handling)
			logger.Log(entry)
		})
	}
}
