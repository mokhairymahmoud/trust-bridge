package asset

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// validManifestJSON returns a valid manifest JSON string for testing.
func validManifestJSON() string {
	return `{
		"format": "tbenc/v1",
		"algo": "aes-256-gcm-chunked",
		"chunk_bytes": 4194304,
		"plaintext_bytes": 53821440,
		"sha256_ciphertext": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"asset_id": "tb-asset-123",
		"weights_filename": "model.tbenc"
	}`
}

// validManifest returns a valid Manifest struct for testing.
func validManifest() *Manifest {
	return &Manifest{
		Format:           "tbenc/v1",
		Algo:             "aes-256-gcm-chunked",
		ChunkBytes:       4194304,
		PlaintextBytes:   53821440,
		SHA256Ciphertext: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		AssetID:          "tb-asset-123",
		WeightsFilename:  "model.tbenc",
	}
}

func TestManifest_Validate_Success(t *testing.T) {
	m := validManifest()
	err := m.Validate()
	if err != nil {
		t.Errorf("expected valid manifest to pass validation, got error: %v", err)
	}
}

func TestManifest_Validate_MissingFields(t *testing.T) {
	tests := []struct {
		name      string
		modify    func(*Manifest)
		wantField string
	}{
		{
			name:      "missing format",
			modify:    func(m *Manifest) { m.Format = "" },
			wantField: "format",
		},
		{
			name:      "missing algo",
			modify:    func(m *Manifest) { m.Algo = "" },
			wantField: "algo",
		},
		{
			name:      "missing sha256_ciphertext",
			modify:    func(m *Manifest) { m.SHA256Ciphertext = "" },
			wantField: "sha256_ciphertext",
		},
		{
			name:      "missing asset_id",
			modify:    func(m *Manifest) { m.AssetID = "" },
			wantField: "asset_id",
		},
		{
			name:      "missing weights_filename",
			modify:    func(m *Manifest) { m.WeightsFilename = "" },
			wantField: "weights_filename",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			tt.modify(m)
			err := m.Validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			var validErr *ManifestValidationError
			if !errors.As(err, &validErr) {
				t.Fatalf("expected ManifestValidationError, got %T", err)
			}
			if validErr.Field != tt.wantField {
				t.Errorf("expected field %q, got %q", tt.wantField, validErr.Field)
			}
		})
	}
}

func TestManifest_Validate_InvalidValues(t *testing.T) {
	tests := []struct {
		name      string
		modify    func(*Manifest)
		wantField string
	}{
		{
			name:      "wrong format",
			modify:    func(m *Manifest) { m.Format = "tbenc/v2" },
			wantField: "format",
		},
		{
			name:      "wrong algo",
			modify:    func(m *Manifest) { m.Algo = "aes-128-gcm" },
			wantField: "algo",
		},
		{
			name:      "zero chunk_bytes",
			modify:    func(m *Manifest) { m.ChunkBytes = 0 },
			wantField: "chunk_bytes",
		},
		{
			name:      "negative chunk_bytes",
			modify:    func(m *Manifest) { m.ChunkBytes = -1 },
			wantField: "chunk_bytes",
		},
		{
			name:      "negative plaintext_bytes",
			modify:    func(m *Manifest) { m.PlaintextBytes = -1 },
			wantField: "plaintext_bytes",
		},
		{
			name:      "short sha256",
			modify:    func(m *Manifest) { m.SHA256Ciphertext = "abc123" },
			wantField: "sha256_ciphertext",
		},
		{
			name:      "invalid hex in sha256",
			modify:    func(m *Manifest) { m.SHA256Ciphertext = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" },
			wantField: "sha256_ciphertext",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			tt.modify(m)
			err := m.Validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			var validErr *ManifestValidationError
			if !errors.As(err, &validErr) {
				t.Fatalf("expected ManifestValidationError, got %T", err)
			}
			if validErr.Field != tt.wantField {
				t.Errorf("expected field %q, got %q", tt.wantField, validErr.Field)
			}
		})
	}
}

func TestManifest_Validate_ZeroPlaintextAllowed(t *testing.T) {
	m := validManifest()
	m.PlaintextBytes = 0
	err := m.Validate()
	if err != nil {
		t.Errorf("zero plaintext_bytes should be valid for empty files, got error: %v", err)
	}
}

