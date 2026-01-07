package asset

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// DownloadResult contains information about a completed download.
type DownloadResult struct {
	Path         string        // Output file path
	BytesWritten int64         // Total bytes written
	Duration     time.Duration // Total download time
}

// Downloader handles file downloads with optional concurrency.
type Downloader struct {
	httpClient *http.Client
	config     *DownloadConfig
}

// NewDownloader creates a new Downloader with the given options.
func NewDownloader(opts ...DownloaderOption) *Downloader {
	d := &Downloader{
		httpClient: &http.Client{
			Timeout: DefaultRequestTimeout,
		},
		config: DefaultDownloadConfig(),
	}

	for _, opt := range opts {
		opt(d)
	}

	// Validate and clamp configuration values
	d.config.Validate()

	return d
}

// DownloadFile performs a simple single-threaded HTTP GET download.
// This is the basic implementation suitable for smaller files or when
// the server doesn't support HTTP Range requests.
func (d *Downloader) DownloadFile(ctx context.Context, url, outputPath string) (*DownloadResult, error) {
	start := time.Now()

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return nil, NewDownloadError(url, 0, fmt.Errorf("%w: %v", ErrFileCreation, err))
	}

	// Create output file
	f, err := os.Create(outputPath)
	if err != nil {
		return nil, NewDownloadError(url, 0, fmt.Errorf("%w: %v", ErrFileCreation, err))
	}
	defer f.Close()

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, NewDownloadError(url, 0, fmt.Errorf("%w: %v", ErrInvalidURL, err))
	}

	// Execute request with retry
	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt <= d.config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := d.calculateBackoff(attempt)
			select {
			case <-ctx.Done():
				return nil, NewNetworkError("download", url, ctx.Err())
			case <-time.After(delay):
			}
		}

		resp, err = d.httpClient.Do(req)
		if err != nil {
			lastErr = NewNetworkError("download", url, err)
			continue
		}

		// Check for retryable status codes
		if isRetryableStatusCode(resp.StatusCode) {
			resp.Body.Close()
			lastErr = NewDownloadError(url, resp.StatusCode, fmt.Errorf("%w: status %d", ErrDownloadFailed, resp.StatusCode))
			continue
		}

		// Check for SAS expiry
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			return nil, NewDownloadError(url, resp.StatusCode, ErrSASExpired)
		}

		// Check for success
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, NewDownloadError(url, resp.StatusCode, fmt.Errorf("%w: unexpected status %d", ErrDownloadFailed, resp.StatusCode))
		}

		break
	}

	if resp == nil {
		return nil, fmt.Errorf("%w: %v", ErrMaxRetriesExceeded, lastErr)
	}
	defer resp.Body.Close()

	// Get total size for progress reporting
	totalSize := resp.ContentLength

	// Copy with progress tracking
	written, err := d.copyWithProgress(ctx, f, resp.Body, totalSize, url)
	if err != nil {
		os.Remove(outputPath) // Clean up partial download
		return nil, err
	}

	return &DownloadResult{
		Path:         outputPath,
		BytesWritten: written,
		Duration:     time.Since(start),
	}, nil
}

