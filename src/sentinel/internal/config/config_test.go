package config

import (
	"os"
	"strings"
	"testing"
)

// setTestEnv sets environment variables for testing and returns a cleanup function.
func setTestEnv(t *testing.T, envVars map[string]string) {
	t.Helper()
	for key, value := range envVars {
		t.Setenv(key, value)
	}
}

// clearConfigEnv removes all TB_ environment variables.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"TB_CONTRACT_ID",
		"TB_ASSET_ID",
		"TB_EDC_ENDPOINT",
		"TB_TARGET_DIR",
		"TB_PIPE_PATH",
		"TB_READY_SIGNAL",
		"TB_RUNTIME_URL",
		"TB_PUBLIC_ADDR",
		"TB_DOWNLOAD_CONCURRENCY",
		"TB_DOWNLOAD_CHUNK_BYTES",
		"TB_LOG_LEVEL",
	}
	for _, key := range envVars {
		os.Unsetenv(key)
	}
}

func TestLoad_AllRequired(t *testing.T) {
	clearConfigEnv(t)
	setTestEnv(t, map[string]string{
		"TB_CONTRACT_ID":  "contract-123",
		"TB_ASSET_ID":     "asset-456",
		"TB_EDC_ENDPOINT": "https://edc.example.com",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.ContractID != "contract-123" {
		t.Errorf("ContractID = %q, want %q", cfg.ContractID, "contract-123")
	}
	if cfg.AssetID != "asset-456" {
		t.Errorf("AssetID = %q, want %q", cfg.AssetID, "asset-456")
	}
	if cfg.EDCEndpoint != "https://edc.example.com" {
		t.Errorf("EDCEndpoint = %q, want %q", cfg.EDCEndpoint, "https://edc.example.com")
	}
}

func TestLoad_MissingContractID(t *testing.T) {
	clearConfigEnv(t)
	setTestEnv(t, map[string]string{
		"TB_ASSET_ID":     "asset-456",
		"TB_EDC_ENDPOINT": "https://edc.example.com",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error for missing TB_CONTRACT_ID")
	}

	if !strings.Contains(err.Error(), "TB_CONTRACT_ID") {
		t.Errorf("error = %v, want error mentioning TB_CONTRACT_ID", err)
	}
}

func TestLoad_MissingAssetID(t *testing.T) {
	clearConfigEnv(t)
	setTestEnv(t, map[string]string{
		"TB_CONTRACT_ID":  "contract-123",
		"TB_EDC_ENDPOINT": "https://edc.example.com",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error for missing TB_ASSET_ID")
	}

	if !strings.Contains(err.Error(), "TB_ASSET_ID") {
		t.Errorf("error = %v, want error mentioning TB_ASSET_ID", err)
	}
}

func TestLoad_MissingEDCEndpoint(t *testing.T) {
	clearConfigEnv(t)
	setTestEnv(t, map[string]string{
		"TB_CONTRACT_ID": "contract-123",
		"TB_ASSET_ID":    "asset-456",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error for missing TB_EDC_ENDPOINT")
	}

	if !strings.Contains(err.Error(), "TB_EDC_ENDPOINT") {
		t.Errorf("error = %v, want error mentioning TB_EDC_ENDPOINT", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearConfigEnv(t)
	setTestEnv(t, map[string]string{
		"TB_CONTRACT_ID":  "contract-123",
		"TB_ASSET_ID":     "asset-456",
		"TB_EDC_ENDPOINT": "https://edc.example.com",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	// Check defaults are applied
	if cfg.TargetDir != DefaultTargetDir {
		t.Errorf("TargetDir = %q, want default %q", cfg.TargetDir, DefaultTargetDir)
	}
	if cfg.PipePath != DefaultPipePath {
		t.Errorf("PipePath = %q, want default %q", cfg.PipePath, DefaultPipePath)
	}
	if cfg.ReadySignal != DefaultReadySignal {
		t.Errorf("ReadySignal = %q, want default %q", cfg.ReadySignal, DefaultReadySignal)
	}
	if cfg.RuntimeURL != DefaultRuntimeURL {
		t.Errorf("RuntimeURL = %q, want default %q", cfg.RuntimeURL, DefaultRuntimeURL)
	}
	if cfg.PublicAddr != DefaultPublicAddr {
		t.Errorf("PublicAddr = %q, want default %q", cfg.PublicAddr, DefaultPublicAddr)
	}
	if cfg.DownloadConcurrency != DefaultDownloadConcurrency {
		t.Errorf("DownloadConcurrency = %d, want default %d", cfg.DownloadConcurrency, DefaultDownloadConcurrency)
	}
	if cfg.DownloadChunkBytes != DefaultDownloadChunkBytes {
		t.Errorf("DownloadChunkBytes = %d, want default %d", cfg.DownloadChunkBytes, DefaultDownloadChunkBytes)
	}
	if cfg.LogLevel != DefaultLogLevel {
		t.Errorf("LogLevel = %q, want default %q", cfg.LogLevel, DefaultLogLevel)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	clearConfigEnv(t)
	setTestEnv(t, map[string]string{
		"TB_CONTRACT_ID":          "contract-123",
		"TB_ASSET_ID":             "asset-456",
		"TB_EDC_ENDPOINT":         "https://edc.example.com",
		"TB_TARGET_DIR":           "/custom/target",
		"TB_PIPE_PATH":            "/custom/pipe",
		"TB_READY_SIGNAL":         "/custom/signal",
		"TB_RUNTIME_URL":          "http://localhost:9000",
		"TB_PUBLIC_ADDR":          "0.0.0.0:9090",
		"TB_DOWNLOAD_CONCURRENCY": "8",
		"TB_DOWNLOAD_CHUNK_BYTES": "16777216",
		"TB_LOG_LEVEL":            "DEBUG",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.TargetDir != "/custom/target" {
		t.Errorf("TargetDir = %q, want %q", cfg.TargetDir, "/custom/target")
	}
	if cfg.PipePath != "/custom/pipe" {
		t.Errorf("PipePath = %q, want %q", cfg.PipePath, "/custom/pipe")
	}
	if cfg.ReadySignal != "/custom/signal" {
		t.Errorf("ReadySignal = %q, want %q", cfg.ReadySignal, "/custom/signal")
	}
	if cfg.RuntimeURL != "http://localhost:9000" {
		t.Errorf("RuntimeURL = %q, want %q", cfg.RuntimeURL, "http://localhost:9000")
	}
	if cfg.PublicAddr != "0.0.0.0:9090" {
		t.Errorf("PublicAddr = %q, want %q", cfg.PublicAddr, "0.0.0.0:9090")
	}
	if cfg.DownloadConcurrency != 8 {
		t.Errorf("DownloadConcurrency = %d, want %d", cfg.DownloadConcurrency, 8)
	}
	if cfg.DownloadChunkBytes != 16777216 {
		t.Errorf("DownloadChunkBytes = %d, want %d", cfg.DownloadChunkBytes, 16777216)
	}
	// LogLevel should be lowercased
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestLoad_InvalidConcurrency(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"zero", "0"},
		{"negative", "-1"},
		{"too_high", "100"},
		{"not_integer", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			setTestEnv(t, map[string]string{
				"TB_CONTRACT_ID":          "contract-123",
				"TB_ASSET_ID":             "asset-456",
				"TB_EDC_ENDPOINT":         "https://edc.example.com",
				"TB_DOWNLOAD_CONCURRENCY": tt.value,
			})

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() error = nil, want error for invalid concurrency %q", tt.value)
			}

			if !strings.Contains(err.Error(), "TB_DOWNLOAD_CONCURRENCY") {
				t.Errorf("error = %v, want error mentioning TB_DOWNLOAD_CONCURRENCY", err)
			}
		})
	}
}

func TestLoad_InvalidChunkBytes(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"zero", "0"},
		{"too_small", "100"},
		{"too_large", "100000000"},
		{"not_integer", "xyz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			setTestEnv(t, map[string]string{
				"TB_CONTRACT_ID":          "contract-123",
				"TB_ASSET_ID":             "asset-456",
				"TB_EDC_ENDPOINT":         "https://edc.example.com",
				"TB_DOWNLOAD_CHUNK_BYTES": tt.value,
			})

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() error = nil, want error for invalid chunk bytes %q", tt.value)
			}

			if !strings.Contains(err.Error(), "TB_DOWNLOAD_CHUNK_BYTES") {
				t.Errorf("error = %v, want error mentioning TB_DOWNLOAD_CHUNK_BYTES", err)
			}
		})
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	clearConfigEnv(t)
	setTestEnv(t, map[string]string{
		"TB_CONTRACT_ID":  "contract-123",
		"TB_ASSET_ID":     "asset-456",
		"TB_EDC_ENDPOINT": "https://edc.example.com",
		"TB_LOG_LEVEL":    "invalid",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error for invalid log level")
	}

	if !strings.Contains(err.Error(), "TB_LOG_LEVEL") {
		t.Errorf("error = %v, want error mentioning TB_LOG_LEVEL", err)
	}
}

func TestLoad_InvalidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"no_scheme", "edc.example.com"},
		{"invalid_scheme", "ftp://edc.example.com"},
		{"no_host", "http://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			setTestEnv(t, map[string]string{
				"TB_CONTRACT_ID":  "contract-123",
				"TB_ASSET_ID":     "asset-456",
				"TB_EDC_ENDPOINT": tt.url,
			})

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() error = nil, want error for invalid URL %q", tt.url)
			}

			if !strings.Contains(err.Error(), "TB_EDC_ENDPOINT") {
				t.Errorf("error = %v, want error mentioning TB_EDC_ENDPOINT", err)
			}
		})
	}
}

