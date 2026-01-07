package asset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testRangeServer creates an httptest.Server that supports HTTP Range requests.
type testRangeServer struct {
	*httptest.Server
	data          []byte
	requestCount  int64
	failAfter     int64 // If > 0, fail after this many bytes (simulates partial failure)
	failWithCode  int   // Status code to return on failure
	delayMs       int   // Delay per request in milliseconds
	rangeDisabled bool  // If true, don't support range requests
}

func newTestRangeServer(data []byte) *testRangeServer {
	ts := &testRangeServer{data: data}
	ts.Server = httptest.NewServer(http.HandlerFunc(ts.handleRequest))
	return ts
}

func (ts *testRangeServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&ts.requestCount, 1)

	// Optional delay
	if ts.delayMs > 0 {
		time.Sleep(time.Duration(ts.delayMs) * time.Millisecond)
	}

	// Handle HEAD request
	if r.Method == "HEAD" {
		if ts.rangeDisabled {
			w.Header().Set("Content-Length", strconv.Itoa(len(ts.data)))
		} else {
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", strconv.Itoa(len(ts.data)))
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse Range header
	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" && !ts.rangeDisabled {
		var start, end int64
		if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end); err != nil {
			http.Error(w, "Invalid range", http.StatusBadRequest)
			return
		}

		// Validate range
		if start < 0 || end >= int64(len(ts.data)) || start > end {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(ts.data)))
			http.Error(w, "Range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
			return
		}

		// Check for simulated failure
		if ts.failAfter > 0 && start >= ts.failAfter {
			code := ts.failWithCode
			if code == 0 {
				code = http.StatusInternalServerError
			}
			http.Error(w, "Simulated failure", code)
			return
		}

		// Serve partial content
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(ts.data)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(ts.data[start : end+1])
		return
	}

	// Serve full content
	w.Header().Set("Content-Length", strconv.Itoa(len(ts.data)))
	if !ts.rangeDisabled {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	w.WriteHeader(http.StatusOK)
	w.Write(ts.data)
}

func TestNewDownloader_Defaults(t *testing.T) {
	d := NewDownloader()

	if d.config.Concurrency != DefaultConcurrency {
		t.Errorf("expected concurrency %d, got %d", DefaultConcurrency, d.config.Concurrency)
	}
	if d.config.ChunkBytes != DefaultChunkBytes {
		t.Errorf("expected chunk bytes %d, got %d", DefaultChunkBytes, d.config.ChunkBytes)
	}
	if d.config.MaxRetries != DefaultMaxRetries {
		t.Errorf("expected max retries %d, got %d", DefaultMaxRetries, d.config.MaxRetries)
	}
}

func TestNewDownloader_WithOptions(t *testing.T) {
	customClient := &http.Client{Timeout: 5 * time.Second}
	progressCalled := false
	progressFn := func(downloaded, total int64) {
		progressCalled = true
	}

	d := NewDownloader(
		WithHTTPClient(customClient),
		WithConcurrency(8),
		WithChunkBytes(1024*1024), // 1MB
		WithRetryConfig(5, 500*time.Millisecond, 10*time.Second),
		WithProgressCallback(progressFn),
		WithRequestTimeout(30*time.Second),
	)

	if d.httpClient != customClient {
		t.Error("custom HTTP client not set")
	}
	if d.config.Concurrency != 8 {
		t.Errorf("expected concurrency 8, got %d", d.config.Concurrency)
	}
	if d.config.ChunkBytes != 1024*1024 {
		t.Errorf("expected chunk bytes 1048576, got %d", d.config.ChunkBytes)
	}
	if d.config.MaxRetries != 5 {
		t.Errorf("expected max retries 5, got %d", d.config.MaxRetries)
	}

	// Trigger progress callback
	if d.config.ProgressCallback != nil {
		d.config.ProgressCallback(100, 1000)
	}
	if !progressCalled {
		t.Error("progress callback not set or not called")
	}
}

func TestNewDownloader_ClampValues(t *testing.T) {
	// Test concurrency clamping
	d := NewDownloader(WithConcurrency(100)) // Over max
	if d.config.Concurrency != MaxConcurrency {
		t.Errorf("expected concurrency clamped to %d, got %d", MaxConcurrency, d.config.Concurrency)
	}

	d = NewDownloader(WithConcurrency(0)) // Under min
	if d.config.Concurrency != MinConcurrency {
		t.Errorf("expected concurrency clamped to %d, got %d", MinConcurrency, d.config.Concurrency)
	}

	// Test chunk bytes clamping
	d = NewDownloader(WithChunkBytes(100 * 1024 * 1024)) // Over max
	if d.config.ChunkBytes != MaxChunkBytes {
		t.Errorf("expected chunk bytes clamped to %d, got %d", MaxChunkBytes, d.config.ChunkBytes)
	}

	d = NewDownloader(WithChunkBytes(100)) // Under min
	if d.config.ChunkBytes != MinChunkBytes {
		t.Errorf("expected chunk bytes clamped to %d, got %d", MinChunkBytes, d.config.ChunkBytes)
	}
}

