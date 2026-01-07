package license

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateHardwareID_DMI_Success(t *testing.T) {
	// Create a temp file to simulate DMI UUID
	tmpDir := t.TempDir()
	dmiPath := filepath.Join(tmpDir, "product_uuid")
	testUUID := "12345678-1234-1234-1234-123456789ABC"
	if err := os.WriteFile(dmiPath, []byte(testUUID+"\n"), 0644); err != nil {
		t.Fatalf("failed to write test DMI file: %v", err)
	}

	g := NewFingerprintGeneratorWithOptions(dmiPath, "", nil)
	fp, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v, want nil", err)
	}

	if fp.Source != SourceDMI {
		t.Errorf("Source = %q, want %q", fp.Source, SourceDMI)
	}

	// UUID should be lowercased
	expectedID := strings.ToLower(testUUID)
	if fp.ID != expectedID {
		t.Errorf("ID = %q, want %q", fp.ID, expectedID)
	}
}

func TestGenerateHardwareID_DMI_NotFound_FallsBackToIMDS(t *testing.T) {
	// Set up mock IMDS server
	mockIMDS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check required header
		if r.Header.Get("Metadata") != "true" {
			t.Errorf("IMDS request missing Metadata header")
			http.Error(w, "missing header", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"compute":{"vmId":"test-vm-12345","name":"testvm"}}`))
	}))
	defer mockIMDS.Close()

	// Use non-existent DMI path
	g := NewFingerprintGeneratorWithOptions("/nonexistent/path", mockIMDS.URL, mockIMDS.Client())
	fp, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v, want nil", err)
	}

	if fp.Source != SourceIMDS {
		t.Errorf("Source = %q, want %q", fp.Source, SourceIMDS)
	}

	// Should contain the vmId
	if !strings.Contains(fp.ID, "test-vm-12345") {
		t.Errorf("ID = %q, should contain vmId 'test-vm-12345'", fp.ID)
	}
}

func TestGenerateHardwareID_IMDS_Success(t *testing.T) {
	// Set up mock IMDS server
	mockIMDS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request
		if r.Header.Get("Metadata") != "true" {
			t.Errorf("IMDS request missing Metadata header")
			http.Error(w, "missing header", http.StatusBadRequest)
			return
		}

		if !strings.Contains(r.URL.String(), "api-version=") {
			t.Errorf("IMDS request missing api-version")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"compute":{"vmId":"azure-vm-abcdef","name":"myvm"}}`))
	}))
	defer mockIMDS.Close()

	// Use non-existent DMI path to force IMDS fallback
	g := NewFingerprintGeneratorWithOptions("/nonexistent/dmi", mockIMDS.URL, mockIMDS.Client())
	fp, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v, want nil", err)
	}

	if fp.Source != SourceIMDS {
		t.Errorf("Source = %q, want %q", fp.Source, SourceIMDS)
	}

	if !strings.Contains(fp.ID, "azure-vm-abcdef") {
		t.Errorf("ID = %q, should contain vmId", fp.ID)
	}
}

func TestGenerateHardwareID_IMDS_Timeout_FallsBackToHostname(t *testing.T) {
	// Set up mock IMDS server that times out
	mockIMDS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the client timeout
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer mockIMDS.Close()

	// Create a client with a very short timeout
	client := &http.Client{Timeout: 100 * time.Millisecond}

	g := NewFingerprintGeneratorWithOptions("/nonexistent/dmi", mockIMDS.URL, client)
	fp, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v, want nil (should fall back to hostname)", err)
	}

	if fp.Source != SourceHostname {
		t.Errorf("Source = %q, want %q", fp.Source, SourceHostname)
	}

	// Should be a 32-character hex hash
	if len(fp.ID) != 32 {
		t.Errorf("ID length = %d, want 32 (hex hash)", len(fp.ID))
	}
}

func TestGenerateHardwareID_IMDS_InvalidJSON_FallsBackToHostname(t *testing.T) {
	// Set up mock IMDS server that returns invalid JSON
	mockIMDS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not valid json`))
	}))
	defer mockIMDS.Close()

	g := NewFingerprintGeneratorWithOptions("/nonexistent/dmi", mockIMDS.URL, mockIMDS.Client())
	fp, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v, want nil (should fall back to hostname)", err)
	}

	if fp.Source != SourceHostname {
		t.Errorf("Source = %q, want %q", fp.Source, SourceHostname)
	}
}

func TestGenerateHardwareID_IMDS_MissingVMID_FallsBackToHostname(t *testing.T) {
	// Set up mock IMDS server that returns empty vmId
	mockIMDS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"compute":{"vmId":"","name":"myvm"}}`))
	}))
	defer mockIMDS.Close()

	g := NewFingerprintGeneratorWithOptions("/nonexistent/dmi", mockIMDS.URL, mockIMDS.Client())
	fp, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v, want nil (should fall back to hostname)", err)
	}

	if fp.Source != SourceHostname {
		t.Errorf("Source = %q, want %q", fp.Source, SourceHostname)
	}
}

