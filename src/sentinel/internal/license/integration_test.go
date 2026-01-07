package license_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"trustbridge/sentinel/internal/config"
	"trustbridge/sentinel/internal/license"
)

// TestFullAuthorizationFlow tests the complete authorization workflow:
// 1. Load configuration from environment
// 2. Generate hardware fingerprint
// 3. Call authorization endpoint
// 4. Parse and validate response
func TestFullAuthorizationFlow(t *testing.T) {
	// Set up mock control plane
	mockControlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request path and method
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if r.URL.Path != "/api/v1/license/authorize" {
			t.Errorf("Expected /api/v1/license/authorize, got %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Verify request body
		var req license.AuthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Verify expected values
		if req.ContractID != "contract-allow" {
			t.Errorf("ContractID = %q, want contract-allow", req.ContractID)
		}
		if req.AssetID != "tb-asset-e2e-001" {
			t.Errorf("AssetID = %q, want tb-asset-e2e-001", req.AssetID)
		}
		if req.HardwareID == "" {
			t.Error("HardwareID is empty")
		}
		if req.ClientVersion == "" {
			t.Error("ClientVersion is empty")
		}

		// Return authorized response
		resp := map[string]interface{}{
			"status":             "authorized",
			"sas_url":            "https://storage.example.com/model.tbenc?sv=sig",
			"manifest_url":       "https://storage.example.com/manifest.json?sv=sig",
			"decryption_key_hex": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"expires_at":         time.Now().Add(time.Hour).Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockControlPlane.Close()

	// Clear and set environment variables
	clearConfigEnv(t)
	t.Setenv("TB_CONTRACT_ID", "contract-allow")
	t.Setenv("TB_ASSET_ID", "tb-asset-e2e-001")
	t.Setenv("TB_EDC_ENDPOINT", mockControlPlane.URL)

	// Step 1: Load configuration
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}

	if cfg.ContractID != "contract-allow" {
		t.Errorf("ContractID = %q, want contract-allow", cfg.ContractID)
	}
	if cfg.AssetID != "tb-asset-e2e-001" {
		t.Errorf("AssetID = %q, want tb-asset-e2e-001", cfg.AssetID)
	}
	if cfg.EDCEndpoint != mockControlPlane.URL {
		t.Errorf("EDCEndpoint = %q, want %q", cfg.EDCEndpoint, mockControlPlane.URL)
	}

	// Step 2: Generate hardware fingerprint
	hwID, err := license.GenerateHardwareID()
	if err != nil {
		t.Fatalf("GenerateHardwareID() error = %v", err)
	}
	if hwID == "" {
		t.Fatal("Hardware ID is empty")
	}
	t.Logf("Generated hardware ID: %s", hwID)

	// Step 3: Create client and authorize
	client := license.NewLicenseClient(cfg.EDCEndpoint)
	resp, err := client.Authorize(context.Background(), cfg.ContractID, cfg.AssetID, hwID)
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}

	// Step 4: Verify response fields
	if resp.Status != "authorized" {
		t.Errorf("Status = %q, want authorized", resp.Status)
	}
	if resp.SASUrl == "" {
		t.Error("SASUrl is empty")
	}
	if resp.ManifestUrl == "" {
		t.Error("ManifestUrl is empty")
	}
	if resp.DecryptionKeyHex == "" {
		t.Error("DecryptionKeyHex is empty")
	}
	if len(resp.DecryptionKeyHex) != 64 {
		t.Errorf("DecryptionKeyHex length = %d, want 64", len(resp.DecryptionKeyHex))
	}
	if resp.ExpiresAt.IsZero() {
		t.Error("ExpiresAt is zero")
	}
	if resp.ExpiresAt.Before(time.Now()) {
		t.Error("ExpiresAt is in the past")
	}

	t.Logf("Authorization successful: SAS URL starts with %s...", resp.SASUrl[:min(50, len(resp.SASUrl))])
}

// TestFullAuthorizationFlow_Denied tests the denial path.
func TestFullAuthorizationFlow_Denied(t *testing.T) {
	// Set up mock control plane that denies
	mockControlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req license.AuthRequest
		json.NewDecoder(r.Body).Decode(&req)

		if req.ContractID == "contract-deny" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{
				"status": "denied",
				"reason": "subscription_inactive",
			})
			return
		}

		// Default to authorized
		resp := map[string]interface{}{
			"status":             "authorized",
			"sas_url":            "https://storage.example.com/model.tbenc",
			"decryption_key_hex": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockControlPlane.Close()

	// Set up denied contract
	clearConfigEnv(t)
	t.Setenv("TB_CONTRACT_ID", "contract-deny")
	t.Setenv("TB_ASSET_ID", "tb-asset-e2e-001")
	t.Setenv("TB_EDC_ENDPOINT", mockControlPlane.URL)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}

	hwID, err := license.GenerateHardwareID()
	if err != nil {
		t.Fatalf("GenerateHardwareID() error = %v", err)
	}

	client := license.NewLicenseClient(cfg.EDCEndpoint)
	_, err = client.Authorize(context.Background(), cfg.ContractID, cfg.AssetID, hwID)

	if err == nil {
		t.Fatal("Authorize() error = nil, want denial error")
	}

	if !license.IsTerminalDenial(err) {
		t.Errorf("Error should be terminal denial, got: %v", err)
	}

	t.Logf("Authorization correctly denied: %v", err)
}

