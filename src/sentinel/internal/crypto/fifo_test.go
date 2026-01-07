package crypto

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateFIFO(t *testing.T) {
	// Create a temp directory for tests
	tmpDir, err := os.MkdirTemp("", "fifo-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("creates FIFO successfully", func(t *testing.T) {
		path := filepath.Join(tmpDir, "test-fifo")
		defer os.Remove(path)

		err := CreateFIFO(path)
		if err != nil {
			t.Fatalf("CreateFIFO failed: %v", err)
		}

		// Verify it's a FIFO
		if !IsFIFO(path) {
			t.Error("created file is not a FIFO")
		}

		// Verify permissions
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("failed to stat FIFO: %v", err)
		}
		// Check that it's mode 0600 (ignoring the file type bits)
		perm := info.Mode().Perm()
		if perm != FIFOMode {
			t.Errorf("FIFO permissions = %o, want %o", perm, FIFOMode)
		}
	})

	t.Run("idempotent when FIFO exists", func(t *testing.T) {
		path := filepath.Join(tmpDir, "test-fifo-idempotent")
		defer os.Remove(path)

		// Create FIFO first time
		err := CreateFIFO(path)
		if err != nil {
			t.Fatalf("first CreateFIFO failed: %v", err)
		}

		// Create again - should succeed
		err = CreateFIFO(path)
		if err != nil {
			t.Fatalf("second CreateFIFO failed: %v", err)
		}

		if !IsFIFO(path) {
			t.Error("file is not a FIFO after idempotent creation")
		}
	})

	t.Run("replaces regular file with FIFO", func(t *testing.T) {
		path := filepath.Join(tmpDir, "test-replace-file")
		defer os.Remove(path)

		// Create a regular file first
		if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create regular file: %v", err)
		}

		// CreateFIFO should replace it
		err := CreateFIFO(path)
		if err != nil {
			t.Fatalf("CreateFIFO failed: %v", err)
		}

		if !IsFIFO(path) {
			t.Error("file was not replaced with FIFO")
		}
	})

	t.Run("creates parent directories", func(t *testing.T) {
		path := filepath.Join(tmpDir, "nested", "dirs", "test-fifo")
		defer os.RemoveAll(filepath.Join(tmpDir, "nested"))

		err := CreateFIFO(path)
		if err != nil {
			t.Fatalf("CreateFIFO with nested dirs failed: %v", err)
		}

		if !IsFIFO(path) {
			t.Error("created file is not a FIFO")
		}
	})

	t.Run("fails with empty path", func(t *testing.T) {
		err := CreateFIFO("")
		if err == nil {
			t.Error("expected error for empty path, got nil")
		}
	})
}

func TestRemoveFIFO(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fifo-remove-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("removes existing FIFO", func(t *testing.T) {
		path := filepath.Join(tmpDir, "test-fifo-remove")

		// Create FIFO
		if err := CreateFIFO(path); err != nil {
			t.Fatalf("CreateFIFO failed: %v", err)
		}

		// Remove it
		err := RemoveFIFO(path)
		if err != nil {
			t.Fatalf("RemoveFIFO failed: %v", err)
		}

		// Verify it's gone
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("FIFO still exists after removal")
		}
	})

	t.Run("idempotent when not exists", func(t *testing.T) {
		path := filepath.Join(tmpDir, "nonexistent-fifo")

		err := RemoveFIFO(path)
		if err != nil {
			t.Errorf("RemoveFIFO on nonexistent path failed: %v", err)
		}
	})

	t.Run("fails with empty path", func(t *testing.T) {
		err := RemoveFIFO("")
		if err == nil {
			t.Error("expected error for empty path, got nil")
		}
	})
}

func TestEnsureParentDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ensure-parent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("creates nested directories", func(t *testing.T) {
		path := filepath.Join(tmpDir, "a", "b", "c", "file.txt")

		err := EnsureParentDir(path)
		if err != nil {
			t.Fatalf("EnsureParentDir failed: %v", err)
		}

		// Check parent dir exists
		parentDir := filepath.Dir(path)
		info, err := os.Stat(parentDir)
		if err != nil {
			t.Fatalf("parent dir doesn't exist: %v", err)
		}
		if !info.IsDir() {
			t.Error("parent is not a directory")
		}
	})

	t.Run("succeeds when directory exists", func(t *testing.T) {
		path := filepath.Join(tmpDir, "file.txt")

		err := EnsureParentDir(path)
		if err != nil {
			t.Errorf("EnsureParentDir failed for existing dir: %v", err)
		}
	})

	t.Run("handles empty path", func(t *testing.T) {
		err := EnsureParentDir("")
		if err != nil {
			t.Errorf("EnsureParentDir failed for empty path: %v", err)
		}
	})
}

func TestIsFIFO(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "isfifo-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("returns true for FIFO", func(t *testing.T) {
		path := filepath.Join(tmpDir, "test-fifo")
		if err := CreateFIFO(path); err != nil {
			t.Fatalf("CreateFIFO failed: %v", err)
		}
		defer os.Remove(path)

		if !IsFIFO(path) {
			t.Error("IsFIFO returned false for a FIFO")
		}
	})

	t.Run("returns false for regular file", func(t *testing.T) {
		path := filepath.Join(tmpDir, "regular-file")
		if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
		defer os.Remove(path)

		if IsFIFO(path) {
			t.Error("IsFIFO returned true for a regular file")
		}
	})

	t.Run("returns false for directory", func(t *testing.T) {
		path := filepath.Join(tmpDir, "test-dir")
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatalf("failed to create dir: %v", err)
		}
		defer os.Remove(path)

		if IsFIFO(path) {
			t.Error("IsFIFO returned true for a directory")
		}
	})

	t.Run("returns false for nonexistent path", func(t *testing.T) {
		if IsFIFO("/nonexistent/path") {
			t.Error("IsFIFO returned true for nonexistent path")
		}
	})
}
