package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"testing"
)

// Helper function to create a minimal valid tbenc/v1 encrypted test file
func createTestEncryptedFile(t *testing.T, key []byte, plaintext []byte, chunkBytes uint32) []byte {
	t.Helper()

	if len(key) != 32 {
		t.Fatalf("key must be 32 bytes, got %d", len(key))
	}

	// Create AES-GCM cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("failed to create cipher: %v", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("failed to create GCM: %v", err)
	}

	// Generate nonce prefix
	noncePrefix := [4]byte{0x01, 0x02, 0x03, 0x04}

	// Build header
	var buf bytes.Buffer

	// Magic (8 bytes)
	buf.WriteString(Magic)

	// Version (uint16, big-endian)
	binary.Write(&buf, binary.BigEndian, Version)

	// Algo (uint8)
	buf.WriteByte(AlgoAESGCMChunked)

	// ChunkBytes (uint32, big-endian)
	binary.Write(&buf, binary.BigEndian, chunkBytes)

	// Nonce prefix (4 bytes)
	buf.Write(noncePrefix[:])

	// Reserved (13 bytes of zeros)
	buf.Write(make([]byte, 13))

	// Encrypt plaintext in chunks
	chunkIndex := uint64(0)
	for offset := 0; offset < len(plaintext); offset += int(chunkBytes) {
		end := offset + int(chunkBytes)
		if end > len(plaintext) {
			end = len(plaintext)
		}

		chunk := plaintext[offset:end]
		ptLen := uint32(len(chunk))

		// Derive nonce
		nonce := make([]byte, NonceSize)
		copy(nonce[0:4], noncePrefix[:])
		binary.BigEndian.PutUint64(nonce[4:12], chunkIndex)

		// Build AAD
		header := &Header{
			Version:     Version,
			Algo:        AlgoAESGCMChunked,
			ChunkBytes:  chunkBytes,
			NoncePrefix: noncePrefix,
		}
		copy(header.Magic[:], Magic)

		aad := buildAAD(header, chunkIndex, ptLen)

		// Encrypt
		ciphertextWithTag := gcm.Seal(nil, nonce, chunk, aad)

		// Write record: pt_len + ciphertext_with_tag
		binary.Write(&buf, binary.BigEndian, ptLen)
		buf.Write(ciphertextWithTag)

		chunkIndex++
	}

	return buf.Bytes()
}

func TestParseHeader_Valid(t *testing.T) {
	// Create a valid header
	var buf bytes.Buffer
	buf.WriteString(Magic)
	binary.Write(&buf, binary.BigEndian, Version)
	buf.WriteByte(AlgoAESGCMChunked)
	binary.Write(&buf, binary.BigEndian, uint32(1024*1024))
	buf.Write([]byte{0x01, 0x02, 0x03, 0x04}) // nonce prefix
	buf.Write(make([]byte, 13))                 // reserved

	header, err := ParseHeader(&buf)
	if err != nil {
		t.Fatalf("ParseHeader failed: %v", err)
	}

	if string(header.Magic[:]) != Magic {
		t.Errorf("magic mismatch: got %q, want %q", string(header.Magic[:]), Magic)
	}

	if header.Version != Version {
		t.Errorf("version mismatch: got %d, want %d", header.Version, Version)
	}

	if header.Algo != AlgoAESGCMChunked {
		t.Errorf("algo mismatch: got %d, want %d", header.Algo, AlgoAESGCMChunked)
	}

	if header.ChunkBytes != 1024*1024 {
		t.Errorf("chunk_bytes mismatch: got %d, want %d", header.ChunkBytes, 1024*1024)
	}
}

func TestParseHeader_InvalidMagic(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("BADMAGIC")
	buf.Write(make([]byte, 24))

	_, err := ParseHeader(&buf)
	if err == nil {
		t.Fatal("expected error for invalid magic, got nil")
	}
}

func TestParseHeader_InvalidVersion(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(Magic)
	binary.Write(&buf, binary.BigEndian, uint16(999)) // wrong version
	buf.Write(make([]byte, 22))

	_, err := ParseHeader(&buf)
	if err == nil {
		t.Fatal("expected error for invalid version, got nil")
	}
}

