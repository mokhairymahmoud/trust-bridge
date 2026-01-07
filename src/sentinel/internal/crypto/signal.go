// Package crypto implements tbenc/v1 decryption for TrustBridge.
//
// This file provides ready signal file utilities for coordinating
// between the sentinel and runtime processes.
package crypto

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SignalFileMode is the permission mode for the ready signal file.
const SignalFileMode = 0644

// SentinelVersion is the version string included in the signal file.
const SentinelVersion = "0.1.0"

// ReadySignal represents the JSON structure of the ready signal file.
type ReadySignal struct {
	Ready           bool   `json:"ready"`
	Timestamp       string `json:"timestamp"`
	SentinelVersion string `json:"sentinel_version"`
}

// WriteReadySignal creates an atomic ready signal file at the specified path.
//
// The signal file is written atomically by first writing to a temporary file
// and then renaming it to the final path. This prevents the runtime from
// reading a partially written file.
//
// The parent directory is created if it doesn't exist.
func WriteReadySignal(path string) error {
	if path == "" {
		return fmt.Errorf("signal path cannot be empty")
	}

	// Ensure parent directory exists
	if err := EnsureParentDir(path); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Create the signal content
	signal := ReadySignal{
		Ready:           true,
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
		SentinelVersion: SentinelVersion,
	}

	data, err := json.MarshalIndent(signal, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal signal JSON: %w", err)
	}
	data = append(data, '\n')

	// Write atomically: create temp file, write, rename
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".signal-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup on error
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpPath)
		}
	}()

	// Write content
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write signal data: %w", err)
	}

	// Set permissions before rename
	if err := tmpFile.Chmod(SignalFileMode); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename signal file: %w", err)
	}

	cleanup = false // Successful - don't remove
	return nil
}

// RemoveReadySignal removes the ready signal file at the specified path.
// Returns nil if the path doesn't exist (idempotent).
func RemoveReadySignal(path string) error {
	if path == "" {
		return fmt.Errorf("signal path cannot be empty")
	}

	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove signal file at %s: %w", path, err)
	}

	return nil
}

// IsReady checks if a ready signal file exists at the given path.
// Returns false if the file doesn't exist or cannot be read.
func IsReady(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ReadReadySignal reads and parses the ready signal file at the given path.
// Returns nil and an error if the file doesn't exist or cannot be parsed.
func ReadReadySignal(path string) (*ReadySignal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read signal file: %w", err)
	}

	var signal ReadySignal
	if err := json.Unmarshal(data, &signal); err != nil {
		return nil, fmt.Errorf("failed to parse signal JSON: %w", err)
	}

	return &signal, nil
}
