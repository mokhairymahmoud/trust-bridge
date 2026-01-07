package asset

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Manifest represents the parsed JSON manifest for a tbenc/v1 encrypted asset.
// The manifest contains metadata about the encrypted file and is used for
// integrity verification after download.
type Manifest struct {
	Format           string `json:"format"`             // Must be "tbenc/v1"
	Algo             string `json:"algo"`               // Must be "aes-256-gcm-chunked"
	ChunkBytes       int64  `json:"chunk_bytes"`        // Size of encryption chunks
	PlaintextBytes   int64  `json:"plaintext_bytes"`    // Total size of original plaintext
	SHA256Ciphertext string `json:"sha256_ciphertext"`  // SHA256 hash of encrypted file (64 hex chars)
	AssetID          string `json:"asset_id"`           // Asset identifier
	WeightsFilename  string `json:"weights_filename"`   // Filename of the encrypted weights file
}

// ManifestValidationError represents a specific validation failure.
type ManifestValidationError struct {
	Field   string
	Message string
}

func (e *ManifestValidationError) Error() string {
	return fmt.Sprintf("manifest validation: %s: %s", e.Field, e.Message)
}

const (
	// Expected values for manifest validation
	expectedFormat = "tbenc/v1"
	expectedAlgo   = "aes-256-gcm-chunked"

	// Default timeout for manifest download
	defaultManifestTimeout = 30 * time.Second

	// Maximum manifest size (1MB should be plenty)
	maxManifestSize = 1 * 1024 * 1024
)

// Validate checks that all required fields are present and valid.
// Returns nil if the manifest is valid, otherwise returns an error
// describing the first validation failure found.
func (m *Manifest) Validate() error {
	// Check format
	if m.Format == "" {
		return &ManifestValidationError{Field: "format", Message: "required but not set"}
	}
	if m.Format != expectedFormat {
		return &ManifestValidationError{
			Field:   "format",
			Message: fmt.Sprintf("must be %q, got %q", expectedFormat, m.Format),
		}
	}

	// Check algo
	if m.Algo == "" {
		return &ManifestValidationError{Field: "algo", Message: "required but not set"}
	}
	if m.Algo != expectedAlgo {
		return &ManifestValidationError{
			Field:   "algo",
			Message: fmt.Sprintf("must be %q, got %q", expectedAlgo, m.Algo),
		}
	}

	// Check chunk_bytes
	if m.ChunkBytes <= 0 {
		return &ManifestValidationError{
			Field:   "chunk_bytes",
			Message: fmt.Sprintf("must be positive, got %d", m.ChunkBytes),
		}
	}

	// Check plaintext_bytes
	if m.PlaintextBytes < 0 {
		return &ManifestValidationError{
			Field:   "plaintext_bytes",
			Message: fmt.Sprintf("must be non-negative, got %d", m.PlaintextBytes),
		}
	}

	// Check sha256_ciphertext
	if m.SHA256Ciphertext == "" {
		return &ManifestValidationError{Field: "sha256_ciphertext", Message: "required but not set"}
	}
	if len(m.SHA256Ciphertext) != 64 {
		return &ManifestValidationError{
			Field:   "sha256_ciphertext",
			Message: fmt.Sprintf("must be 64 hex characters, got %d", len(m.SHA256Ciphertext)),
		}
	}
	// Validate it's actually hex
	if _, err := hex.DecodeString(m.SHA256Ciphertext); err != nil {
		return &ManifestValidationError{
			Field:   "sha256_ciphertext",
			Message: fmt.Sprintf("must be valid hex: %v", err),
		}
	}

	// Check asset_id
	if m.AssetID == "" {
		return &ManifestValidationError{Field: "asset_id", Message: "required but not set"}
	}

	// Check weights_filename
	if m.WeightsFilename == "" {
		return &ManifestValidationError{Field: "weights_filename", Message: "required but not set"}
	}

	return nil
}

// ParseManifest parses manifest JSON from a reader.
// Returns the parsed Manifest or an error if parsing fails.
// This function does NOT validate the manifest - call Validate() separately.
func ParseManifest(r io.Reader) (*Manifest, error) {
	// Limit the reader to prevent memory exhaustion
	limited := io.LimitReader(r, maxManifestSize)

	var m Manifest
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("failed to parse manifest JSON: %w", err)
	}

	return &m, nil
}

// DownloadManifest fetches and parses the manifest from the given URL.
// It validates the manifest after parsing and returns an error if validation fails.
// Uses a default HTTP client with a 30-second timeout.
func DownloadManifest(ctx context.Context, url string) (*Manifest, error) {
	client := &http.Client{
		Timeout: defaultManifestTimeout,
	}
	return DownloadManifestWithClient(ctx, client, url)
}

// DownloadManifestWithClient fetches and parses the manifest using a custom HTTP client.
// This allows for custom timeouts, transport configurations, or testing with mock clients.
func DownloadManifestWithClient(ctx context.Context, client *http.Client, manifestURL string) (*Manifest, error) {
	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", manifestURL, nil)
	if err != nil {
		return nil, NewManifestError(manifestURL, 0, fmt.Errorf("%w: %v", ErrInvalidURL, err))
	}

	req.Header.Set("Accept", "application/json")

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return nil, NewNetworkError("manifest", manifestURL, err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		// Read error body for debugging (limited size)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, NewManifestError(
			manifestURL,
			resp.StatusCode,
			fmt.Errorf("%w: status %d: %s", ErrManifestDownloadFailed, resp.StatusCode, string(body)),
		)
	}

	// Parse manifest
	manifest, err := ParseManifest(resp.Body)
	if err != nil {
		return nil, NewManifestError(manifestURL, 0, fmt.Errorf("%w: %v", ErrManifestInvalid, err))
	}

	// Validate manifest
	if err := manifest.Validate(); err != nil {
		return nil, NewManifestError(manifestURL, 0, fmt.Errorf("%w: %v", ErrManifestInvalid, err))
	}

	return manifest, nil
}

// CiphertextSize calculates the expected size of the encrypted file based on manifest data.
// This is useful for pre-allocating download buffers or validating downloaded file size.
// The calculation accounts for:
//   - 32-byte header
//   - For each chunk: 4-byte length prefix + plaintext_len + 16-byte GCM tag
func (m *Manifest) CiphertextSize() int64 {
	if m.PlaintextBytes == 0 {
		return 32 // Just header for empty file
	}

	// Header size
	size := int64(32)

	// Calculate number of full chunks and remainder
	fullChunks := m.PlaintextBytes / m.ChunkBytes
	remainder := m.PlaintextBytes % m.ChunkBytes

	// Each chunk record: 4-byte pt_len + pt_len + 16-byte tag
	chunkOverhead := int64(4 + 16) // Length prefix + GCM tag

	// Full chunks
	size += fullChunks * (m.ChunkBytes + chunkOverhead)

	// Remainder chunk (if any)
	if remainder > 0 {
		size += remainder + chunkOverhead
	}

	return size
}
