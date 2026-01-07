// Package config provides configuration loading and validation for the TrustBridge Sentinel.
//
// Configuration is loaded from environment variables and validated to ensure
// all required fields are present and values are within acceptable ranges.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Default values for optional configuration
const (
	DefaultTargetDir           = "/mnt/resource/trustbridge"
	DefaultPipePath            = "/dev/shm/model-pipe"
	DefaultReadySignal         = "/dev/shm/weights/ready.signal"
	DefaultRuntimeURL          = "http://127.0.0.1:8081"
	DefaultPublicAddr          = "0.0.0.0:8000"
	DefaultDownloadConcurrency = 4
	DefaultDownloadChunkBytes  = 8388608 // 8MB
	DefaultLogLevel            = "info"

	// Validation limits
	MinDownloadConcurrency = 1
	MaxDownloadConcurrency = 32
	MinDownloadChunkBytes  = 1024           // 1KB
	MaxDownloadChunkBytes  = 64 * 1024 * 1024 // 64MB
)

// Valid log levels
var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

// Config holds all sentinel configuration.
type Config struct {
	// Required fields
	ContractID  string // TB_CONTRACT_ID - Contract identifier for authorization
	AssetID     string // TB_ASSET_ID - Asset identifier for the model
	EDCEndpoint string // TB_EDC_ENDPOINT - Control Plane/EDC endpoint URL

	// Paths with defaults
	TargetDir   string // TB_TARGET_DIR - Directory for encrypted downloads
	PipePath    string // TB_PIPE_PATH - FIFO path for decrypted output
	ReadySignal string // TB_READY_SIGNAL - Signal file for runtime readiness

	// URLs with defaults
	RuntimeURL string // TB_RUNTIME_URL - Runtime inference server URL
	PublicAddr string // TB_PUBLIC_ADDR - Public address to bind sentinel

	// Download configuration
	DownloadConcurrency int // TB_DOWNLOAD_CONCURRENCY - Number of concurrent download workers
	DownloadChunkBytes  int // TB_DOWNLOAD_CHUNK_BYTES - Size of download chunks

	// Logging
	LogLevel string // TB_LOG_LEVEL - Logging level (debug, info, warn, error)
}

// ValidationError represents a configuration validation failure.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("config: %s: %s", e.Field, e.Message)
}

// ConfigErrors aggregates multiple validation errors.
type ConfigErrors []error