// TestFingerprintConsistency verifies fingerprint consistency across calls.
func TestFingerprintConsistency(t *testing.T) {
	// Generate fingerprint multiple times
	fp1, err := license.GenerateHardwareFingerprintWithSource()
	if err != nil {
		t.Fatalf("First call error = %v", err)
	}

	fp2, err := license.GenerateHardwareFingerprintWithSource()
	if err != nil {
		t.Fatalf("Second call error = %v", err)
	}

	if fp1.ID != fp2.ID {
		t.Errorf("Fingerprints not consistent: %q != %q", fp1.ID, fp2.ID)
	}

	if fp1.Source != fp2.Source {
		t.Errorf("Sources not consistent: %q != %q", fp1.Source, fp2.Source)
	}

	t.Logf("Fingerprint: ID=%s, Source=%s", fp1.ID, fp1.Source)
}

// TestConfigValidation verifies config validation catches errors.
func TestConfigValidation(t *testing.T) {
	clearConfigEnv(t)

	// Missing all required fields should fail
	_, err := config.Load()
	if err == nil {
		t.Fatal("config.Load() should fail with missing required fields")
	}

	t.Logf("Config validation correctly caught errors: %v", err)
}

// TestFingerprintWithMockedDMI tests fingerprint with mocked DMI file.
func TestFingerprintWithMockedDMI(t *testing.T) {
	// Create temp DMI file
	tmpDir := t.TempDir()
	dmiPath := filepath.Join(tmpDir, "product_uuid")
	testUUID := "TEST-UUID-1234-5678-ABCDEF"
	if err := os.WriteFile(dmiPath, []byte(testUUID), 0644); err != nil {
		t.Fatalf("Failed to write DMI file: %v", err)
	}

	// Create generator with mocked DMI
	generator := license.NewFingerprintGeneratorWithOptions(dmiPath, "", nil)
	fp, err := generator.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if fp.Source != license.SourceDMI {
		t.Errorf("Source = %q, want %q", fp.Source, license.SourceDMI)
	}

	expectedID := "test-uuid-1234-5678-abcdef" // lowercased
	if fp.ID != expectedID {
		t.Errorf("ID = %q, want %q", fp.ID, expectedID)
	}
}

// TestE2EWithMockedDMIAndControlPlane tests the full flow with mocked sources.
func TestE2EWithMockedDMIAndControlPlane(t *testing.T) {
	// Create temp DMI file
	tmpDir := t.TempDir()
	dmiPath := filepath.Join(tmpDir, "product_uuid")
	testUUID := "E2E-TEST-UUID"
	if err := os.WriteFile(dmiPath, []byte(testUUID), 0644); err != nil {
		t.Fatalf("Failed to write DMI file: %v", err)
	}

	// Create mock control plane
	mockControlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req license.AuthRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Verify the hardware ID matches our mocked DMI
		if req.HardwareID != "e2e-test-uuid" {
			t.Errorf("HardwareID = %q, want e2e-test-uuid", req.HardwareID)
		}

		resp := map[string]interface{}{
			"status":             "authorized",
			"sas_url":            "https://storage.example.com/model.tbenc",
			"manifest_url":       "https://storage.example.com/manifest.json",
			"decryption_key_hex": "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
			"expires_at":         time.Now().Add(time.Hour).Format(time.RFC3339),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockControlPlane.Close()

	// Set environment
	clearConfigEnv(t)
	t.Setenv("TB_CONTRACT_ID", "e2e-contract")
	t.Setenv("TB_ASSET_ID", "e2e-asset")
	t.Setenv("TB_EDC_ENDPOINT", mockControlPlane.URL)

	// Load config
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}

	// Generate fingerprint with mocked DMI
	generator := license.NewFingerprintGeneratorWithOptions(dmiPath, "", nil)
	fp, err := generator.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Authorize
	client := license.NewLicenseClient(cfg.EDCEndpoint)
	resp, err := client.Authorize(context.Background(), cfg.ContractID, cfg.AssetID, fp.ID)
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}

	// Verify
	if resp.Status != "authorized" {
		t.Errorf("Status = %q, want authorized", resp.Status)
	}

	t.Logf("E2E test passed: authorized with key %s...", resp.DecryptionKeyHex[:16])
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
