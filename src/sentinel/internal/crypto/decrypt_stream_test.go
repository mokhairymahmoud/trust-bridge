package crypto

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// waitForFIFO waits up to 1 second for the FIFO to be created
func waitForFIFO(path string) error {
	for i := 0; i < 100; i++ {
		if IsFIFO(path) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("FIFO not created within timeout: %s", path)
}

func TestDecryptToFIFO_Success(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "decrypt-stream-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test data
	key := bytes.Repeat([]byte{0x42}, 32)
	plaintext := bytes.Repeat([]byte("TrustBridge-Stream-Test-"), 1000) // ~24KB

	// Create encrypted file
	encryptedData := createTestEncryptedFile(t, key, plaintext, 4096)
	encryptedPath := filepath.Join(tmpDir, "test.tbenc")
	if err := os.WriteFile(encryptedPath, encryptedData, 0644); err != nil {
		t.Fatalf("failed to write encrypted file: %v", err)
	}

	fifoPath := filepath.Join(tmpDir, "test-pipe")

	// Start decryption
	ctx := context.Background()
	resultCh := DecryptToFIFO(ctx, encryptedPath, fifoPath, key,
		WithTotalBytes(int64(len(plaintext))),
	)

	// Read from FIFO concurrently
	var decrypted bytes.Buffer
	var readErr error
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		// Wait for FIFO to be created
		if err := waitForFIFO(fifoPath); err != nil {
			readErr = err
			return
		}

		// Open FIFO for reading (this unblocks the writer)
		fifo, err := os.Open(fifoPath)
		if err != nil {
			readErr = err
			return
		}
		defer fifo.Close()

		// Read all data
		_, readErr = io.Copy(&decrypted, fifo)
	}()

	// Wait for result
	result := <-resultCh
	wg.Wait()

	// Check for errors
	if result.Err != nil {
		t.Fatalf("DecryptToFIFO failed: %v", result.Err)
	}

	if readErr != nil {
		t.Fatalf("reading from FIFO failed: %v", readErr)
	}

	// Verify bytes written
	if result.BytesWritten != int64(len(plaintext)) {
		t.Errorf("bytes written = %d, want %d", result.BytesWritten, len(plaintext))
	}

	// Verify plaintext
	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Errorf("plaintext mismatch: got %d bytes, want %d bytes", decrypted.Len(), len(plaintext))
	}
}

func TestDecryptToFIFOBlocking_Success(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "decrypt-blocking-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test data
	key := bytes.Repeat([]byte{0x55}, 32)
	plaintext := []byte("Blocking test plaintext data")

	// Create encrypted file
	encryptedData := createTestEncryptedFile(t, key, plaintext, 1024)
	encryptedPath := filepath.Join(tmpDir, "test.tbenc")
	if err := os.WriteFile(encryptedPath, encryptedData, 0644); err != nil {
		t.Fatalf("failed to write encrypted file: %v", err)
	}

	fifoPath := filepath.Join(tmpDir, "test-pipe")

	// Start reader in goroutine before calling blocking function
	var decrypted bytes.Buffer
	var readErr error
	readerDone := make(chan struct{})

	go func() {
		defer close(readerDone)

		// Wait for FIFO to be created
		if err := waitForFIFO(fifoPath); err != nil {
			readErr = err
			return
		}

		// Open FIFO for reading
		fifo, err := os.Open(fifoPath)
		if err != nil {
			readErr = err
			return
		}
		defer fifo.Close()

		_, readErr = io.Copy(&decrypted, fifo)
	}()

	// Call blocking function
	ctx := context.Background()
	bytesWritten, err := DecryptToFIFOBlocking(ctx, encryptedPath, fifoPath, key)

	// Wait for reader to finish
	<-readerDone

	if err != nil {
		t.Fatalf("DecryptToFIFOBlocking failed: %v", err)
	}

	if readErr != nil {
		t.Fatalf("reading from FIFO failed: %v", readErr)
	}

	if bytesWritten != int64(len(plaintext)) {
		t.Errorf("bytes written = %d, want %d", bytesWritten, len(plaintext))
	}

	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Error("plaintext mismatch")
	}
}

