// Package crypto implements tbenc/v1 decryption for TrustBridge.
//
// The tbenc/v1 format is a chunked AES-256-GCM encryption format designed for
// streaming decryption of large model weight files.
package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// Magic header identifier
	Magic = "TBENC001"

	// Version is the format version
	Version uint16 = 1

	// AlgoAESGCMChunked represents the AES-256-GCM chunked algorithm
	AlgoAESGCMChunked uint8 = 1

	// HeaderSize is the total size of the tbenc/v1 header
	HeaderSize = 32

	// NoncePrefixSize is the size of the random nonce prefix
	NoncePrefixSize = 4

	// NonceSize is the total GCM nonce size (prefix + counter)
	NonceSize = 12

	// TagSize is the GCM authentication tag size
	TagSize = 16
)

// Header represents the parsed tbenc/v1 file header.
type Header struct {
	Magic       [8]byte
	Version     uint16
	Algo        uint8
	ChunkBytes  uint32
	NoncePrefix [4]byte
	Reserved    [13]byte
}

// ParseHeader reads and validates the 32-byte tbenc/v1 header.
func ParseHeader(r io.Reader) (*Header, error) {
	buf := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("failed to read header: %w", err)
	}

	h := &Header{}
	offset := 0

	// Parse magic
	copy(h.Magic[:], buf[offset:offset+8])
	offset += 8

	if string(h.Magic[:]) != Magic {
		return nil, fmt.Errorf("invalid magic: got %q, want %q", string(h.Magic[:]), Magic)
	}

	// Parse version (big-endian uint16)
	h.Version = binary.BigEndian.Uint16(buf[offset : offset+2])
	offset += 2

	if h.Version != Version {
		return nil, fmt.Errorf("unsupported version: got %d, want %d", h.Version, Version)
	}

	// Parse algo (uint8)
	h.Algo = buf[offset]
	offset++

	if h.Algo != AlgoAESGCMChunked {
		return nil, fmt.Errorf("unsupported algorithm: got %d, want %d", h.Algo, AlgoAESGCMChunked)
	}

	// Parse chunk_bytes (big-endian uint32)
	h.ChunkBytes = binary.BigEndian.Uint32(buf[offset : offset+4])
	offset += 4

	if h.ChunkBytes == 0 || h.ChunkBytes > 64*1024*1024 {
		return nil, fmt.Errorf("invalid chunk_bytes: %d (must be 1 to 64MB)", h.ChunkBytes)
	}

	// Parse nonce prefix
	copy(h.NoncePrefix[:], buf[offset:offset+4])
	offset += 4

	// Parse reserved (should be zeros, but we don't enforce)
	copy(h.Reserved[:], buf[offset:offset+13])

	return h, nil
}

// deriveNonce derives a 12-byte GCM nonce from the prefix and chunk index.
// Format: nonce_prefix (4 bytes) || counter (8 bytes, big-endian)
func deriveNonce(prefix [4]byte, chunkIndex uint64) []byte {
	nonce := make([]byte, NonceSize)
	copy(nonce[0:4], prefix[:])
	binary.BigEndian.PutUint64(nonce[4:12], chunkIndex)
	return nonce
}

// buildAAD constructs the Associated Authenticated Data for GCM verification.
// AAD = magic||version||algo||chunk_bytes||nonce_prefix||chunk_index||pt_len
func buildAAD(h *Header, chunkIndex uint64, ptLen uint32) []byte {
	aad := make([]byte, 0, 8+2+1+4+4+8+4)
	aad = append(aad, h.Magic[:]...)
	aad = binary.BigEndian.AppendUint16(aad, h.Version)
	aad = append(aad, h.Algo)
	aad = binary.BigEndian.AppendUint32(aad, h.ChunkBytes)
	aad = append(aad, h.NoncePrefix[:]...)
	aad = binary.BigEndian.AppendUint64(aad, chunkIndex)
	aad = binary.BigEndian.AppendUint32(aad, ptLen)
	return aad
}

