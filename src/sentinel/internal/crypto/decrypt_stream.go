// Package crypto implements tbenc/v1 decryption for TrustBridge.
//
// This file provides streaming decryption orchestration for decrypting
// encrypted model weights to a FIFO (named pipe).
package crypto

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
)

// StreamResult contains the result of a streaming decryption operation.
type StreamResult struct {
	BytesWritten int64
	Err          error
}

// StreamOption configures the streaming decryption behavior.
type StreamOption func(*streamConfig)

// streamConfig holds the configuration for streaming decryption.
type streamConfig struct {
	progressCallback func(bytesWritten, totalBytes int64)
	logger           *slog.Logger
	totalBytes       int64 // Expected total plaintext bytes (for progress %)
}

// WithProgressCallback sets a callback function that is called periodically
// during decryption with the number of bytes written so far.
func WithProgressCallback(fn func(bytesWritten, totalBytes int64)) StreamOption {
	return func(c *streamConfig) {
		c.progressCallback = fn
	}
}

// WithLogger sets the logger for the streaming operation.
func WithLogger(logger *slog.Logger) StreamOption {
	return func(c *streamConfig) {
		c.logger = logger
	}
}

// WithTotalBytes sets the expected total plaintext bytes.
// This is used for calculating progress percentage.
func WithTotalBytes(totalBytes int64) StreamOption {
	return func(c *streamConfig) {
		c.totalBytes = totalBytes
	}
}

// DecryptToFIFO decrypts an encrypted file to a FIFO asynchronously.
//
// This function:
// 1. Creates the FIFO at fifoPath (if it doesn't exist)
// 2. Opens the encrypted file
// 3. Starts a goroutine that:
//   - Opens the FIFO for writing (blocks until a reader opens it)
//   - Decrypts the file chunk by chunk
//   - Writes plaintext to the FIFO
//
// 4. Returns immediately with a channel that will receive the result
//
// The returned channel receives exactly one StreamResult when decryption
// completes (either successfully or with an error).
//
// The context can be used to cancel the operation. If cancelled, the
// result will contain a context.Canceled or context.DeadlineExceeded error.
func DecryptToFIFO(ctx context.Context, encryptedPath, fifoPath string, key []byte, opts ...StreamOption) <-chan StreamResult {
	result := make(chan StreamResult, 1)

	// Apply options
	cfg := &streamConfig{
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	go func() {
		defer close(result)

		bytesWritten, err := decryptToFIFOInternal(ctx, encryptedPath, fifoPath, key, cfg)
		result <- StreamResult{
			BytesWritten: bytesWritten,
			Err:          err,
		}
	}()

	return result
}

// DecryptToFIFOBlocking is a blocking variant of DecryptToFIFO.
// It blocks until decryption is complete and returns the result directly.
func DecryptToFIFOBlocking(ctx context.Context, encryptedPath, fifoPath string, key []byte, opts ...StreamOption) (int64, error) {
	resultCh := DecryptToFIFO(ctx, encryptedPath, fifoPath, key, opts...)

	select {
	case <-ctx.Done():
		// Wait for the result anyway to ensure cleanup
		res := <-resultCh
		if res.Err != nil {
			return res.BytesWritten, res.Err
		}
		return res.BytesWritten, ctx.Err()
	case res := <-resultCh:
		return res.BytesWritten, res.Err
	}
}

// decryptToFIFOInternal contains the actual decryption logic.
func decryptToFIFOInternal(ctx context.Context, encryptedPath, fifoPath string, key []byte, cfg *streamConfig) (int64, error) {
	// Validate inputs
	if encryptedPath == "" {
		return 0, fmt.Errorf("encrypted file path cannot be empty")
	}
	if fifoPath == "" {
		return 0, fmt.Errorf("FIFO path cannot be empty")
	}
	if len(key) != 32 {
		return 0, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}

	// Check for cancellation
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	cfg.logger.Info("starting decryption to FIFO",
		"encrypted_path", encryptedPath,
		"fifo_path", fifoPath,
	)

	// Create FIFO
	if err := CreateFIFO(fifoPath); err != nil {
		return 0, fmt.Errorf("failed to create FIFO: %w", err)
	}

	// Open encrypted file
	encFile, err := os.Open(encryptedPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open encrypted file: %w", err)
	}
	defer encFile.Close()

	// Check for cancellation before blocking on FIFO open
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	cfg.logger.Info("opening FIFO for writing (will block until reader connects)")

	// Open FIFO for writing - this blocks until a reader opens it
	// We use a goroutine to allow cancellation
	type fifoResult struct {
		file *os.File
		err  error
	}
	fifoOpenCh := make(chan fifoResult, 1)

	go func() {
		f, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		fifoOpenCh <- fifoResult{file: f, err: err}
	}()

	var fifoFile *os.File
	select {
	case <-ctx.Done():
		// Context cancelled while waiting for reader
		// Note: The goroutine will still be blocked on OpenFile until
		// a reader connects or the FIFO is removed
		return 0, ctx.Err()
	case res := <-fifoOpenCh:
		if res.err != nil {
			return 0, fmt.Errorf("failed to open FIFO for writing: %w", res.err)
		}
		fifoFile = res.file
	}
	defer fifoFile.Close()

	cfg.logger.Info("FIFO opened, starting decryption")

	// Create progress tracking writer
	var progressWriter io.Writer = fifoFile
	if cfg.progressCallback != nil || cfg.totalBytes > 0 {
		progressWriter = &progressTrackingWriter{
			w:                fifoFile,
			totalBytes:       cfg.totalBytes,
			progressCallback: cfg.progressCallback,
			logger:           cfg.logger,
			lastLogPercent:   -10, // Will log at 0%
		}
	}

	// Create a context-aware reader that checks for cancellation
	ctxReader := &contextReader{
		ctx: ctx,
		r:   encFile,
	}

	// Decrypt to the progress writer
	bytesWritten, err := DecryptToWriter(ctxReader, progressWriter, key)
	if err != nil {
		return bytesWritten, fmt.Errorf("decryption failed: %w", err)
	}

	cfg.logger.Info("decryption completed",
		"bytes_written", bytesWritten,
	)

	return bytesWritten, nil
}

// progressTrackingWriter wraps a writer and tracks progress.
type progressTrackingWriter struct {
	w                io.Writer
	totalBytes       int64
	bytesWritten     atomic.Int64
	progressCallback func(bytesWritten, totalBytes int64)
	logger           *slog.Logger
	lastLogPercent   int
}

func (pw *progressTrackingWriter) Write(p []byte) (n int, err error) {
	n, err = pw.w.Write(p)
	if n > 0 {
		written := pw.bytesWritten.Add(int64(n))

		// Call progress callback
		if pw.progressCallback != nil {
			pw.progressCallback(written, pw.totalBytes)
		}

		// Log progress every 10%
		if pw.totalBytes > 0 {
			percent := int(float64(written) / float64(pw.totalBytes) * 100)
			// Round down to nearest 10
			percent = (percent / 10) * 10
			if percent > pw.lastLogPercent {
				pw.lastLogPercent = percent
				pw.logger.Info("decryption progress",
					"percent", percent,
					"bytes_written", written,
					"total_bytes", pw.totalBytes,
				)
			}
		}
	}
	return n, err
}

// contextReader wraps a reader and checks for context cancellation.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (cr *contextReader) Read(p []byte) (n int, err error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, err
	}
	return cr.r.Read(p)
}