func TestLoad_InvalidPath(t *testing.T) {
	tests := []struct {
		name     string
		envVar   string
		value    string
		errorKey string
	}{
		{"relative_target_dir", "TB_TARGET_DIR", "relative/path", "TB_TARGET_DIR"},
		{"relative_pipe_path", "TB_PIPE_PATH", "relative/pipe", "TB_PIPE_PATH"},
		{"relative_ready_signal", "TB_READY_SIGNAL", "relative/signal", "TB_READY_SIGNAL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			setTestEnv(t, map[string]string{
				"TB_CONTRACT_ID":  "contract-123",
				"TB_ASSET_ID":     "asset-456",
				"TB_EDC_ENDPOINT": "https://edc.example.com",
				tt.envVar:         tt.value,
			})

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() error = nil, want error for relative path %q", tt.value)
			}

			if !strings.Contains(err.Error(), tt.errorKey) {
				t.Errorf("error = %v, want error mentioning %s", err, tt.errorKey)
			}
		})
	}
}

func TestLoad_MultipleErrors(t *testing.T) {
	clearConfigEnv(t)
	// Set no required fields - should get multiple errors
	setTestEnv(t, map[string]string{
		"TB_DOWNLOAD_CONCURRENCY": "abc",
		"TB_LOG_LEVEL":            "invalid",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want multiple errors")
	}

	errStr := err.Error()
	// Should mention all missing required fields
	if !strings.Contains(errStr, "TB_CONTRACT_ID") {
		t.Errorf("error should mention TB_CONTRACT_ID, got: %v", errStr)
	}
	if !strings.Contains(errStr, "TB_ASSET_ID") {
		t.Errorf("error should mention TB_ASSET_ID, got: %v", errStr)
	}
	if !strings.Contains(errStr, "TB_EDC_ENDPOINT") {
		t.Errorf("error should mention TB_EDC_ENDPOINT, got: %v", errStr)
	}
}

func TestValidate_AllValid(t *testing.T) {
	cfg := &Config{
		ContractID:          "contract-123",
		AssetID:             "asset-456",
		EDCEndpoint:         "https://edc.example.com",
		TargetDir:           "/mnt/resource",
		PipePath:            "/dev/shm/pipe",
		ReadySignal:         "/dev/shm/ready",
		RuntimeURL:          "http://localhost:8081",
		PublicAddr:          "0.0.0.0:8000",
		DownloadConcurrency: 4,
		DownloadChunkBytes:  4194304,
		LogLevel:            "info",
	}

	errs := cfg.Validate()
	if len(errs) > 0 {
		t.Errorf("Validate() returned errors for valid config: %v", errs)
	}
}

func TestConfig_String(t *testing.T) {
	cfg := &Config{
		ContractID:          "contract-123",
		AssetID:             "asset-456",
		EDCEndpoint:         "https://edc.example.com",
		TargetDir:           "/mnt/resource",
		PipePath:            "/dev/shm/pipe",
		ReadySignal:         "/dev/shm/ready",
		RuntimeURL:          "http://localhost:8081",
		PublicAddr:          "0.0.0.0:8000",
		DownloadConcurrency: 4,
		DownloadChunkBytes:  4194304,
		LogLevel:            "info",
	}

	str := cfg.String()

	// Verify all fields are included
	if !strings.Contains(str, "contract-123") {
		t.Error("String() should contain ContractID")
	}
	if !strings.Contains(str, "asset-456") {
		t.Error("String() should contain AssetID")
	}
	if !strings.Contains(str, "https://edc.example.com") {
		t.Error("String() should contain EDCEndpoint")
	}
}

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{
		Field:   "TB_TEST_FIELD",
		Message: "test error message",
	}

	expected := "config: TB_TEST_FIELD: test error message"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}