// DecryptChunk decrypts a single chunk using AES-256-GCM.
//
// Args:
//   - key: 32-byte AES-256 key
//   - header: parsed file header
//   - chunkIndex: zero-based chunk index
//   - ptLen: plaintext length for this chunk
//   - ciphertextWithTag: encrypted data with appended 16-byte GCM tag
//
// Returns decrypted plaintext or error.
func DecryptChunk(key []byte, header *Header, chunkIndex uint64, ptLen uint32, ciphertextWithTag []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Derive nonce
	nonce := deriveNonce(header.NoncePrefix, chunkIndex)

	// Build AAD
	aad := buildAAD(header, chunkIndex, ptLen)

	// Decrypt (Open verifies the tag and decrypts)
	plaintext, err := gcm.Open(nil, nonce, ciphertextWithTag, aad)
	if err != nil {
		return nil, fmt.Errorf("decryption failed at chunk %d: %w", chunkIndex, err)
	}

	// Verify plaintext length matches expected
	if uint32(len(plaintext)) != ptLen {
		return nil, fmt.Errorf("plaintext length mismatch: got %d, expected %d", len(plaintext), ptLen)
	}

	return plaintext, nil
}

// DecryptToWriter decrypts a tbenc/v1 file and writes plaintext to a writer.
//
// This is the main decryption function used by the sentinel for streaming
// decryption to a FIFO or other writer.
//
// Args:
//   - r: reader for encrypted input (e.g., os.File)
//   - w: writer for plaintext output (e.g., FIFO, tmpfs file)
//   - key: 32-byte AES-256 decryption key
//
// Returns total bytes written or error.
func DecryptToWriter(r io.Reader, w io.Writer, key []byte) (int64, error) {
	// Parse header
	header, err := ParseHeader(r)
	if err != nil {
		return 0, fmt.Errorf("failed to parse header: %w", err)
	}

	var totalWritten int64
	chunkIndex := uint64(0)

	for {
		// Read record header: pt_len (uint32, big-endian)
		var ptLen uint32
		if err := binary.Read(r, binary.BigEndian, &ptLen); err != nil {
			if err == io.EOF {
				// Normal end of file
				break
			}
			return totalWritten, fmt.Errorf("failed to read pt_len at chunk %d: %w", chunkIndex, err)
		}

		// Validate pt_len
		if ptLen == 0 || ptLen > header.ChunkBytes {
			return totalWritten, fmt.Errorf("invalid pt_len %d at chunk %d (max %d)", ptLen, chunkIndex, header.ChunkBytes)
		}

		// Read ciphertext + tag
		ctWithTag := make([]byte, int(ptLen)+TagSize)
		if _, err := io.ReadFull(r, ctWithTag); err != nil {
			return totalWritten, fmt.Errorf("failed to read ciphertext at chunk %d: %w", chunkIndex, err)
		}

		// Decrypt chunk
		plaintext, err := DecryptChunk(key, header, chunkIndex, ptLen, ctWithTag)
		if err != nil {
			return totalWritten, err
		}

		// Write plaintext to output
		n, err := w.Write(plaintext)
		if err != nil {
			return totalWritten, fmt.Errorf("failed to write plaintext at chunk %d: %w", chunkIndex, err)
		}

		totalWritten += int64(n)

		// Security: best-effort overwrite plaintext buffer
		SecureZeroBytes(plaintext)

		chunkIndex++
	}

	return totalWritten, nil
}

// SecureZeroBytes overwrites a byte slice with zeros.
// Note: This is best-effort and may not prevent all memory attacks due to
// Go's GC potentially copying data, but it reduces exposure.
func SecureZeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// DecryptToBytes is a convenience function that decrypts an entire file to memory.
// Use with caution for large files.
func DecryptToBytes(r io.Reader, key []byte) ([]byte, error) {
	var buf bytes.Buffer
	_, err := DecryptToWriter(r, &buf, key)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
