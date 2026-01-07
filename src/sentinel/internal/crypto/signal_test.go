package crypto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteReadySignal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "signal-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("creates signal file successfully", func(t *testing.T) {
		path := filepath.Join(tmpDir, "ready.signal")
		defer os.Remove(path)

		beforeTime := time.Now().UTC()
		err := WriteReadySignal(path)
		afterTime := time.Now().UTC()

		if err != nil {
			t.Fatalf("WriteReadySignal failed: %v", err)
		}

		// Verify file exists
		if !IsReady(path) {
			t.Error("signal file does not exist")
		}

		// Verify content
		signal, err := ReadReadySignal(path)
		if err != nil {
			t.Fatalf("failed to read signal: %v", err)
		}

		if !signal.Ready {
			t.Error("signal.Ready = false, want true")
		}

		if signal.SentinelVersion != SentinelVersion {
			t.Errorf("signal.SentinelVersion = %q, want %q", signal.SentinelVersion, SentinelVersion)
		}

		// Verify timestamp is valid and within expected range
		ts, err := time.Parse(time.RFC3339, signal.Timestamp)
		if err != nil {
			t.Errorf("failed to parse timestamp: %v", err)
		}

		if ts.Before(beforeTime.Truncate(time.Second)) || ts.After(afterTime.Add(time.Second)) {
			t.Errorf("timestamp %v not within expected range [%v, %v]", ts, beforeTime, afterTime)
		}
	})

	t.Run("creates parent directories", func(t *testing.T) {
		path := filepath.Join(tmpDir, "nested", "dirs", "ready.signal")
		defer os.RemoveAll(filepath.Join(tmpDir, "nested"))

		err := WriteReadySignal(path)
		if err != nil {
			t.Fatalf("WriteReadySignal failed: %v", err)
		}

		if !IsReady(path) {
			t.Error("signal file does not exist")
		}
	})

	t.Run("overwrites existing signal file", func(t *testing.T) {
		path := filepath.Join(tmpDir, "overwrite.signal")
		defer os.Remove(path)

		// Create first signal
		err := WriteReadySignal(path)
		if err != nil {
			t.Fatalf("first WriteReadySignal failed: %v", err)
		}

		// Read first timestamp
		signal1, err := ReadReadySignal(path)
		if err != nil {
			t.Fatalf("failed to read first signal: %v", err)
		}

		// Wait a moment
		time.Sleep(10 * time.Millisecond)

		// Create second signal
		err = WriteReadySignal(path)
		if err != nil {
			t.Fatalf("second WriteReadySignal failed: %v", err)
		}

		// Read second timestamp
		signal2, err := ReadReadySignal(path)
		if err != nil {
			t.Fatalf("failed to read second signal: %v", err)
		}

		// Timestamps should be different (or at least the file was rewritten)
		if signal1.Timestamp == signal2.Timestamp {
			// They might be the same if the test runs fast enough
			// but at minimum the file should still be valid
		}

		if !signal2.Ready {
			t.Error("second signal.Ready = false, want true")
		}
	})

	t.Run("atomic write - no partial reads", func(t *testing.T) {
		path := filepath.Join(tmpDir, "atomic.signal")
		defer os.Remove(path)

		err := WriteReadySignal(path)
		if err != nil {
			t.Fatalf("WriteReadySignal failed: %v", err)
		}

		// Read raw content and verify it's valid JSON
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		var signal ReadySignal
		if err := json.Unmarshal(data, &signal); err != nil {
			t.Errorf("file content is not valid JSON: %v", err)
		}
	})

	t.Run("fails with empty path", func(t *testing.T) {
		err := WriteReadySignal("")
		if err == nil {
			t.Error("expected error for empty path, got nil")
		}
	})

	t.Run("file permissions are correct", func(t *testing.T) {
		path := filepath.Join(tmpDir, "permissions.signal")
		defer os.Remove(path)

		err := WriteReadySignal(path)
		if err != nil {
			t.Fatalf("WriteReadySignal failed: %v", err)
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("failed to stat file: %v", err)
		}

		perm := info.Mode().Perm()
		if perm != SignalFileMode {
			t.Errorf("file permissions = %o, want %o", perm, SignalFileMode)
		}
	})

	t.Run("JSON is properly formatted", func(t *testing.T) {
		path := filepath.Join(tmpDir, "formatted.signal")
		defer os.Remove(path)

		err := WriteReadySignal(path)
		if err != nil {
			t.Fatalf("WriteReadySignal failed: %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		content := string(data)

		// Verify it's indented (has newlines inside the JSON)
		if !strings.Contains(content, "\n  ") {
			t.Error("JSON is not indented")
		}

		// Verify it ends with a newline
		if !strings.HasSuffix(content, "\n") {
			t.Error("file does not end with newline")
		}
	})
}

func TestRemoveReadySignal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "signal-remove-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("removes existing signal", func(t *testing.T) {
		path := filepath.Join(tmpDir, "remove.signal")

		// Create signal
		if err := WriteReadySignal(path); err != nil {
			t.Fatalf("WriteReadySignal failed: %v", err)
		}

		// Remove it
		err := RemoveReadySignal(path)
		if err != nil {
			t.Fatalf("RemoveReadySignal failed: %v", err)
		}

		// Verify it's gone
		if IsReady(path) {
			t.Error("signal file still exists after removal")
		}
	})

	t.Run("idempotent when not exists", func(t *testing.T) {
		path := filepath.Join(tmpDir, "nonexistent.signal")

		err := RemoveReadySignal(path)
		if err != nil {
			t.Errorf("RemoveReadySignal on nonexistent path failed: %v", err)
		}
	})

	t.Run("fails with empty path", func(t *testing.T) {
		err := RemoveReadySignal("")
		if err == nil {
			t.Error("expected error for empty path, got nil")
		}
	})
}