func (e ConfigErrors) Error() string {
	if len(e) == 0 {
		return "no errors"
	}
	if len(e) == 1 {
		return e[0].Error()
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d configuration errors:\n", len(e)))
	for _, err := range e {
		sb.WriteString("  - ")
		sb.WriteString(err.Error())
		sb.WriteString("\n")
	}
	return sb.String()
}

// Load reads configuration from environment variables and applies defaults.
// Returns an error if any required field is missing or validation fails.
func Load() (*Config, error) {
	cfg := &Config{
		// Required fields (no defaults)
		ContractID:  os.Getenv("TB_CONTRACT_ID"),
		AssetID:     os.Getenv("TB_ASSET_ID"),
		EDCEndpoint: os.Getenv("TB_EDC_ENDPOINT"),

		// Optional fields with defaults
		TargetDir:   getEnv("TB_TARGET_DIR", DefaultTargetDir),
		PipePath:    getEnv("TB_PIPE_PATH", DefaultPipePath),
		ReadySignal: getEnv("TB_READY_SIGNAL", DefaultReadySignal),
		RuntimeURL:  getEnv("TB_RUNTIME_URL", DefaultRuntimeURL),
		PublicAddr:  getEnv("TB_PUBLIC_ADDR", DefaultPublicAddr),
		LogLevel:    strings.ToLower(getEnv("TB_LOG_LEVEL", DefaultLogLevel)),
	}

	// Parse integer fields
	var parseErrs ConfigErrors

	concurrency, err := getEnvInt("TB_DOWNLOAD_CONCURRENCY", DefaultDownloadConcurrency)
	if err != nil {
		parseErrs = append(parseErrs, &ValidationError{
			Field:   "TB_DOWNLOAD_CONCURRENCY",
			Message: err.Error(),
		})
	}
	cfg.DownloadConcurrency = concurrency

	chunkBytes, err := getEnvInt("TB_DOWNLOAD_CHUNK_BYTES", DefaultDownloadChunkBytes)
	if err != nil {
		parseErrs = append(parseErrs, &ValidationError{
			Field:   "TB_DOWNLOAD_CHUNK_BYTES",
			Message: err.Error(),
		})
	}
	cfg.DownloadChunkBytes = chunkBytes

	// Validate all fields
	validationErrs := cfg.Validate()
	allErrs := append(parseErrs, validationErrs...)

	if len(allErrs) > 0 {
		return nil, allErrs
	}

	return cfg, nil
}

// Validate checks that all configuration values are valid.
// Returns a slice of all validation errors found.
func (c *Config) Validate() []error {
	var errs []error

	// Required fields
	if c.ContractID == "" {
		errs = append(errs, &ValidationError{
			Field:   "TB_CONTRACT_ID",
			Message: "required but not set",
		})
	}

	if c.AssetID == "" {
		errs = append(errs, &ValidationError{
			Field:   "TB_ASSET_ID",
			Message: "required but not set",
		})
	}

	if c.EDCEndpoint == "" {
		errs = append(errs, &ValidationError{
			Field:   "TB_EDC_ENDPOINT",
			Message: "required but not set",
		})
	} else if err := validateURL(c.EDCEndpoint); err != nil {
		errs = append(errs, &ValidationError{
			Field:   "TB_EDC_ENDPOINT",
			Message: err.Error(),
		})
	}

	// Optional URL validation
	if c.RuntimeURL != "" {
		if err := validateURL(c.RuntimeURL); err != nil {
			errs = append(errs, &ValidationError{
				Field:   "TB_RUNTIME_URL",
				Message: err.Error(),
			})
		}
	}

	// Path validation (must be absolute)
	if c.TargetDir != "" && !strings.HasPrefix(c.TargetDir, "/") {
		errs = append(errs, &ValidationError{
			Field:   "TB_TARGET_DIR",
			Message: "must be an absolute path",
		})
	}

	if c.PipePath != "" && !strings.HasPrefix(c.PipePath, "/") {
		errs = append(errs, &ValidationError{
			Field:   "TB_PIPE_PATH",
			Message: "must be an absolute path",
		})
	}

	if c.ReadySignal != "" && !strings.HasPrefix(c.ReadySignal, "/") {
		errs = append(errs, &ValidationError{
			Field:   "TB_READY_SIGNAL",
			Message: "must be an absolute path",
		})
	}

	// Range validation
	if c.DownloadConcurrency < MinDownloadConcurrency || c.DownloadConcurrency > MaxDownloadConcurrency {
		errs = append(errs, &ValidationError{
			Field:   "TB_DOWNLOAD_CONCURRENCY",
			Message: fmt.Sprintf("must be between %d and %d, got %d", MinDownloadConcurrency, MaxDownloadConcurrency, c.DownloadConcurrency),
		})
	}

	if c.DownloadChunkBytes < MinDownloadChunkBytes || c.DownloadChunkBytes > MaxDownloadChunkBytes {
		errs = append(errs, &ValidationError{
			Field:   "TB_DOWNLOAD_CHUNK_BYTES",
			Message: fmt.Sprintf("must be between %d and %d, got %d", MinDownloadChunkBytes, MaxDownloadChunkBytes, c.DownloadChunkBytes),
		})
	}

	// Log level validation
	if !validLogLevels[c.LogLevel] {
		errs = append(errs, &ValidationError{
			Field:   "TB_LOG_LEVEL",
			Message: fmt.Sprintf("must be one of: debug, info, warn, error; got %q", c.LogLevel),
		})
	}

	return errs
}

// String returns a string representation of the config safe for logging.
// Sensitive values are redacted.
func (c *Config) String() string {
	return fmt.Sprintf(
		"Config{ContractID=%q, AssetID=%q, EDCEndpoint=%q, TargetDir=%q, PipePath=%q, ReadySignal=%q, RuntimeURL=%q, PublicAddr=%q, DownloadConcurrency=%d, DownloadChunkBytes=%d, LogLevel=%q}",
		c.ContractID,
		c.AssetID,
		c.EDCEndpoint,
		c.TargetDir,
		c.PipePath,
		c.ReadySignal,
		c.RuntimeURL,
		c.PublicAddr,
		c.DownloadConcurrency,
		c.DownloadChunkBytes,
		c.LogLevel,
	)
}

// getEnv returns the environment variable value or a default if not set.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt returns the environment variable value as an integer or a default if not set.
func getEnvInt(key string, defaultValue int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue, nil
	}

	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue, fmt.Errorf("invalid integer: %q", value)
	}

	return intValue, nil
}

// validateURL checks if a string is a valid URL with http or https scheme.
func validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("URL scheme must be http or https")
	}

	if parsed.Host == "" {
		return errors.New("URL must have a host")
	}

	return nil
}
