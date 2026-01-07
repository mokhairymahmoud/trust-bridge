package asset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
)

// ComputeFileHash computes and returns the SHA256 hash of a file as a lowercase hex string.
// Uses buffered reading for efficient handling of large files.
func ComputeFileHash(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	return ComputeReaderHash(f)
}

// ComputeReaderHash computes the SHA256 hash from a reader and returns it as a lowercase hex string.
// This is useful for computing hashes during streaming operations.
func ComputeReaderHash(r io.Reader) (string, error) {
	h := sha256.New()

	// Use a 32KB buffer for efficient reading
	buf := make([]byte, 32*1024)
	if _, err := io.CopyBuffer(h, r, buf); err != nil {
		return "", fmt.Errorf("failed to compute hash: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyFileHash computes the SHA256 of the file and compares it to the expected hash.
// Returns nil if the hashes match, ErrHashMismatch if they don't match,
// or another error if the file cannot be read.
//
// The expected hash should be a 64-character lowercase or uppercase hex string.
func VerifyFileHash(filePath, expectedSHA256 string) error {
	// Normalize expected hash to lowercase
	expectedSHA256 = strings.ToLower(expectedSHA256)

	// Validate expected hash format
	if len(expectedSHA256) != 64 {
		return fmt.Errorf("invalid expected hash: must be 64 hex characters, got %d", len(expectedSHA256))
	}
	if _, err := hex.DecodeString(expectedSHA256); err != nil {
		return fmt.Errorf("invalid expected hash: not valid hex: %w", err)
	}

	// Compute actual hash
	actualHash, err := ComputeFileHash(filePath)
	if err != nil {
		return NewVerifyError(filePath, err)
	}

	// Compare hashes
	if actualHash != expectedSHA256 {
		return NewVerifyError(filePath, fmt.Errorf("%w: expected %s, got %s", ErrHashMismatch, expectedSHA256, actualHash))
	}

	return nil
}

// VerifyReaderHash computes the SHA256 from a reader and compares it to the expected hash.
// This is useful for verifying data during streaming operations without
// first writing to disk.
//
// Returns nil if the hashes match, ErrHashMismatch if they don't match,
// or another error if the reader fails.
func VerifyReaderHash(r io.Reader, expectedSHA256 string) error {
	// Normalize expected hash to lowercase
	expectedSHA256 = strings.ToLower(expectedSHA256)

	// Validate expected hash format
	if len(expectedSHA256) != 64 {
		return fmt.Errorf("invalid expected hash: must be 64 hex characters, got %d", len(expectedSHA256))
	}
	if _, err := hex.DecodeString(expectedSHA256); err != nil {
		return fmt.Errorf("invalid expected hash: not valid hex: %w", err)
	}

	// Compute actual hash
	actualHash, err := ComputeReaderHash(r)
	if err != nil {
		return err
	}

	// Compare hashes
	if actualHash != expectedSHA256 {
		return fmt.Errorf("%w: expected %s, got %s", ErrHashMismatch, expectedSHA256, actualHash)
	}

	return nil
}

// HashingReader wraps an io.Reader and computes a running SHA256 hash as data is read.
// Use this when you need to compute a hash while also using the data (e.g., writing to a file).
type HashingReader struct {
	r    io.Reader
	hash hash.Hash
}

// NewHashingReader creates a new HashingReader that computes SHA256 as data is read.
func NewHashingReader(r io.Reader) *HashingReader {
	return &HashingReader{
		r:    r,
		hash: sha256.New(),
	}
}

// Read implements io.Reader, updating the hash with each read.
func (hr *HashingReader) Read(p []byte) (n int, err error) {
	n, err = hr.r.Read(p)
	if n > 0 {
		hr.hash.Write(p[:n])
	}
	return n, err
}

// Sum returns the computed hash as a lowercase hex string.
// Should only be called after all data has been read.
func (hr *HashingReader) Sum() string {
	return hex.EncodeToString(hr.hash.Sum(nil))
}

// HashingWriter wraps an io.Writer and computes a running SHA256 hash as data is written.
// Use this when you need to compute a hash while also writing data.
type HashingWriter struct {
	w    io.Writer
	hash hash.Hash
}

// NewHashingWriter creates a new HashingWriter that computes SHA256 as data is written.
func NewHashingWriter(w io.Writer) *HashingWriter {
	return &HashingWriter{
		w:    w,
		hash: sha256.New(),
	}
}

// Write implements io.Writer, updating the hash with each write.
func (hw *HashingWriter) Write(p []byte) (n int, err error) {
	n, err = hw.w.Write(p)
	if n > 0 {
		hw.hash.Write(p[:n])
	}
	return n, err
}

// Sum returns the computed hash as a lowercase hex string.
// Should only be called after all data has been written.
func (hw *HashingWriter) Sum() string {
	return hex.EncodeToString(hw.hash.Sum(nil))
}