// DownloadFileConcurrent performs a concurrent download using HTTP Range requests.
// Falls back to single-threaded download if the server doesn't support ranges.
// The totalSize parameter should be provided from the manifest for best results.
func (d *Downloader) DownloadFileConcurrent(ctx context.Context, url, outputPath string, totalSize int64) (*DownloadResult, error) {
	start := time.Now()

	// Check if server supports range requests and get size
	supportsRange, serverSize, err := d.checkRangeSupport(ctx, url)
	if err != nil {
		// If we can't check, fall back to single-threaded
		log.Printf("Range check failed, falling back to single-threaded download: %v", err)
		return d.DownloadFile(ctx, url, outputPath)
	}

	// Use server-reported size if totalSize is 0 or doesn't match
	if totalSize == 0 {
		totalSize = serverSize
	}

	// If server doesn't support ranges, fall back to single-threaded
	if !supportsRange || totalSize <= 0 {
		log.Printf("Server doesn't support range requests or size unknown, falling back to single-threaded")
		return d.DownloadFile(ctx, url, outputPath)
	}

	// For small files, use single-threaded download
	if totalSize < int64(d.config.ChunkBytes) {
		return d.DownloadFile(ctx, url, outputPath)
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return nil, NewDownloadError(url, 0, fmt.Errorf("%w: %v", ErrFileCreation, err))
	}

	// Create and pre-allocate output file
	f, err := os.Create(outputPath)
	if err != nil {
		return nil, NewDownloadError(url, 0, fmt.Errorf("%w: %v", ErrFileCreation, err))
	}

	// Pre-allocate the file to avoid fragmentation
	if err := f.Truncate(totalSize); err != nil {
		f.Close()
		os.Remove(outputPath)
		return nil, NewDownloadError(url, 0, fmt.Errorf("failed to pre-allocate file: %w", err))
	}

	// Calculate ranges
	ranges := d.calculateRanges(totalSize)

	// Create channels for coordination
	type rangeResult struct {
		start   int64
		end     int64
		written int64
		err     error
	}

	resultCh := make(chan rangeResult, len(ranges))
	progressCh := make(chan int64, len(ranges)*10)

	// Context for cancellation on first error
	downloadCtx, cancelDownload := context.WithCancel(ctx)
	defer cancelDownload()

	// Progress tracking goroutine
	var totalDownloaded int64
	var progressWg sync.WaitGroup
	progressWg.Add(1)
	go func() {
		defer progressWg.Done()
		lastLoggedPercent := -1
		for {
			select {
			case <-downloadCtx.Done():
				return
			case bytes, ok := <-progressCh:
				if !ok {
					return
				}
				downloaded := atomic.AddInt64(&totalDownloaded, bytes)
				if d.config.ProgressCallback != nil {
					d.config.ProgressCallback(downloaded, totalSize)
				}
				// Log progress at 10% intervals
				percent := int(float64(downloaded) / float64(totalSize) * 100)
				if percent/10 > lastLoggedPercent/10 {
					log.Printf("Download progress: %d%% (%d/%d bytes)", percent, downloaded, totalSize)
					lastLoggedPercent = percent
				}
			}
		}
	}()

	// Launch download workers
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, d.config.Concurrency)

	for _, r := range ranges {
		wg.Add(1)
		go func(rangeStart, rangeEnd int64) {
			defer wg.Done()

			semaphore <- struct{}{}        // Acquire semaphore
			defer func() { <-semaphore }() // Release semaphore

			written, err := d.downloadRange(downloadCtx, f, url, rangeStart, rangeEnd, progressCh)
			resultCh <- rangeResult{
				start:   rangeStart,
				end:     rangeEnd,
				written: written,
				err:     err,
			}

			// Cancel on error
			if err != nil {
				cancelDownload()
			}
		}(r.start, r.end)
	}

	// Wait for all workers and close result channel
	go func() {
		wg.Wait()
		close(resultCh)
		close(progressCh)
	}()

	// Collect results
	var firstErr error
	var totalWritten int64
	for result := range resultCh {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		totalWritten += result.written
	}

	// Wait for progress goroutine
	progressWg.Wait()

	// Close file
	if err := f.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("failed to close file: %w", err)
	}

	// Clean up on error
	if firstErr != nil {
		os.Remove(outputPath)
		return nil, firstErr
	}

	// Verify final size
	if totalWritten != totalSize {
		os.Remove(outputPath)
		return nil, NewVerifyError(outputPath, fmt.Errorf("%w: expected %d bytes, got %d", ErrFileSizeMismatch, totalSize, totalWritten))
	}

	return &DownloadResult{
		Path:         outputPath,
		BytesWritten: totalWritten,
		Duration:     time.Since(start),
	}, nil
}

// rangeSpec represents a byte range to download.
type rangeSpec struct {
	start int64
	end   int64
}

// calculateRanges divides the total size into ranges for concurrent download.
func (d *Downloader) calculateRanges(totalSize int64) []rangeSpec {
	chunkSize := int64(d.config.ChunkBytes)
	var ranges []rangeSpec

	for start := int64(0); start < totalSize; start += chunkSize {
		end := start + chunkSize - 1
		if end >= totalSize {
			end = totalSize - 1
		}
		ranges = append(ranges, rangeSpec{start: start, end: end})
	}

	return ranges
}

// checkRangeSupport checks if the server supports HTTP Range requests.
// Returns: supportsRange, totalSize, error
func (d *Downloader) checkRangeSupport(ctx context.Context, url string) (bool, int64, error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return false, 0, err
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try a range request instead to check support
		return d.checkRangeSupportWithGet(ctx, url)
	}

	acceptRanges := resp.Header.Get("Accept-Ranges")
	supportsRange := acceptRanges == "bytes"

	return supportsRange, resp.ContentLength, nil
}

// checkRangeSupportWithGet tries a small range request to check support.
func (d *Downloader) checkRangeSupportWithGet(ctx context.Context, url string) (bool, int64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("Range", "bytes=0-0")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()

	// 206 Partial Content means range requests are supported
	if resp.StatusCode == http.StatusPartialContent {
		// Try to parse Content-Range header for total size
		// Format: "bytes 0-0/12345"
		contentRange := resp.Header.Get("Content-Range")
		var start, end, total int64
		if _, err := fmt.Sscanf(contentRange, "bytes %d-%d/%d", &start, &end, &total); err == nil {
			return true, total, nil
		}
		return true, 0, nil
	}

	return false, resp.ContentLength, nil
}