func TestGenerateHardwareID_IMDS_ServerError_FallsBackToHostname(t *testing.T) {
	// Set up mock IMDS server that returns 500
	mockIMDS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockIMDS.Close()

	g := NewFingerprintGeneratorWithOptions("/nonexistent/dmi", mockIMDS.URL, mockIMDS.Client())
	fp, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v, want nil (should fall back to hostname)", err)
	}

	if fp.Source != SourceHostname {
		t.Errorf("Source = %q, want %q", fp.Source, SourceHostname)
	}
}

func TestGenerateHardwareID_Hostname_Success(t *testing.T) {
	// Create generator with both DMI and IMDS unavailable
	mockIMDS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockIMDS.Close()

	g := NewFingerprintGeneratorWithOptions("/nonexistent/dmi", mockIMDS.URL, mockIMDS.Client())
	fp, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v, want nil", err)
	}

	if fp.Source != SourceHostname {
		t.Errorf("Source = %q, want %q", fp.Source, SourceHostname)
	}

	// Should be a valid hex string
	for _, c := range fp.ID {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("ID contains invalid hex character: %c", c)
			break
		}
	}
}

func TestGenerateHardwareID_Consistency(t *testing.T) {
	// Test that the same inputs produce the same outputs
	tmpDir := t.TempDir()
	dmiPath := filepath.Join(tmpDir, "product_uuid")
	testUUID := "CONSISTENT-UUID-1234-5678"
	if err := os.WriteFile(dmiPath, []byte(testUUID), 0644); err != nil {
		t.Fatalf("failed to write test DMI file: %v", err)
	}

	g := NewFingerprintGeneratorWithOptions(dmiPath, "", nil)

	fp1, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() first call error = %v", err)
	}

	fp2, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() second call error = %v", err)
	}

	if fp1.ID != fp2.ID {
		t.Errorf("Inconsistent IDs: %q != %q", fp1.ID, fp2.ID)
	}

	if fp1.Source != fp2.Source {
		t.Errorf("Inconsistent sources: %q != %q", fp1.Source, fp2.Source)
	}
}

func TestGenerateHardwareID_DifferentInputs(t *testing.T) {
	// Test that different inputs produce different outputs
	tmpDir := t.TempDir()

	dmiPath1 := filepath.Join(tmpDir, "uuid1")
	dmiPath2 := filepath.Join(tmpDir, "uuid2")

	if err := os.WriteFile(dmiPath1, []byte("UUID-1111"), 0644); err != nil {
		t.Fatalf("failed to write test DMI file 1: %v", err)
	}
	if err := os.WriteFile(dmiPath2, []byte("UUID-2222"), 0644); err != nil {
		t.Fatalf("failed to write test DMI file 2: %v", err)
	}

	g1 := NewFingerprintGeneratorWithOptions(dmiPath1, "", nil)
	g2 := NewFingerprintGeneratorWithOptions(dmiPath2, "", nil)

	fp1, err := g1.Generate()
	if err != nil {
		t.Fatalf("Generate() g1 error = %v", err)
	}

	fp2, err := g2.Generate()
	if err != nil {
		t.Fatalf("Generate() g2 error = %v", err)
	}

	if fp1.ID == fp2.ID {
		t.Errorf("Different inputs should produce different IDs: both got %q", fp1.ID)
	}
}