func TestDownloadFile_Success(t *testing.T) {
	testData := []byte("Hello, TrustBridge download test!")
	server := newTestRangeServer(testData)
	defer server.Close()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloader()
	ctx := context.Background()

	result, err := d.DownloadFile(ctx, server.URL+"/test.bin", outputPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.BytesWritten != int64(len(testData)) {
		t.Errorf("expected %d bytes written, got %d", len(testData), result.BytesWritten)
	}

	// Verify content
	downloaded, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if !bytes.Equal(downloaded, testData) {
		t.Error("downloaded content doesn't match original")
	}
}

func TestDownloadFile_ProgressCallback(t *testing.T) {
	testData := bytes.Repeat([]byte("X"), 100*1024) // 100KB
	server := newTestRangeServer(testData)
	defer server.Close()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "downloaded.bin")

	var progressCalls int64
	d := NewDownloader(
		WithProgressCallback(func(downloaded, total int64) {
			atomic.AddInt64(&progressCalls, 1)
		}),
	)

	ctx := context.Background()
	_, err := d.DownloadFile(ctx, server.URL+"/test.bin", outputPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if progressCalls == 0 {
		t.Error("progress callback was never called")
	}
}

func TestDownloadFile_HTTPError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		retryable  bool
	}{
		{name: "404 Not Found", statusCode: 404, retryable: false},
		{name: "403 Forbidden (SAS expired)", statusCode: 403, retryable: true}, // SAS expiry is retryable with new SAS
		{name: "401 Unauthorized", statusCode: 401, retryable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			tmpDir := t.TempDir()
			outputPath := filepath.Join(tmpDir, "downloaded.bin")

			d := NewDownloader(WithRetryConfig(0, time.Millisecond, time.Millisecond)) // No retries
			ctx := context.Background()

			_, err := d.DownloadFile(ctx, server.URL+"/test.bin", outputPath)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			// Check for SAS expiry on 403/401
			if tt.statusCode == 403 || tt.statusCode == 401 {
				if !IsSASExpired(err) {
					t.Error("expected SAS expired error for 403/401")
				}
			}
		})
	}
}

func TestDownloadFile_ServerError_Retry(t *testing.T) {
	var requestCount int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt64(&requestCount, 1)
		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Third request succeeds
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloader(
		WithRetryConfig(3, 10*time.Millisecond, 50*time.Millisecond),
	)
	ctx := context.Background()

	_, err := d.DownloadFile(ctx, server.URL+"/test.bin", outputPath)
	if err != nil {
		t.Fatalf("expected success after retries, got error: %v", err)
	}

	if requestCount != 3 {
		t.Errorf("expected 3 requests (2 failures + 1 success), got %d", requestCount)
	}
}

func TestDownloadFile_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloader()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := d.DownloadFile(ctx, server.URL+"/test.bin", outputPath)
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestDownloadFileConcurrent_Success(t *testing.T) {
	// Create test data large enough for multiple chunks
	size := 1024 * 1024 // 1MB
	testData := make([]byte, size)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	expectedHash := computeSHA256(testData)

	server := newTestRangeServer(testData)
	defer server.Close()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloader(
		WithConcurrency(4),
		WithChunkBytes(256*1024), // 256KB chunks, so 4 chunks total
	)
	ctx := context.Background()

	result, err := d.DownloadFileConcurrent(ctx, server.URL+"/test.bin", outputPath, int64(size))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.BytesWritten != int64(size) {
		t.Errorf("expected %d bytes written, got %d", size, result.BytesWritten)
	}

	// Verify content hash
	actualHash, err := ComputeFileHash(outputPath)
	if err != nil {
		t.Fatalf("failed to compute hash: %v", err)
	}
	if actualHash != expectedHash {
		t.Error("downloaded content hash doesn't match original")
	}
}

