// Package asset provides functionality for downloading and verifying encrypted model assets.
//
// This package handles:
//   - Downloading and parsing manifest files
//   - Concurrent file downloads using HTTP Range requests
//   - SHA256 integrity verification
package asset

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Sentinel error values for asset operations.
var (
	// ErrManifestDownloadFailed indicates the manifest could not be downloaded.
	ErrManifestDownloadFailed = errors.New("manifest download failed")

	// ErrManifestInvalid indicates the manifest failed validation.
	ErrManifestInvalid = errors.New("manifest validation failed")

	// ErrDownloadFailed indicates the asset download failed.
	ErrDownloadFailed = errors.New("asset download failed")

	// ErrHashMismatch indicates the downloaded file hash doesn't match expected.
	ErrHashMismatch = errors.New("integrity check failed: hash mismatch")

	// ErrRangeNotSupported indicates the server doesn't support HTTP Range requests.
	ErrRangeNotSupported = errors.New("server does not support range requests")

	// ErrFileSizeMismatch indicates the downloaded file size doesn't match expected.
	ErrFileSizeMismatch = errors.New("downloaded file size mismatch")

	// ErrSASExpired indicates the SAS URL has expired (403/401 during download).
	ErrSASExpired = errors.New("SAS URL expired")

	// ErrMaxRetriesExceeded indicates all retry attempts were exhausted.
	ErrMaxRetriesExceeded = errors.New("max download retries exceeded")

	// ErrInvalidURL indicates the provided URL is malformed.
	ErrInvalidURL = errors.New("invalid URL")

	// ErrFileCreation indicates the output file could not be created.
	ErrFileCreation = errors.New("failed to create output file")
)

// AssetError represents an asset operation error with additional context.
type AssetError struct {
	Op         string // Operation: "manifest", "download", "verify", "range"
	URL        string // Sanitized URL (SAS signature removed for security)
	StatusCode int    // HTTP status code (if applicable)
	Retryable  bool   // Whether this error can be retried
	Err        error  // Underlying error
}

// Error implements the error interface.
func (e *AssetError) Error() string {
	var sb strings.Builder
	sb.WriteString("asset ")
	sb.WriteString(e.Op)
	sb.WriteString(" error")

	if e.URL != "" {
		sb.WriteString(": url=")
		sb.WriteString(e.URL)
	}

	if e.StatusCode != 0 {
		sb.WriteString(fmt.Sprintf(" (status %d)", e.StatusCode))
	}

	if e.Err != nil {
		sb.WriteString(": ")
		sb.WriteString(e.Err.Error())
	}

	return sb.String()
}

// Unwrap returns the underlying error for errors.Is/As compatibility.
func (e *AssetError) Unwrap() error {
	return e.Err
}

// IsRetryable returns true if the error can be retried.
func (e *AssetError) IsRetryable() bool {
	return e.Retryable
}

// NewManifestError creates an AssetError for manifest operations.
func NewManifestError(rawURL string, statusCode int, err error) *AssetError {
	retryable := isRetryableStatusCode(statusCode)
	return &AssetError{
		Op:         "manifest",
		URL:        sanitizeURL(rawURL),
		StatusCode: statusCode,
		Retryable:  retryable,
		Err:        err,
	}
}

// NewDownloadError creates an AssetError for download operations.
func NewDownloadError(rawURL string, statusCode int, err error) *AssetError {
	retryable := isRetryableStatusCode(statusCode)
	// Check for SAS expiry (403/401 during download)
	if statusCode == 403 || statusCode == 401 {
		return &AssetError{
			Op:         "download",
			URL:        sanitizeURL(rawURL),
			StatusCode: statusCode,
			Retryable:  true, // Retryable with new SAS URL
			Err:        ErrSASExpired,
		}
	}
	return &AssetError{
		Op:         "download",
		URL:        sanitizeURL(rawURL),
		StatusCode: statusCode,
		Retryable:  retryable,
		Err:        err,
	}
}

// NewRangeError creates an AssetError for range download operations.
func NewRangeError(rawURL string, statusCode int, start, end int64, err error) *AssetError {
	retryable := isRetryableStatusCode(statusCode)
	// Check for SAS expiry
	if statusCode == 403 || statusCode == 401 {
		return &AssetError{
			Op:         "range",
			URL:        sanitizeURL(rawURL),
			StatusCode: statusCode,
			Retryable:  true,
			Err:        ErrSASExpired,
		}
	}
	return &AssetError{
		Op:         "range",
		URL:        fmt.Sprintf("%s (bytes=%d-%d)", sanitizeURL(rawURL), start, end),
		StatusCode: statusCode,
		Retryable:  retryable,
		Err:        err,
	}
}

// NewVerifyError creates an AssetError for verification operations.
func NewVerifyError(filePath string, err error) *AssetError {
	return &AssetError{
		Op:        "verify",
		URL:       filePath,
		Retryable: false, // Hash mismatches are never retryable
		Err:       err,
	}
}

// NewNetworkError creates an AssetError for network-related errors.
func NewNetworkError(op, rawURL string, err error) *AssetError {
	return &AssetError{
		Op:        op,
		URL:       sanitizeURL(rawURL),
		Retryable: true,
		Err:       err,
	}
}

// IsSASExpired returns true if the error indicates a SAS URL expiration.
func IsSASExpired(err error) bool {
	var assetErr *AssetError
	if errors.As(err, &assetErr) {
		return errors.Is(assetErr.Err, ErrSASExpired)
	}
	return errors.Is(err, ErrSASExpired)
}

// IsRetryable returns true if the error can be retried.
func IsRetryable(err error) bool {
	var assetErr *AssetError
	if errors.As(err, &assetErr) {
		return assetErr.Retryable
	}
	return false
}

// IsHashMismatch returns true if the error indicates a hash mismatch.
func IsHashMismatch(err error) bool {
	return errors.Is(err, ErrHashMismatch)
}

// isRetryableStatusCode returns true for HTTP status codes that indicate transient failures.
func isRetryableStatusCode(statusCode int) bool {
	switch statusCode {
	case 0: // Network error (no response)
		return true
	case 429: // Too Many Requests
		return true
	case 500, 502, 503, 504: // Server errors
		return true
	default:
		return false
	}
}

// sanitizeURL removes sensitive query parameters (like SAS signatures) from URLs for logging.
func sanitizeURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		// If we can't parse it, truncate potential signatures
		if idx := strings.Index(rawURL, "?"); idx > 0 {
			return rawURL[:idx] + "?[REDACTED]"
		}
		return rawURL
	}

	// Remove query parameters that might contain sensitive data
	if parsed.RawQuery != "" {
		parsed.RawQuery = "[REDACTED]"
	}

	return parsed.String()
}