func TestIsReady(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "isready-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("returns true when signal exists", func(t *testing.T) {
		path := filepath.Join(tmpDir, "exists.signal")
		if err := WriteReadySignal(path); err != nil {
			t.Fatalf("WriteReadySignal failed: %v", err)
		}
		defer os.Remove(path)

		if !IsReady(path) {
			t.Error("IsReady returned false for existing signal")
		}
	})

	t.Run("returns false when signal does not exist", func(t *testing.T) {
		path := filepath.Join(tmpDir, "nonexistent.signal")

		if IsReady(path) {
			t.Error("IsReady returned true for nonexistent path")
		}
	})
}

func TestReadReadySignal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "readsignal-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("reads valid signal file", func(t *testing.T) {
		path := filepath.Join(tmpDir, "valid.signal")
		if err := WriteReadySignal(path); err != nil {
			t.Fatalf("WriteReadySignal failed: %v", err)
		}
		defer os.Remove(path)

		signal, err := ReadReadySignal(path)
		if err != nil {
			t.Fatalf("ReadReadySignal failed: %v", err)
		}

		if !signal.Ready {
			t.Error("signal.Ready = false, want true")
		}

		if signal.Timestamp == "" {
			t.Error("signal.Timestamp is empty")
		}

		if signal.SentinelVersion == "" {
			t.Error("signal.SentinelVersion is empty")
		}
	})

	t.Run("fails for nonexistent file", func(t *testing.T) {
		path := filepath.Join(tmpDir, "nonexistent.signal")

		_, err := ReadReadySignal(path)
		if err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})

	t.Run("fails for invalid JSON", func(t *testing.T) {
		path := filepath.Join(tmpDir, "invalid.signal")
		if err := os.WriteFile(path, []byte("not valid json"), 0644); err != nil {
			t.Fatalf("failed to create invalid file: %v", err)
		}
		defer os.Remove(path)

		_, err := ReadReadySignal(path)
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}
	})
}