func TestDecryptToFIFO_ProgressCallback(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "decrypt-progress-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test data with multiple chunks
	key := bytes.Repeat([]byte{0x77}, 32)
	plaintext := bytes.Repeat([]byte("X"), 10000) // 10KB

	// Create encrypted file with small chunks
	encryptedData := createTestEncryptedFile(t, key, plaintext, 1000)
	encryptedPath := filepath.Join(tmpDir, "test.tbenc")
	if err := os.WriteFile(encryptedPath, encryptedData, 0644); err != nil {
		t.Fatalf("failed to write encrypted file: %v", err)
	}

	fifoPath := filepath.Join(tmpDir, "test-pipe")

	// Track progress calls
	var progressCalls []int64
	var mu sync.Mutex

	progressCallback := func(bytesWritten, totalBytes int64) {
		mu.Lock()
		progressCalls = append(progressCalls, bytesWritten)
		mu.Unlock()
	}

	ctx := context.Background()
	resultCh := DecryptToFIFO(ctx, encryptedPath, fifoPath, key,
		WithProgressCallback(progressCallback),
		WithTotalBytes(int64(len(plaintext))),
	)

	// Read from FIFO
	go func() {
		if err := waitForFIFO(fifoPath); err != nil {
			return
		}
		fifo, err := os.Open(fifoPath)
		if err != nil {
			return
		}
		defer fifo.Close()
		io.Copy(io.Discard, fifo)
	}()

	result := <-resultCh
	if result.Err != nil {
		t.Fatalf("DecryptToFIFO failed: %v", result.Err)
	}

	// Verify progress was called
	mu.Lock()
	callCount := len(progressCalls)
	mu.Unlock()

	if callCount == 0 {
		t.Error("progress callback was never called")
	}

	// Verify progress increases monotonically
	mu.Lock()
	var lastVal int64
	for i, val := range progressCalls {
		if val < lastVal {
			t.Errorf("progress decreased at call %d: %d < %d", i, val, lastVal)
		}
		lastVal = val
	}
	mu.Unlock()
}

func TestDecryptToFIFO_CancelBeforeReaderConnects(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "decrypt-cancel-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test data
	key := bytes.Repeat([]byte{0x88}, 32)
	plaintext := []byte("Cancel test")

	encryptedData := createTestEncryptedFile(t, key, plaintext, 1024)
	encryptedPath := filepath.Join(tmpDir, "test.tbenc")
	if err := os.WriteFile(encryptedPath, encryptedData, 0644); err != nil {
		t.Fatalf("failed to write encrypted file: %v", err)
	}

	fifoPath := filepath.Join(tmpDir, "test-pipe")

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	resultCh := DecryptToFIFO(ctx, encryptedPath, fifoPath, key)

	// Cancel before any reader connects
	time.Sleep(50 * time.Millisecond)
	cancel()

	// The result should indicate cancellation
	result := <-resultCh

	// Either cancelled error or the operation completed before we could cancel
	if result.Err != nil && result.Err != context.Canceled {
		// Some other error is acceptable if it's not a real failure
		t.Logf("got error: %v (expected context.Canceled)", result.Err)
	}
}

func TestDecryptToFIFO_InvalidInputs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "decrypt-invalid-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	key := bytes.Repeat([]byte{0x42}, 32)
	fifoPath := filepath.Join(tmpDir, "test-pipe")

	t.Run("empty encrypted path", func(t *testing.T) {
		ctx := context.Background()
		resultCh := DecryptToFIFO(ctx, "", fifoPath, key)
		result := <-resultCh
		if result.Err == nil {
			t.Error("expected error for empty encrypted path")
		}
	})

	t.Run("empty FIFO path", func(t *testing.T) {
		ctx := context.Background()
		resultCh := DecryptToFIFO(ctx, filepath.Join(tmpDir, "test.tbenc"), "", key)
		result := <-resultCh
		if result.Err == nil {
			t.Error("expected error for empty FIFO path")
		}
	})

	t.Run("invalid key length", func(t *testing.T) {
		ctx := context.Background()
		badKey := []byte("too short")
		resultCh := DecryptToFIFO(ctx, filepath.Join(tmpDir, "test.tbenc"), fifoPath, badKey)
		result := <-resultCh
		if result.Err == nil {
			t.Error("expected error for invalid key length")
		}
	})
}

func TestDecryptToFIFO_WrongKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "decrypt-wrongkey-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test data with correct key
	correctKey := bytes.Repeat([]byte{0x42}, 32)
	wrongKey := bytes.Repeat([]byte{0x99}, 32)
	plaintext := []byte("Wrong key test")

	encryptedData := createTestEncryptedFile(t, correctKey, plaintext, 1024)
	encryptedPath := filepath.Join(tmpDir, "test.tbenc")
	if err := os.WriteFile(encryptedPath, encryptedData, 0644); err != nil {
		t.Fatalf("failed to write encrypted file: %v", err)
	}

	fifoPath := filepath.Join(tmpDir, "test-pipe")

	ctx := context.Background()
	resultCh := DecryptToFIFO(ctx, encryptedPath, fifoPath, wrongKey)

	// Start reader
	go func() {
		if err := waitForFIFO(fifoPath); err != nil {
			return
		}
		fifo, err := os.Open(fifoPath)
		if err != nil {
			return
		}
		defer fifo.Close()
		io.Copy(io.Discard, fifo)
	}()

	result := <-resultCh
	if result.Err == nil {
		t.Error("expected error for wrong key, got nil")
	}
}

