// Package crypto implements tbenc/v1 decryption for TrustBridge.
//
// This file provides FIFO (named pipe) creation and management utilities
// for streaming decrypted model weights to the runtime.
package crypto

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// FIFOMode is the default permission mode for created FIFOs.
// 0600 = owner read/write only (security requirement).
const FIFOMode = 0600

// CreateFIFO creates a named pipe (FIFO) at the specified path.
//
// If a file already exists at the path:
//   - If it's a FIFO, return nil (idempotent)
//   - If it's any other file type, remove it and create a new FIFO
//
// The parent directory is created if it doesn't exist.
func CreateFIFO(path string) error {
	if path == "" {
		return fmt.Errorf("fifo path cannot be empty")
	}

	// Ensure parent directory exists
	if err := EnsureParentDir(path); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Check if something already exists at the path
	info, err := os.Lstat(path)
	if err == nil {
		// File exists - check if it's already a FIFO
		if info.Mode()&os.ModeNamedPipe != 0 {
			// Already a FIFO, idempotent success
			return nil
		}
		// Not a FIFO - remove it so we can create one
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to remove existing file at %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		// Some other error (permissions, etc.)
		return fmt.Errorf("failed to stat path %s: %w", path, err)
	}

	// Create the FIFO
	if err := syscall.Mkfifo(path, FIFOMode); err != nil {
		return fmt.Errorf("failed to create FIFO at %s: %w", path, err)
	}

	return nil
}

// RemoveFIFO removes a FIFO at the specified path.
// Returns nil if the path doesn't exist (idempotent).
func RemoveFIFO(path string) error {
	if path == "" {
		return fmt.Errorf("fifo path cannot be empty")
	}

	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove FIFO at %s: %w", path, err)
	}

	return nil
}

// EnsureParentDir creates the parent directory of the given path if it doesn't exist.
// Uses 0755 permissions for created directories.
func EnsureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		// No parent directory to create
		return nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	return nil
}

// IsFIFO checks if the file at the given path is a named pipe (FIFO).
// Returns false if the path doesn't exist or is not a FIFO.
func IsFIFO(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeNamedPipe != 0
}