func TestParseManifest_Success(t *testing.T) {
	r := strings.NewReader(validManifestJSON())
	m, err := ParseManifest(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Format != "tbenc/v1" {
		t.Errorf("expected format 'tbenc/v1', got %q", m.Format)
	}
	if m.Algo != "aes-256-gcm-chunked" {
		t.Errorf("expected algo 'aes-256-gcm-chunked', got %q", m.Algo)
	}
	if m.ChunkBytes != 4194304 {
		t.Errorf("expected chunk_bytes 4194304, got %d", m.ChunkBytes)
	}
	if m.PlaintextBytes != 53821440 {
		t.Errorf("expected plaintext_bytes 53821440, got %d", m.PlaintextBytes)
	}
	if m.AssetID != "tb-asset-123" {
		t.Errorf("expected asset_id 'tb-asset-123', got %q", m.AssetID)
	}
	if m.WeightsFilename != "model.tbenc" {
		t.Errorf("expected weights_filename 'model.tbenc', got %q", m.WeightsFilename)
	}
}

func TestParseManifest_InvalidJSON(t *testing.T) {
	r := strings.NewReader("not valid json")
	_, err := ParseManifest(r)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParseManifest_EmptyJSON(t *testing.T) {
	r := strings.NewReader("{}")
	m, err := ParseManifest(r)
	if err != nil {
		t.Fatalf("unexpected error parsing empty JSON: %v", err)
	}
	// Empty JSON should parse, but validation should fail
	err = m.Validate()
	if err == nil {
		t.Fatal("expected validation error for empty manifest")
	}
}

func TestParseManifest_ExtraFields(t *testing.T) {
	// Manifests with extra fields should still parse
	jsonWithExtra := `{
		"format": "tbenc/v1",
		"algo": "aes-256-gcm-chunked",
		"chunk_bytes": 4194304,
		"plaintext_bytes": 53821440,
		"sha256_ciphertext": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"asset_id": "tb-asset-123",
		"weights_filename": "model.tbenc",
		"extra_field": "should be ignored",
		"another_extra": 12345
	}`
	r := strings.NewReader(jsonWithExtra)
	m, err := ParseManifest(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("expected valid manifest with extra fields, got error: %v", err)
	}
}

func TestDownloadManifest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET request, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(validManifestJSON()))
	}))
	defer server.Close()

	ctx := context.Background()
	m, err := DownloadManifest(ctx, server.URL+"/manifest.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Format != "tbenc/v1" {
		t.Errorf("expected format 'tbenc/v1', got %q", m.Format)
	}
}

func TestDownloadManifest_HTTPError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
		retryable  bool
	}{
		{name: "404 Not Found", statusCode: http.StatusNotFound, wantErr: true, retryable: false},
		{name: "403 Forbidden", statusCode: http.StatusForbidden, wantErr: true, retryable: false},
		{name: "500 Internal Server Error", statusCode: http.StatusInternalServerError, wantErr: true, retryable: true},
		{name: "502 Bad Gateway", statusCode: http.StatusBadGateway, wantErr: true, retryable: true},
		{name: "503 Service Unavailable", statusCode: http.StatusServiceUnavailable, wantErr: true, retryable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte("error response"))
			}))
			defer server.Close()

			ctx := context.Background()
			_, err := DownloadManifest(ctx, server.URL+"/manifest.json")

			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if err != nil {
				var assetErr *AssetError
				if errors.As(err, &assetErr) {
					if assetErr.Retryable != tt.retryable {
						t.Errorf("expected retryable=%v, got %v", tt.retryable, assetErr.Retryable)
					}
				}
			}
		})
	}
}

func TestDownloadManifest_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	ctx := context.Background()
	_, err := DownloadManifest(ctx, server.URL+"/manifest.json")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid, got %v", err)
	}
}