func TestDownloadFileConcurrent_FallbackToSingleThreaded(t *testing.T) {
	testData := []byte("Small test data")
	server := newTestRangeServer(testData)
	server.rangeDisabled = true // Disable range support
	defer server.Close()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloader(WithConcurrency(4))
	ctx := context.Background()

	// Should fall back to single-threaded
	result, err := d.DownloadFileConcurrent(ctx, server.URL+"/test.bin", outputPath, int64(len(testData)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.BytesWritten != int64(len(testData)) {
		t.Errorf("expected %d bytes written, got %d", len(testData), result.BytesWritten)
	}
}

func TestDownloadFileConcurrent_SmallFile(t *testing.T) {
	// File smaller than chunk size should use single-threaded
	testData := []byte("Small file")
	server := newTestRangeServer(testData)
	defer server.Close()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloader(WithChunkBytes(1024 * 1024)) // 1MB chunks
	ctx := context.Background()

	result, err := d.DownloadFileConcurrent(ctx, server.URL+"/test.bin", outputPath, int64(len(testData)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.BytesWritten != int64(len(testData)) {
		t.Errorf("expected %d bytes written, got %d", len(testData), result.BytesWritten)
	}
}

func TestDownloadFileConcurrent_RangeError_SASExpired(t *testing.T) {
	testData := make([]byte, 1024*1024) // 1MB
	server := newTestRangeServer(testData)
	server.failAfter = 256 * 1024 // Fail after first 256KB
	server.failWithCode = 403     // SAS expired
	defer server.Close()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloader(
		WithConcurrency(4),
		WithChunkBytes(256*1024),
		WithRetryConfig(0, time.Millisecond, time.Millisecond), // No retries
	)
	ctx := context.Background()

	_, err := d.DownloadFileConcurrent(ctx, server.URL+"/test.bin", outputPath, int64(len(testData)))
	if err == nil {
		t.Fatal("expected error for SAS expiry, got nil")
	}
	if !IsSASExpired(err) {
		t.Errorf("expected SAS expired error, got: %v", err)
	}
}

func TestCheckRangeSupport_Supported(t *testing.T) {
	testData := []byte("test data")
	server := newTestRangeServer(testData)
	defer server.Close()

	d := NewDownloader()
	ctx := context.Background()

	supportsRange, size, err := d.checkRangeSupport(ctx, server.URL+"/test.bin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !supportsRange {
		t.Error("expected range support to be true")
	}
	if size != int64(len(testData)) {
		t.Errorf("expected size %d, got %d", len(testData), size)
	}
}

func TestCheckRangeSupport_NotSupported(t *testing.T) {
	testData := []byte("test data")
	server := newTestRangeServer(testData)
	server.rangeDisabled = true
	defer server.Close()

	d := NewDownloader()
	ctx := context.Background()

	supportsRange, _, err := d.checkRangeSupport(ctx, server.URL+"/test.bin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if supportsRange {
		t.Error("expected range support to be false")
	}
}

func TestCalculateRanges(t *testing.T) {
	tests := []struct {
		name       string
		totalSize  int64
		chunkBytes int
		wantCount  int
	}{
		{
			name:       "exact multiple",
			totalSize:  4096,
			chunkBytes: 1024, // MinChunkBytes
			wantCount:  4,
		},
		{
			name:       "with remainder",
			totalSize:  5000,
			chunkBytes: 1024,
			wantCount:  5, // 1024 + 1024 + 1024 + 1024 + 904
		},
		{
			name:       "smaller than chunk",
			totalSize:  512,
			chunkBytes: 1024,
			wantCount:  1,
		},
		{
			name:       "single byte",
			totalSize:  1,
			chunkBytes: 1024,
			wantCount:  1,
		},
		{
			name:       "large file multiple chunks",
			totalSize:  10 * 1024 * 1024, // 10MB
			chunkBytes: 2 * 1024 * 1024,  // 2MB
			wantCount:  5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDownloader(WithChunkBytes(tt.chunkBytes))
			ranges := d.calculateRanges(tt.totalSize)

			if len(ranges) != tt.wantCount {
				t.Errorf("expected %d ranges, got %d", tt.wantCount, len(ranges))
			}

			// Verify ranges cover the entire file
			var totalCovered int64
			for _, r := range ranges {
				totalCovered += r.end - r.start + 1
				if r.start < 0 || r.end >= tt.totalSize || r.start > r.end {
					t.Errorf("invalid range: %d-%d for total size %d", r.start, r.end, tt.totalSize)
				}
			}
			if totalCovered != tt.totalSize {
				t.Errorf("ranges don't cover entire file: covered %d, total %d", totalCovered, tt.totalSize)
			}
		})
	}
}

func TestCalculateBackoff(t *testing.T) {
	d := NewDownloader(
		WithRetryConfig(5, 100*time.Millisecond, 1*time.Second),
	)

	// First retry should be around initial backoff
	backoff1 := d.calculateBackoff(1)
	if backoff1 < 90*time.Millisecond || backoff1 > 110*time.Millisecond {
		t.Errorf("first backoff %v not around 100ms", backoff1)
	}

	// Second retry should be around 2x
	backoff2 := d.calculateBackoff(2)
	if backoff2 < 180*time.Millisecond || backoff2 > 220*time.Millisecond {
		t.Errorf("second backoff %v not around 200ms", backoff2)
	}

	// Should cap at max backoff
	backoff10 := d.calculateBackoff(10)
	if backoff10 > 1100*time.Millisecond { // max + 10% jitter
		t.Errorf("backoff %v exceeds max", backoff10)
	}
}

func TestDownloadFile_CreatesDirectory(t *testing.T) {
	testData := []byte("test data")
	server := newTestRangeServer(testData)
	defer server.Close()

	tmpDir := t.TempDir()
	// Use a nested path that doesn't exist
	outputPath := filepath.Join(tmpDir, "subdir1", "subdir2", "downloaded.bin")

	d := NewDownloader()
	ctx := context.Background()

	_, err := d.DownloadFile(ctx, server.URL+"/test.bin", outputPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Error("expected file to be created in nested directory")
	}
}

func TestDownloadFile_InvalidURL(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloader()
	ctx := context.Background()

	_, err := d.DownloadFile(ctx, "://invalid-url", outputPath)
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

// BenchmarkDownloadFile_SingleThreaded benchmarks single-threaded download.
func BenchmarkDownloadFile_SingleThreaded(b *testing.B) {
	size := 1024 * 1024 // 1MB
	testData := make([]byte, size)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	server := newTestRangeServer(testData)
	defer server.Close()

	d := NewDownloader()

	b.ResetTimer()
	b.SetBytes(int64(size))

	for i := 0; i < b.N; i++ {
		tmpDir := b.TempDir()
		outputPath := filepath.Join(tmpDir, "downloaded.bin")
		ctx := context.Background()

		_, err := d.DownloadFile(ctx, server.URL+"/test.bin", outputPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDownloadFileConcurrent benchmarks concurrent download.
func BenchmarkDownloadFileConcurrent(b *testing.B) {
	size := 4 * 1024 * 1024 // 4MB
	testData := make([]byte, size)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	server := newTestRangeServer(testData)
	defer server.Close()

	d := NewDownloader(
		WithConcurrency(4),
		WithChunkBytes(1024*1024), // 1MB chunks
	)

	b.ResetTimer()
	b.SetBytes(int64(size))

	for i := 0; i < b.N; i++ {
		tmpDir := b.TempDir()
		outputPath := filepath.Join(tmpDir, "downloaded.bin")
		ctx := context.Background()

		_, err := d.DownloadFileConcurrent(ctx, server.URL+"/test.bin", outputPath, int64(size))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Test 100MB acceptance criteria
func TestAcceptance_100MB_ConcurrentDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping 100MB acceptance test in short mode")
	}

	// Create 100MB test data
	size := 100 * 1024 * 1024
	testData := make([]byte, size)
	pattern := []byte("TRUSTBRIDGE_TEST_")
	for i := range testData {
		testData[i] = pattern[i%len(pattern)]
	}
	expectedHash := computeSHA256(testData)

	server := newTestRangeServer(testData)
	defer server.Close()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "100mb.bin")

	d := NewDownloader(
		WithConcurrency(4),
		WithChunkBytes(8*1024*1024), // 8MB chunks
	)
	ctx := context.Background()

	start := time.Now()
	result, err := d.DownloadFileConcurrent(ctx, server.URL+"/test.bin", outputPath, int64(size))
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	duration := time.Since(start)

	t.Logf("Downloaded %d bytes in %v", result.BytesWritten, duration)

	// Verify size
	if result.BytesWritten != int64(size) {
		t.Errorf("expected %d bytes, got %d", size, result.BytesWritten)
	}

	// Verify hash
	actualHash, err := ComputeFileHash(outputPath)
	if err != nil {
		t.Fatalf("failed to compute hash: %v", err)
	}
	if actualHash != expectedHash {
		t.Error("hash mismatch after download")
	}
}

// computeSHA256 is a helper to compute SHA256 hash.
func computeSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestIntegration_DownloadAndVerify(t *testing.T) {
	// Create test data
	testData := bytes.Repeat([]byte("Integration test data "), 1000)
	expectedHash := computeSHA256(testData)

	server := newTestRangeServer(testData)
	defer server.Close()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "model.tbenc")

	// Download
	d := NewDownloader(WithConcurrency(2))
	ctx := context.Background()

	_, err := d.DownloadFileConcurrent(ctx, server.URL+"/model.tbenc", outputPath, int64(len(testData)))
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	// Verify hash
	err = VerifyFileHash(outputPath, expectedHash)
	if err != nil {
		t.Fatalf("verification failed: %v", err)
	}
}

func TestDownloadFileConcurrent_WithProgressCallback(t *testing.T) {
	size := 1024 * 1024 // 1MB
	testData := make([]byte, size)
	server := newTestRangeServer(testData)
	defer server.Close()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "downloaded.bin")

	var maxProgress int64
	d := NewDownloader(
		WithConcurrency(4),
		WithChunkBytes(256*1024),
		WithProgressCallback(func(downloaded, total int64) {
			if downloaded > atomic.LoadInt64(&maxProgress) {
				atomic.StoreInt64(&maxProgress, downloaded)
			}
		}),
	)
	ctx := context.Background()

	_, err := d.DownloadFileConcurrent(ctx, server.URL+"/test.bin", outputPath, int64(size))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Progress should have reached 100%
	if maxProgress != int64(size) {
		t.Errorf("expected max progress %d, got %d", size, maxProgress)
	}
}

func TestAssetError_URLSanitization(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "with SAS token",
			url:      "https://storage.blob.core.windows.net/container/blob?sv=2020-08-04&sig=secret",
			expected: "https://storage.blob.core.windows.net/container/blob?[REDACTED]",
		},
		{
			name:     "without query params",
			url:      "https://example.com/path/to/file.bin",
			expected: "https://example.com/path/to/file.bin",
		},
		{
			name:     "empty URL",
			url:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewDownloadError(tt.url, 500, errors.New("test"))
			if err.URL != tt.expected {
				t.Errorf("expected URL %q, got %q", tt.expected, err.URL)
			}
		})
	}
}

func TestOptionsWithConfig(t *testing.T) {
	cfg := &DownloadConfig{
		Concurrency:    8,
		ChunkBytes:     2 * 1024 * 1024,
		RequestTimeout: 45 * time.Second,
		MaxRetries:     5,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     60 * time.Second,
	}

	d := NewDownloader(WithConfig(cfg))

	if d.config.Concurrency != 8 {
		t.Errorf("expected concurrency 8, got %d", d.config.Concurrency)
	}
	if d.config.ChunkBytes != 2*1024*1024 {
		t.Errorf("expected chunk bytes %d, got %d", 2*1024*1024, d.config.ChunkBytes)
	}
}

func TestDownloadConfig_Validate(t *testing.T) {
	cfg := &DownloadConfig{
		Concurrency:    -1,
		ChunkBytes:     0,
		RequestTimeout: 0,
		MaxRetries:     -1,
		InitialBackoff: 0,
		MaxBackoff:     0,
	}

	cfg.Validate()

	if cfg.Concurrency != MinConcurrency {
		t.Errorf("expected concurrency %d, got %d", MinConcurrency, cfg.Concurrency)
	}
	if cfg.ChunkBytes != MinChunkBytes {
		t.Errorf("expected chunk bytes %d, got %d", MinChunkBytes, cfg.ChunkBytes)
	}
	if cfg.RequestTimeout != DefaultRequestTimeout {
		t.Errorf("expected timeout %v, got %v", DefaultRequestTimeout, cfg.RequestTimeout)
	}
	if cfg.MaxRetries != 0 {
		t.Errorf("expected max retries 0, got %d", cfg.MaxRetries)
	}
}

// Test for error message formatting
func TestAssetError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *AssetError
		contains []string
	}{
		{
			name: "download error with all fields",
			err: &AssetError{
				Op:         "download",
				URL:        "https://example.com/file.bin",
				StatusCode: 500,
				Err:        errors.New("server error"),
			},
			contains: []string{"asset download error", "example.com", "status 500", "server error"},
		},
		{
			name: "range error",
			err: &AssetError{
				Op:  "range",
				URL: "https://example.com (bytes=0-1000)",
				Err: errors.New("timeout"),
			},
			contains: []string{"asset range error", "bytes=0-1000", "timeout"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			for _, s := range tt.contains {
				if !strings.Contains(msg, s) {
					t.Errorf("error message %q should contain %q", msg, s)
				}
			}
		})
	}
}