func TestConfigErrors_Error(t *testing.T) {
	tests := []struct {
		name     string
		errs     ConfigErrors
		contains []string
	}{
		{
			name:     "no_errors",
			errs:     ConfigErrors{},
			contains: []string{"no errors"},
		},
		{
			name: "single_error",
			errs: ConfigErrors{
				&ValidationError{Field: "FIELD1", Message: "error1"},
			},
			contains: []string{"FIELD1", "error1"},
		},
		{
			name: "multiple_errors",
			errs: ConfigErrors{
				&ValidationError{Field: "FIELD1", Message: "error1"},
				&ValidationError{Field: "FIELD2", Message: "error2"},
			},
			contains: []string{"2 configuration errors", "FIELD1", "FIELD2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errStr := tt.errs.Error()
			for _, substr := range tt.contains {
				if !strings.Contains(errStr, substr) {
					t.Errorf("Error() = %q, want to contain %q", errStr, substr)
				}
			}
		})
	}
}

func TestLoad_HTTPEndpoint(t *testing.T) {
	// HTTP endpoints should be allowed (for testing/development)
	clearConfigEnv(t)
	setTestEnv(t, map[string]string{
		"TB_CONTRACT_ID":  "contract-123",
		"TB_ASSET_ID":     "asset-456",
		"TB_EDC_ENDPOINT": "http://localhost:8080",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil (HTTP should be allowed)", err)
	}

	if cfg.EDCEndpoint != "http://localhost:8080" {
		t.Errorf("EDCEndpoint = %q, want %q", cfg.EDCEndpoint, "http://localhost:8080")
	}
}

func TestLoad_BoundaryValues(t *testing.T) {
	tests := []struct {
		name        string
		concurrency string
		chunkBytes  string
		wantErr     bool
	}{
		{"min_concurrency", "1", "8388608", false},
		{"max_concurrency", "32", "8388608", false},
		{"min_chunk_bytes", "4", "1024", false},
		{"max_chunk_bytes", "4", "67108864", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			setTestEnv(t, map[string]string{
				"TB_CONTRACT_ID":          "contract-123",
				"TB_ASSET_ID":             "asset-456",
				"TB_EDC_ENDPOINT":         "https://edc.example.com",
				"TB_DOWNLOAD_CONCURRENCY": tt.concurrency,
				"TB_DOWNLOAD_CHUNK_BYTES": tt.chunkBytes,
			})

			_, err := Load()
			if tt.wantErr && err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
		})
	}
}