func TestDownloadManifest_ValidationFailure(t *testing.T) {
	// Return valid JSON but with invalid manifest content
	invalidManifest := `{"format": "wrong", "algo": "wrong"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(invalidManifest))
	}))
	defer server.Close()

	ctx := context.Background()
	_, err := DownloadManifest(ctx, server.URL+"/manifest.json")
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid, got %v", err)
	}
}

func TestDownloadManifest_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay response
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(validManifestJSON()))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := DownloadManifest(ctx, server.URL+"/manifest.json")
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestDownloadManifest_NetworkError(t *testing.T) {
	// Use an invalid URL that won't connect
	ctx := context.Background()
	_, err := DownloadManifest(ctx, "http://localhost:1/nonexistent")
	if err == nil {
		t.Fatal("expected network error, got nil")
	}

	var assetErr *AssetError
	if errors.As(err, &assetErr) {
		if !assetErr.Retryable {
			t.Error("network errors should be retryable")
		}
	}
}

func TestDownloadManifestWithClient_CustomClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(validManifestJSON()))
	}))
	defer server.Close()

	customClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	ctx := context.Background()
	m, err := DownloadManifestWithClient(ctx, customClient, server.URL+"/manifest.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Format != "tbenc/v1" {
		t.Errorf("expected format 'tbenc/v1', got %q", m.Format)
	}
}

func TestManifest_CiphertextSize(t *testing.T) {
	tests := []struct {
		name           string
		chunkBytes     int64
		plaintextBytes int64
		wantSize       int64
	}{
		{
			name:           "empty file",
			chunkBytes:     4194304,
			plaintextBytes: 0,
			wantSize:       32, // Just header
		},
		{
			name:           "one byte",
			chunkBytes:     4194304,
			plaintextBytes: 1,
			// Header (32) + one chunk (4 + 1 + 16)
			wantSize: 32 + 21,
		},
		{
			name:           "exactly one chunk",
			chunkBytes:     1024,
			plaintextBytes: 1024,
			// Header (32) + one chunk (4 + 1024 + 16)
			wantSize: 32 + 1044,
		},
		{
			name:           "one chunk plus one byte",
			chunkBytes:     1024,
			plaintextBytes: 1025,
			// Header (32) + full chunk (4 + 1024 + 16) + partial chunk (4 + 1 + 16)
			wantSize: 32 + 1044 + 21,
		},
		{
			name:           "multiple chunks exact",
			chunkBytes:     1024,
			plaintextBytes: 4096, // 4 chunks
			// Header (32) + 4 chunks * (4 + 1024 + 16)
			wantSize: 32 + 4*1044,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manifest{
				ChunkBytes:     tt.chunkBytes,
				PlaintextBytes: tt.plaintextBytes,
			}
			got := m.CiphertextSize()
			if got != tt.wantSize {
				t.Errorf("CiphertextSize() = %d, want %d", got, tt.wantSize)
			}
		})
	}
}

func TestManifestValidationError_Error(t *testing.T) {
	err := &ManifestValidationError{Field: "format", Message: "required but not set"}
	expected := "manifest validation: format: required but not set"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestParseManifest_LargeManifest(t *testing.T) {
	// Create a manifest that exceeds the size limit
	largeData := make([]byte, maxManifestSize+1000)
	for i := range largeData {
		largeData[i] = 'a'
	}

	r := strings.NewReader(string(largeData))
	_, err := ParseManifest(r)
	if err == nil {
		t.Fatal("expected error for oversized manifest, got nil")
	}
}

// BenchmarkParseManifest measures the performance of manifest parsing.
func BenchmarkParseManifest(b *testing.B) {
	jsonData := validManifestJSON()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := strings.NewReader(jsonData)
		_, err := ParseManifest(r)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkManifestValidate measures the performance of manifest validation.
func BenchmarkManifestValidate(b *testing.B) {
	m := validManifest()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Validate()
	}
}

func TestManifest_JSONRoundTrip(t *testing.T) {
	original := validManifest()

	// Marshal to JSON
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Unmarshal back
	var decoded Manifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Compare
	if decoded.Format != original.Format {
		t.Errorf("format mismatch: got %q, want %q", decoded.Format, original.Format)
	}
	if decoded.Algo != original.Algo {
		t.Errorf("algo mismatch: got %q, want %q", decoded.Algo, original.Algo)
	}
	if decoded.ChunkBytes != original.ChunkBytes {
		t.Errorf("chunk_bytes mismatch: got %d, want %d", decoded.ChunkBytes, original.ChunkBytes)
	}
	if decoded.PlaintextBytes != original.PlaintextBytes {
		t.Errorf("plaintext_bytes mismatch: got %d, want %d", decoded.PlaintextBytes, original.PlaintextBytes)
	}
	if decoded.SHA256Ciphertext != original.SHA256Ciphertext {
		t.Errorf("sha256_ciphertext mismatch: got %q, want %q", decoded.SHA256Ciphertext, original.SHA256Ciphertext)
	}
	if decoded.AssetID != original.AssetID {
		t.Errorf("asset_id mismatch: got %q, want %q", decoded.AssetID, original.AssetID)
	}
	if decoded.WeightsFilename != original.WeightsFilename {
		t.Errorf("weights_filename mismatch: got %q, want %q", decoded.WeightsFilename, original.WeightsFilename)
	}
}