func TestGenerateHardwareID_EmptyDMI_FallsBackToIMDS(t *testing.T) {
	// Create empty DMI file
	tmpDir := t.TempDir()
	dmiPath := filepath.Join(tmpDir, "product_uuid")
	if err := os.WriteFile(dmiPath, []byte("  \n  "), 0644); err != nil {
		t.Fatalf("failed to write test DMI file: %v", err)
	}

	// Set up mock IMDS server
	mockIMDS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"compute":{"vmId":"fallback-vm-id"}}`))
	}))
	defer mockIMDS.Close()

	g := NewFingerprintGeneratorWithOptions(dmiPath, mockIMDS.URL, mockIMDS.Client())
	fp, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v, want nil", err)
	}

	if fp.Source != SourceIMDS {
		t.Errorf("Source = %q, want %q (empty DMI should fall back)", fp.Source, SourceIMDS)
	}
}

func TestGenerateHardwareID_ConvenienceFunction(t *testing.T) {
	// Test the convenience function GenerateHardwareID
	// This will use real system sources, so we just verify it returns something
	id, err := GenerateHardwareID()

	// On most systems, at least hostname fallback should work
	if err != nil {
		t.Logf("GenerateHardwareID() returned error (may be expected in some environments): %v", err)
		// Don't fail - some CI environments might not have any valid sources
		return
	}

	if id == "" {
		t.Error("GenerateHardwareID() returned empty ID")
	}

	// Verify it's a reasonable format (non-empty string)
	if len(id) < 8 {
		t.Errorf("GenerateHardwareID() returned suspiciously short ID: %q", id)
	}
}

func TestGenerateHardwareFingerprintWithSource(t *testing.T) {
	// Test the convenience function that returns both ID and source
	fp, err := GenerateHardwareFingerprintWithSource()

	if err != nil {
		t.Logf("GenerateHardwareFingerprintWithSource() returned error (may be expected): %v", err)
		return
	}

	if fp == nil {
		t.Fatal("GenerateHardwareFingerprintWithSource() returned nil fingerprint")
	}

	if fp.ID == "" {
		t.Error("Fingerprint ID is empty")
	}

	// Verify source is one of the valid values
	switch fp.Source {
	case SourceDMI, SourceIMDS, SourceHostname:
		// Valid
	default:
		t.Errorf("Invalid source: %q", fp.Source)
	}
}

func TestHashIdentifier(t *testing.T) {
	// Test the hash function
	tests := []struct {
		name   string
		parts  []string
		length int
	}{
		{"single_part", []string{"hello"}, 32},
		{"multiple_parts", []string{"hello", "world"}, 32},
		{"empty_part", []string{""}, 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hashIdentifier(tt.parts...)
			if len(result) != tt.length {
				t.Errorf("hashIdentifier() length = %d, want %d", len(result), tt.length)
			}

			// Verify it's valid hex
			for _, c := range result {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Errorf("hashIdentifier() contains invalid hex character: %c", c)
					break
				}
			}
		})
	}
}

func TestHashIdentifier_Deterministic(t *testing.T) {
	parts := []string{"test", "input", "values"}

	hash1 := hashIdentifier(parts...)
	hash2 := hashIdentifier(parts...)

	if hash1 != hash2 {
		t.Errorf("hashIdentifier() not deterministic: %q != %q", hash1, hash2)
	}
}

func TestHashIdentifier_DifferentInputs(t *testing.T) {
	hash1 := hashIdentifier("input1")
	hash2 := hashIdentifier("input2")

	if hash1 == hash2 {
		t.Errorf("Different inputs produced same hash: %q", hash1)
	}
}

func TestNewFingerprintGeneratorWithOptions_Defaults(t *testing.T) {
	// Test that defaults are applied when empty values are passed
	g := NewFingerprintGeneratorWithOptions("", "", nil)

	if g.dmiPath != defaultDMIPath {
		t.Errorf("dmiPath = %q, want default %q", g.dmiPath, defaultDMIPath)
	}

	if g.imdsEndpoint != defaultIMDSEndpoint {
		t.Errorf("imdsEndpoint = %q, want default %q", g.imdsEndpoint, defaultIMDSEndpoint)
	}

	if g.httpClient == nil {
		t.Error("httpClient is nil, want default client")
	}
}

func TestTryDMI_NormalizeCase(t *testing.T) {
	// Test that DMI UUID is normalized to lowercase
	tmpDir := t.TempDir()
	dmiPath := filepath.Join(tmpDir, "product_uuid")
	if err := os.WriteFile(dmiPath, []byte("UPPERCASE-UUID-1234"), 0644); err != nil {
		t.Fatalf("failed to write test DMI file: %v", err)
	}

	g := NewFingerprintGeneratorWithOptions(dmiPath, "", nil)
	fp, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if fp.ID != "uppercase-uuid-1234" {
		t.Errorf("ID = %q, want lowercase", fp.ID)
	}
}

func TestTryDMI_TrimWhitespace(t *testing.T) {
	// Test that whitespace is trimmed from DMI UUID
	tmpDir := t.TempDir()
	dmiPath := filepath.Join(tmpDir, "product_uuid")
	if err := os.WriteFile(dmiPath, []byte("  test-uuid  \n"), 0644); err != nil {
		t.Fatalf("failed to write test DMI file: %v", err)
	}

	g := NewFingerprintGeneratorWithOptions(dmiPath, "", nil)
	fp, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if fp.ID != "test-uuid" {
		t.Errorf("ID = %q, want trimmed 'test-uuid'", fp.ID)
	}
}