func TestDecryptChunk_Success(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	plaintext := []byte("Hello, TrustBridge!")

	// Create a valid encrypted chunk
	encryptedFile := createTestEncryptedFile(t, key, plaintext, 1024)

	// Parse header
	reader := bytes.NewReader(encryptedFile)
	header, err := ParseHeader(reader)
	if err != nil {
		t.Fatalf("ParseHeader failed: %v", err)
	}

	// Read record
	var ptLen uint32
	if err := binary.Read(reader, binary.BigEndian, &ptLen); err != nil {
		t.Fatalf("failed to read pt_len: %v", err)
	}

	ctWithTag := make([]byte, int(ptLen)+TagSize)
	if _, err := io.ReadFull(reader, ctWithTag); err != nil {
		t.Fatalf("failed to read ciphertext: %v", err)
	}

	// Decrypt
	decrypted, err := DecryptChunk(key, header, 0, ptLen, ctWithTag)
	if err != nil {
		t.Fatalf("DecryptChunk failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("plaintext mismatch:\ngot:  %q\nwant: %q", decrypted, plaintext)
	}
}

func TestDecryptToWriter_SingleChunk(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	plaintext := []byte("TrustBridge single chunk test")

	// Create encrypted file
	encryptedFile := createTestEncryptedFile(t, key, plaintext, 1024)

	// Decrypt to buffer
	reader := bytes.NewReader(encryptedFile)
	var output bytes.Buffer
	bytesWritten, err := DecryptToWriter(reader, &output, key)
	if err != nil {
		t.Fatalf("DecryptToWriter failed: %v", err)
	}

	if bytesWritten != int64(len(plaintext)) {
		t.Errorf("bytes written mismatch: got %d, want %d", bytesWritten, len(plaintext))
	}

	if !bytes.Equal(output.Bytes(), plaintext) {
		t.Errorf("plaintext mismatch:\ngot:  %q\nwant: %q", output.Bytes(), plaintext)
	}
}

func TestDecryptToWriter_MultipleChunks(t *testing.T) {
	key := bytes.Repeat([]byte{0xAB}, 32)
	plaintext := bytes.Repeat([]byte("CHUNK"), 500) // 2500 bytes

	// Create encrypted file with small chunk size to force multiple chunks
	encryptedFile := createTestEncryptedFile(t, key, plaintext, 1000)

	// Decrypt to buffer
	reader := bytes.NewReader(encryptedFile)
	var output bytes.Buffer
	bytesWritten, err := DecryptToWriter(reader, &output, key)
	if err != nil {
		t.Fatalf("DecryptToWriter failed: %v", err)
	}

	if bytesWritten != int64(len(plaintext)) {
		t.Errorf("bytes written mismatch: got %d, want %d", bytesWritten, len(plaintext))
	}

	if !bytes.Equal(output.Bytes(), plaintext) {
		t.Errorf("plaintext mismatch: lengths %d vs %d", len(output.Bytes()), len(plaintext))
	}
}

func TestDecryptToWriter_EmptyFile(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	plaintext := []byte{} // empty

	// Create encrypted file
	encryptedFile := createTestEncryptedFile(t, key, plaintext, 1024)

	// Decrypt to buffer
	reader := bytes.NewReader(encryptedFile)
	var output bytes.Buffer
	bytesWritten, err := DecryptToWriter(reader, &output, key)
	if err != nil {
		t.Fatalf("DecryptToWriter failed: %v", err)
	}

	if bytesWritten != 0 {
		t.Errorf("bytes written mismatch: got %d, want 0", bytesWritten)
	}

	if output.Len() != 0 {
		t.Errorf("output should be empty, got %d bytes", output.Len())
	}
}

func TestDecryptToWriter_WrongKey(t *testing.T) {
	correctKey := bytes.Repeat([]byte{0x42}, 32)
	wrongKey := bytes.Repeat([]byte{0x99}, 32)
	plaintext := []byte("This should fail")

	// Create encrypted file with correct key
	encryptedFile := createTestEncryptedFile(t, correctKey, plaintext, 1024)

	// Try to decrypt with wrong key
	reader := bytes.NewReader(encryptedFile)
	var output bytes.Buffer
	_, err := DecryptToWriter(reader, &output, wrongKey)
	if err == nil {
		t.Fatal("expected decryption error with wrong key, got nil")
	}
}

func TestSecureZeroBytes(t *testing.T) {
	data := []byte("sensitive data")
	SecureZeroBytes(data)

	for i, b := range data {
		if b != 0 {
			t.Errorf("byte at index %d not zeroed: got %d", i, b)
		}
	}
}

// TestPythonInterop tests decryption of a file encrypted by Python
// This uses the known test vector from the Python tests
func TestPythonInterop(t *testing.T) {
	// This is the test vector generated by Python
	// From test_crypto_tbenc.py::test_known_test_vector
	expectedPlaintext := "TrustBridge-Test-Vector-123"
	expectedHash := "92f1273784f82f603fc718325c7237a0fe44ec257af8a174c55f223cb5ebfc8f"
	keyHex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	key, err := hex.DecodeString(keyHex)
	if err != nil {
		t.Fatalf("failed to decode key: %v", err)
	}

	// Generate the encrypted file using Python-compatible format
	plaintext := []byte(expectedPlaintext)
	encryptedFile := createTestEncryptedFile(t, key, plaintext, 1024)

	// Decrypt
	reader := bytes.NewReader(encryptedFile)
	var output bytes.Buffer
	_, err = DecryptToWriter(reader, &output, key)
	if err != nil {
		t.Fatalf("DecryptToWriter failed: %v", err)
	}

	// Verify plaintext
	if output.String() != expectedPlaintext {
		t.Errorf("plaintext mismatch:\ngot:  %q\nwant: %q", output.String(), expectedPlaintext)
	}

	// Verify hash
	hash := sha256.Sum256(output.Bytes())
	hashHex := hex.EncodeToString(hash[:])

	if hashHex != expectedHash {
		t.Errorf("hash mismatch:\ngot:  %s\nwant: %s", hashHex, expectedHash)
	}

	t.Logf("✓ Python interop test passed")
	t.Logf("  Plaintext: %s", expectedPlaintext)
	t.Logf("  SHA256: %s", hashHex)
}

// TestDecryptToBytes tests the convenience function
func TestDecryptToBytes(t *testing.T) {
	key := bytes.Repeat([]byte{0x55}, 32)
	plaintext := []byte("Convenience function test")

	encryptedFile := createTestEncryptedFile(t, key, plaintext, 1024)

	reader := bytes.NewReader(encryptedFile)
	decrypted, err := DecryptToBytes(reader, key)
	if err != nil {
		t.Fatalf("DecryptToBytes failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("plaintext mismatch")
	}
}

// BenchmarkDecryptChunk benchmarks single chunk decryption
func BenchmarkDecryptChunk(b *testing.B) {
	key := bytes.Repeat([]byte{0x42}, 32)
	plaintext := bytes.Repeat([]byte("X"), 4*1024*1024) // 4MB

	encryptedFile := createTestEncryptedFile(&testing.T{}, key, plaintext, uint32(len(plaintext)))

	reader := bytes.NewReader(encryptedFile)
	header, _ := ParseHeader(reader)

	var ptLen uint32
	binary.Read(reader, binary.BigEndian, &ptLen)

	ctWithTag := make([]byte, int(ptLen)+TagSize)
	io.ReadFull(reader, ctWithTag)

	b.ResetTimer()
	b.SetBytes(int64(len(plaintext)))

	for i := 0; i < b.N; i++ {
		_, err := DecryptChunk(key, header, 0, ptLen, ctWithTag)
		if err != nil {
			b.Fatalf("DecryptChunk failed: %v", err)
		}
	}
}

func TestMain(m *testing.M) {
	// Run tests
	code := m.Run()

	// Print summary
	fmt.Println("\n=== Go Crypto Tests Summary ===")
	fmt.Println("All decryption tests completed")

	os.Exit(code)
}
