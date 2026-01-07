// decrypt-test is a CLI tool for testing tbenc/v1 decryption.
//
// Usage:
//   go run ./cmd/decrypt-test/main.go <encrypted-file> <key-hex>
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"trustbridge/sentinel/internal/crypto"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <encrypted-file> <key-hex>\n", os.Args[0])
		os.Exit(1)
	}

	encryptedPath := os.Args[1]
	keyHex := os.Args[2]

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

	// Open encrypted file
	f, err := os.Open(encryptedPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to open file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	// Decrypt to bytes
	plaintext, err := crypto.DecryptToBytes(f, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: decryption failed: %v\n", err)
		os.Exit(1)
	}

	// Compute hash
	hash := sha256.Sum256(plaintext)
	hashHex := hex.EncodeToString(hash[:])

	// Output results
	fmt.Printf("Decryption successful!\n")
	fmt.Printf("Plaintext size: %d bytes\n", len(plaintext))
	fmt.Printf("Plaintext SHA256: %s\n", hashHex)
	fmt.Printf("\nFirst 100 bytes of plaintext:\n%s\n", truncate(plaintext, 100))
}

func truncate(b []byte, maxLen int) string {
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + "..."
}