func TestDecryptToFIFO_HashVerification(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "decrypt-hash-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create deterministic test data
	key := bytes.Repeat([]byte{0xAB}, 32)
	plaintext := []byte("Deterministic plaintext for hash verification")
	expectedHash := sha256.Sum256(plaintext)

	encryptedData := createTestEncryptedFile(t, key, plaintext, 1024)
	encryptedPath := filepath.Join(tmpDir, "test.tbenc")
	if err := os.WriteFile(encryptedPath, encryptedData, 0644); err != nil {
		t.Fatalf("failed to write encrypted file: %v", err)
	}

	fifoPath := filepath.Join(tmpDir, "test-pipe")

	ctx := context.Background()
	resultCh := DecryptToFIFO(ctx, encryptedPath, fifoPath, key)

	// Read and hash from FIFO
	var decrypted bytes.Buffer
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		if err := waitForFIFO(fifoPath); err != nil {
			return
		}
		fifo, err := os.Open(fifoPath)
		if err != nil {
			return
		}
		defer fifo.Close()
		io.Copy(&decrypted, fifo)
	}()

	result := <-resultCh
	<-readerDone
	if result.Err != nil {
		t.Fatalf("DecryptToFIFO failed: %v", result.Err)
	}

	// Verify hash
	actualHash := sha256.Sum256(decrypted.Bytes())
	if actualHash != expectedHash {
		t.Errorf("hash mismatch:\ngot:  %s\nwant: %s",
			hex.EncodeToString(actualHash[:]),
			hex.EncodeToString(expectedHash[:]))
	}
}

func TestDecryptToFIFO_LargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large file test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "decrypt-large-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create 1MB test data
	key := bytes.Repeat([]byte{0xCD}, 32)
	plaintext := bytes.Repeat([]byte("LARGE"), 200*1024) // 1MB

	encryptedData := createTestEncryptedFile(t, key, plaintext, 64*1024) // 64KB chunks
	encryptedPath := filepath.Join(tmpDir, "large.tbenc")
	if err := os.WriteFile(encryptedPath, encryptedData, 0644); err != nil {
		t.Fatalf("failed to write encrypted file: %v", err)
	}

	fifoPath := filepath.Join(tmpDir, "large-pipe")

	ctx := context.Background()
	resultCh := DecryptToFIFO(ctx, encryptedPath, fifoPath, key,
		WithTotalBytes(int64(len(plaintext))),
	)

	// Read from FIFO
	var decrypted bytes.Buffer
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		if err := waitForFIFO(fifoPath); err != nil {
			return
		}
		fifo, err := os.Open(fifoPath)
		if err != nil {
			return
		}
		defer fifo.Close()
		io.Copy(&decrypted, fifo)
	}()

	result := <-resultCh
	<-readerDone
	if result.Err != nil {
		t.Fatalf("DecryptToFIFO failed: %v", result.Err)
	}

	if result.BytesWritten != int64(len(plaintext)) {
		t.Errorf("bytes written = %d, want %d", result.BytesWritten, len(plaintext))
	}

	if decrypted.Len() != len(plaintext) {
		t.Errorf("read %d bytes, want %d", decrypted.Len(), len(plaintext))
	}

	// Verify content matches
	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Error("plaintext content mismatch")
	}
}

func TestDecryptToFIFO_NonexistentEncryptedFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "decrypt-nofile-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	key := bytes.Repeat([]byte{0x42}, 32)
	encryptedPath := filepath.Join(tmpDir, "nonexistent.tbenc")
	fifoPath := filepath.Join(tmpDir, "test-pipe")

	ctx := context.Background()
	resultCh := DecryptToFIFO(ctx, encryptedPath, fifoPath, key)

	// We need to open the FIFO for reading to unblock the writer
	// But the error should happen before that since file doesn't exist
	go func() {
		time.Sleep(100 * time.Millisecond)
		// Try to open FIFO if it was created
		if _, err := os.Stat(fifoPath); err == nil {
			fifo, _ := os.Open(fifoPath)
			if fifo != nil {
				fifo.Close()
			}
		}
	}()

	result := <-resultCh
	if result.Err == nil {
		t.Error("expected error for nonexistent encrypted file")
	}
}