// downloadRange downloads a specific byte range and writes it to the file.
func (d *Downloader) downloadRange(ctx context.Context, f *os.File, url string, start, end int64, progressCh chan<- int64) (int64, error) {
	var lastErr error

	for attempt := 0; attempt <= d.config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := d.calculateBackoff(attempt)
			select {
			case <-ctx.Done():
				return 0, NewNetworkError("range", url, ctx.Err())
			case <-time.After(delay):
			}
		}

		written, err := d.doRangeRequest(ctx, f, url, start, end, progressCh)
		if err == nil {
			return written, nil
		}

		lastErr = err

		// Check for SAS expiry - don't retry locally
		if IsSASExpired(err) {
			return 0, err
		}

		// Check if context is cancelled
		if ctx.Err() != nil {
			return 0, NewNetworkError("range", url, ctx.Err())
		}

		// Check if error is retryable
		if !IsRetryable(err) {
			return 0, err
		}
	}

	return 0, fmt.Errorf("%w: %v", ErrMaxRetriesExceeded, lastErr)
}

// doRangeRequest performs a single range request.
func (d *Downloader) doRangeRequest(ctx context.Context, f *os.File, url string, start, end int64, progressCh chan<- int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, NewRangeError(url, 0, start, end, err)
	}

	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return 0, NewNetworkError("range", url, err)
	}
	defer resp.Body.Close()

	// Check status
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return 0, NewRangeError(url, resp.StatusCode, start, end, ErrSASExpired)
	}

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, NewRangeError(url, resp.StatusCode, start, end, fmt.Errorf("unexpected status: %d", resp.StatusCode))
	}

	// Read and write at the correct offset
	expectedBytes := end - start + 1
	buf := make([]byte, 32*1024) // 32KB buffer
	var totalWritten int64

	for {
		select {
		case <-ctx.Done():
			return totalWritten, NewNetworkError("range", url, ctx.Err())
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			writeOffset := start + totalWritten
			nw, writeErr := f.WriteAt(buf[:n], writeOffset)
			if writeErr != nil {
				return totalWritten, NewRangeError(url, 0, start, end, fmt.Errorf("write error: %w", writeErr))
			}
			if nw != n {
				return totalWritten, NewRangeError(url, 0, start, end, fmt.Errorf("short write: %d/%d", nw, n))
			}
			totalWritten += int64(nw)

			// Report progress
			select {
			case progressCh <- int64(nw):
			default:
				// Don't block on progress
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return totalWritten, NewNetworkError("range", url, readErr)
		}
	}

	// Verify we got the expected number of bytes
	if totalWritten != expectedBytes {
		return totalWritten, NewRangeError(url, 0, start, end, fmt.Errorf("incomplete read: got %d, expected %d", totalWritten, expectedBytes))
	}

	return totalWritten, nil
}

// copyWithProgress copies from reader to writer with progress tracking.
func (d *Downloader) copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, totalSize int64, url string) (int64, error) {
	buf := make([]byte, 32*1024) // 32KB buffer
	var written int64
	lastLoggedPercent := -1

	for {
		select {
		case <-ctx.Done():
			return written, NewNetworkError("download", url, ctx.Err())
		default:
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			nw, writeErr := dst.Write(buf[:n])
			if writeErr != nil {
				return written, NewDownloadError(url, 0, writeErr)
			}
			if nw != n {
				return written, NewDownloadError(url, 0, fmt.Errorf("short write: %d/%d", nw, n))
			}
			written += int64(nw)

			// Report progress
			if d.config.ProgressCallback != nil {
				d.config.ProgressCallback(written, totalSize)
			}

			// Log progress at 10% intervals
			if totalSize > 0 {
				percent := int(float64(written) / float64(totalSize) * 100)
				if percent/10 > lastLoggedPercent/10 {
					log.Printf("Download progress: %d%% (%d/%d bytes)", percent, written, totalSize)
					lastLoggedPercent = percent
				}
			}
		}

		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, NewNetworkError("download", url, readErr)
		}
	}
}

// calculateBackoff calculates the backoff delay for a retry attempt.
// Uses exponential backoff with jitter.
func (d *Downloader) calculateBackoff(attempt int) time.Duration {
	delay := d.config.InitialBackoff
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay > d.config.MaxBackoff {
			delay = d.config.MaxBackoff
			break
		}
	}

	// Add jitter: +/- 10%
	jitter := float64(delay) * 0.1 * (2*rand.Float64() - 1)
	return time.Duration(float64(delay) + jitter)
}
