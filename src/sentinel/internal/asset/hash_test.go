package asset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// computeExpectedHash is a helper that computes the SHA256 hash of data.
func computeExpectedHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestComputeFileHash_Success(t *testing.T) {
	// Create a temporary file with known content
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.bin")

	testData := []byte("Hello, TrustBridge!")
	if err := os.WriteFile(filePath, testData, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	expectedHash := computeExpectedHash(testData)

	hash, err := ComputeFileHash(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", hash, expectedHash)
	}
}

func TestComputeFileHash_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "empty.bin")

	if err := os.WriteFile(filePath, []byte{}, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	expectedHash := computeExpectedHash([]byte{})

	hash, err := ComputeFileHash(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", hash, expectedHash)
	}
}

func TestComputeFileHash_LargeFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "large.bin")

	// Create a 10MB file with repeating pattern
	size := 10 * 1024 * 1024
	pattern := []byte("TRUSTBRIDGE_TEST_PATTERN_")
	testData := make([]byte, size)
	for i := 0; i < size; i++ {
		testData[i] = pattern[i%len(pattern)]
	}

	if err := os.WriteFile(filePath, testData, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	expectedHash := computeExpectedHash(testData)

	hash, err := ComputeFileHash(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", hash, expectedHash)
	}
}

func TestComputeFileHash_FileNotFound(t *testing.T) {
	_, err := ComputeFileHash("/nonexistent/path/to/file.bin")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestComputeReaderHash_Success(t *testing.T) {
	testData := []byte("Test data for reader hash")
	r := bytes.NewReader(testData)

	expectedHash := computeExpectedHash(testData)

	hash, err := ComputeReaderHash(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", hash, expectedHash)
	}
}

func TestComputeReaderHash_EmptyReader(t *testing.T) {
	r := bytes.NewReader([]byte{})
	expectedHash := computeExpectedHash([]byte{})

	hash, err := ComputeReaderHash(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", hash, expectedHash)
	}
}

func TestVerifyFileHash_Match(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.bin")

	testData := []byte("Verification test data")
	if err := os.WriteFile(filePath, testData, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	expectedHash := computeExpectedHash(testData)

	err := VerifyFileHash(filePath, expectedHash)
	if err != nil {
		t.Errorf("expected verification to pass, got error: %v", err)
	}
}

func TestVerifyFileHash_MatchUppercase(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.bin")

	testData := []byte("Verification test data")
	if err := os.WriteFile(filePath, testData, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Use uppercase hash
	expectedHash := strings.ToUpper(computeExpectedHash(testData))

	err := VerifyFileHash(filePath, expectedHash)
	if err != nil {
		t.Errorf("expected verification with uppercase hash to pass, got error: %v", err)
	}
}

func TestVerifyFileHash_Mismatch(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.bin")

	testData := []byte("Original data")
	if err := os.WriteFile(filePath, testData, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Use hash of different data
	wrongHash := computeExpectedHash([]byte("Different data"))

	err := VerifyFileHash(filePath, wrongHash)
	if err == nil {
		t.Fatal("expected hash mismatch error, got nil")
	}
	if !errors.Is(err, ErrHashMismatch) {
		t.Errorf("expected ErrHashMismatch, got %v", err)
	}
}

func TestVerifyFileHash_InvalidHashFormat(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.bin")

	if err := os.WriteFile(filePath, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	tests := []struct {
		name string
		hash string
	}{
		{name: "too short", hash: "abc123"},
		{name: "too long", hash: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2extra"},
		{name: "invalid hex", hash: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyFileHash(filePath, tt.hash)
			if err == nil {
				t.Error("expected error for invalid hash format, got nil")
			}
		})
	}
}

func TestVerifyFileHash_FileNotFound(t *testing.T) {
	validHash := computeExpectedHash([]byte("anything"))
	err := VerifyFileHash("/nonexistent/file.bin", validHash)
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestVerifyReaderHash_Match(t *testing.T) {
	testData := []byte("Reader verification test")
	r := bytes.NewReader(testData)
	expectedHash := computeExpectedHash(testData)

	err := VerifyReaderHash(r, expectedHash)
	if err != nil {
		t.Errorf("expected verification to pass, got error: %v", err)
	}
}

func TestVerifyReaderHash_Mismatch(t *testing.T) {
	testData := []byte("Original reader data")
	r := bytes.NewReader(testData)
	wrongHash := computeExpectedHash([]byte("Different data"))

	err := VerifyReaderHash(r, wrongHash)
	if err == nil {
		t.Fatal("expected hash mismatch error, got nil")
	}
	if !errors.Is(err, ErrHashMismatch) {
		t.Errorf("expected ErrHashMismatch, got %v", err)
	}
}

func TestHashingReader(t *testing.T) {
	testData := []byte("Data to hash while reading")
	originalReader := bytes.NewReader(testData)

	hr := NewHashingReader(originalReader)

	// Read all data
	buf := make([]byte, len(testData))
	n, err := io.ReadFull(hr, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(testData) {
		t.Errorf("expected to read %d bytes, got %d", len(testData), n)
	}
	if !bytes.Equal(buf, testData) {
		t.Error("read data doesn't match original")
	}

	// Check hash
	expectedHash := computeExpectedHash(testData)
	actualHash := hr.Sum()
	if actualHash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", actualHash, expectedHash)
	}
}

func TestHashingReader_PartialReads(t *testing.T) {
	testData := []byte("Data to hash in chunks")
	originalReader := bytes.NewReader(testData)

	hr := NewHashingReader(originalReader)

	// Read in small chunks
	var allData []byte
	buf := make([]byte, 5)
	for {
		n, err := hr.Read(buf)
		if n > 0 {
			allData = append(allData, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if !bytes.Equal(allData, testData) {
		t.Error("read data doesn't match original")
	}

	expectedHash := computeExpectedHash(testData)
	actualHash := hr.Sum()
	if actualHash != expectedHash {
		t.Errorf("hash mismatch after partial reads: got %s, want %s", actualHash, expectedHash)
	}
}

func TestHashingWriter(t *testing.T) {
	testData := []byte("Data to hash while writing")
	var buf bytes.Buffer

	hw := NewHashingWriter(&buf)

	// Write data
	n, err := hw.Write(testData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(testData) {
		t.Errorf("expected to write %d bytes, got %d", len(testData), n)
	}
	if !bytes.Equal(buf.Bytes(), testData) {
		t.Error("written data doesn't match original")
	}

	// Check hash
	expectedHash := computeExpectedHash(testData)
	actualHash := hw.Sum()
	if actualHash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", actualHash, expectedHash)
	}
}

func TestHashingWriter_MultipleWrites(t *testing.T) {
	var buf bytes.Buffer
	hw := NewHashingWriter(&buf)

	chunks := [][]byte{
		[]byte("First chunk "),
		[]byte("Second chunk "),
		[]byte("Third chunk"),
	}

	var allData []byte
	for _, chunk := range chunks {
		n, err := hw.Write(chunk)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != len(chunk) {
			t.Errorf("expected to write %d bytes, got %d", len(chunk), n)
		}
		allData = append(allData, chunk...)
	}

	if !bytes.Equal(buf.Bytes(), allData) {
		t.Error("written data doesn't match expected")
	}

	expectedHash := computeExpectedHash(allData)
	actualHash := hw.Sum()
	if actualHash != expectedHash {
		t.Errorf("hash mismatch after multiple writes: got %s, want %s", actualHash, expectedHash)
	}
}

func TestHashingReader_EmptyRead(t *testing.T) {
	hr := NewHashingReader(bytes.NewReader([]byte{}))

	buf := make([]byte, 10)
	n, err := hr.Read(buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got err=%v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 bytes, got %d", n)
	}

	expectedHash := computeExpectedHash([]byte{})
	actualHash := hr.Sum()
	if actualHash != expectedHash {
		t.Errorf("hash mismatch for empty read: got %s, want %s", actualHash, expectedHash)
	}
}

// BenchmarkComputeFileHash measures performance for different file sizes.
func BenchmarkComputeFileHash_1MB(b *testing.B) {
	benchmarkComputeFileHash(b, 1*1024*1024)
}

func BenchmarkComputeFileHash_10MB(b *testing.B) {
	benchmarkComputeFileHash(b, 10*1024*1024)
}

func BenchmarkComputeFileHash_100MB(b *testing.B) {
	benchmarkComputeFileHash(b, 100*1024*1024)
}

func benchmarkComputeFileHash(b *testing.B, size int) {
	tmpDir := b.TempDir()
	filePath := filepath.Join(tmpDir, "bench.bin")

	// Create test file
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		b.Fatalf("failed to create test file: %v", err)
	}

	b.ResetTimer()
	b.SetBytes(int64(size))

	for i := 0; i < b.N; i++ {
		_, err := ComputeFileHash(filePath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHashingReader(b *testing.B) {
	size := 10 * 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	b.SetBytes(int64(size))

	for i := 0; i < b.N; i++ {
		r := bytes.NewReader(data)
		hr := NewHashingReader(r)
		io.Copy(io.Discard, hr)
		_ = hr.Sum()
	}
}

func BenchmarkHashingWriter(b *testing.B) {
	size := 10 * 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	b.SetBytes(int64(size))

	for i := 0; i < b.N; i++ {
		hw := NewHashingWriter(io.Discard)
		hw.Write(data)
		_ = hw.Sum()
	}
}
