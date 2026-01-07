// fifo-test is a CLI tool for testing tbenc/v1 decryption to a FIFO.
//
// Usage:
//
//	go run ./cmd/fifo-test/main.go <encrypted-file> <key-hex> <fifo-path>
//
// The tool creates a FIFO at the given path, writes decrypted data to it,
// and waits for a reader to consume the data. It also writes a ready signal
// file at <fifo-path>.ready to indicate when decryption has started.
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"trustbridge/sentinel/internal/crypto"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "Usage: %s <encrypted-file> <key-hex> <fifo-path>\n", os.Args[0])
		os.Exit(1)
	}

	encryptedPath := os.Args[1]
	keyHex := os.Args[2]
	fifoPath := os.Args[3]
	signalPath := fifoPath + ".ready"

	// Decode key
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid key hex: %v\n", err)
		os.Exit(1)
	}

	if len(key) != 32 {
		fmt.Fprintf(os.Stderr, "Error: key must be 32 bytes (64 hex chars), got %d bytes\n", len(key))
		os.Exit(1)
	}

	// Verify encrypted file exists
	info, err := os.Stat(encryptedPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: encrypted file not found: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("FIFO Decryption Test\n")
	fmt.Printf("  Encrypted file: %s (%d bytes)\n", encryptedPath, info.Size())
	fmt.Printf("  FIFO path: %s\n", fifoPath)
	fmt.Printf("  Signal file: %s\n", signalPath)
	fmt.Println()

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nReceived interrupt signal, shutting down...")
		cancel()
	}()

	// Create FIFO
	fmt.Println("Creating FIFO...")
	if err := crypto.CreateFIFO(fifoPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create FIFO: %v\n", err)
		os.Exit(1)
	}
	defer crypto.RemoveFIFO(fifoPath)

	// Write ready signal
	fmt.Println("Writing ready signal...")
	if err := crypto.WriteReadySignal(signalPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to write ready signal: %v\n", err)
		os.Exit(1)
	}
	defer crypto.RemoveReadySignal(signalPath)

	fmt.Println("Ready! Waiting for reader to connect to FIFO...")
	fmt.Println("(Start reading from the FIFO in another terminal)")
	fmt.Println()

	// Start decryption (blocking)
	startTime := time.Now()
	bytesWritten, err := crypto.DecryptToFIFOBlocking(ctx, encryptedPath, fifoPath, key)
	elapsed := time.Since(startTime)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: decryption failed: %v\n", err)
		os.Exit(1)
	}

	// Output results
	fmt.Println()
	fmt.Printf("Decryption complete!\n")
	fmt.Printf("  Bytes written: %d\n", bytesWritten)
	fmt.Printf("  Duration: %v\n", elapsed)
	if elapsed.Seconds() > 0 {
		throughput := float64(bytesWritten) / elapsed.Seconds() / 1024 / 1024
		fmt.Printf("  Throughput: %.2f MB/s\n", throughput)
	}
}
