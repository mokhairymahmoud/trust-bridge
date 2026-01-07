package asset

import (
	"net/http"
	"time"
)

// Default configuration values for the Downloader.
const (
	DefaultConcurrency     = 4
	DefaultChunkBytes      = 8 * 1024 * 1024 // 8MB
	DefaultRequestTimeout  = 60 * time.Second
	DefaultMaxRetries      = 3
	DefaultInitialBackoff  = 1 * time.Second
	DefaultMaxBackoff      = 30 * time.Second

	// Validation limits
	MinConcurrency = 1
	MaxConcurrency = 32
	MinChunkBytes  = 1024           // 1KB
	MaxChunkBytes  = 64 * 1024 * 1024 // 64MB
)

// ProgressFunc is called to report download progress.
// downloaded is the number of bytes downloaded so far.
// total is the total size of the file being downloaded (may be 0 if unknown).
type ProgressFunc func(downloaded, total int64)

// DownloadConfig holds configuration for asset downloads.
type DownloadConfig struct {
	// Concurrency is the number of concurrent download workers.
	// Default: 4, Range: 1-32
	Concurrency int

	// ChunkBytes is the size of each download chunk for range requests.
	// Default: 8MB, Range: 1KB-64MB
	ChunkBytes int

	// RequestTimeout is the timeout for each HTTP request.
	// Default: 60 seconds
	RequestTimeout time.Duration

	// MaxRetries is the maximum number of retry attempts per range/request.
	// Default: 3
	MaxRetries int

	// InitialBackoff is the initial delay before the first retry.
	// Default: 1 second
	InitialBackoff time.Duration

	// MaxBackoff is the maximum delay between retries.
	// Default: 30 seconds
	MaxBackoff time.Duration

	// ProgressCallback is called periodically to report download progress.
	// May be nil.
	ProgressCallback ProgressFunc
}

// DefaultDownloadConfig returns a DownloadConfig with default values.
func DefaultDownloadConfig() *DownloadConfig {
	return &DownloadConfig{
		Concurrency:      DefaultConcurrency,
		ChunkBytes:       DefaultChunkBytes,
		RequestTimeout:   DefaultRequestTimeout,
		MaxRetries:       DefaultMaxRetries,
		InitialBackoff:   DefaultInitialBackoff,
		MaxBackoff:       DefaultMaxBackoff,
		ProgressCallback: nil,
	}
}

// Validate checks the configuration values and clamps them to valid ranges.
func (c *DownloadConfig) Validate() {
	if c.Concurrency < MinConcurrency {
		c.Concurrency = MinConcurrency
	}
	if c.Concurrency > MaxConcurrency {
		c.Concurrency = MaxConcurrency
	}

	if c.ChunkBytes < MinChunkBytes {
		c.ChunkBytes = MinChunkBytes
	}
	if c.ChunkBytes > MaxChunkBytes {
		c.ChunkBytes = MaxChunkBytes
	}

	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}

	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}

	if c.InitialBackoff <= 0 {
		c.InitialBackoff = DefaultInitialBackoff
	}

	if c.MaxBackoff <= 0 {
		c.MaxBackoff = DefaultMaxBackoff
	}
}

// DownloaderOption is a functional option for configuring a Downloader.
type DownloaderOption func(*Downloader)

// WithHTTPClient sets a custom HTTP client for the downloader.
// This allows for custom timeouts, transport configurations, or testing with mock clients.
func WithHTTPClient(client *http.Client) DownloaderOption {
	return func(d *Downloader) {
		if client != nil {
			d.httpClient = client
		}
	}
}

// WithConcurrency sets the number of concurrent download workers.
// The value will be clamped to the range [1, 32].
func WithConcurrency(n int) DownloaderOption {
	return func(d *Downloader) {
		d.config.Concurrency = n
	}
}

// WithChunkBytes sets the size of each download chunk for range requests.
// The value will be clamped to the range [1KB, 64MB].
func WithChunkBytes(size int) DownloaderOption {
	return func(d *Downloader) {
		d.config.ChunkBytes = size
	}
}

// WithRetryConfig sets the retry configuration for the downloader.
func WithRetryConfig(maxRetries int, initialBackoff, maxBackoff time.Duration) DownloaderOption {
	return func(d *Downloader) {
		d.config.MaxRetries = maxRetries
		d.config.InitialBackoff = initialBackoff
		d.config.MaxBackoff = maxBackoff
	}
}

// WithProgressCallback sets the progress callback function.
// The callback is invoked periodically during downloads to report progress.
func WithProgressCallback(fn ProgressFunc) DownloaderOption {
	return func(d *Downloader) {
		d.config.ProgressCallback = fn
	}
}

// WithRequestTimeout sets the timeout for each HTTP request.
func WithRequestTimeout(timeout time.Duration) DownloaderOption {
	return func(d *Downloader) {
		d.config.RequestTimeout = timeout
	}
}

// WithConfig sets the entire download configuration at once.
// This is useful when loading configuration from external sources.
func WithConfig(cfg *DownloadConfig) DownloaderOption {
	return func(d *Downloader) {
		if cfg != nil {
			d.config = cfg
		}
	}
}
