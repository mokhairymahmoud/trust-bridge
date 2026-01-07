package license

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthorize_Success(t *testing.T) {
	// Set up mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		if r.URL.Path != authorizePath {
			t.Errorf("Path = %q, want %q", r.URL.Path, authorizePath)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}

		// Verify request body
		var req AuthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode request: %v", err)
		}
		if req.ContractID != "contract-123" {
			t.Errorf("ContractID = %q, want contract-123", req.ContractID)
		}
		if req.AssetID != "asset-456" {
			t.Errorf("AssetID = %q, want asset-456", req.AssetID)
		}

		// Return success response
		resp := AuthResponse{
			Status:           "authorized",
			SASUrl:           "https://storage.example.com/model.tbenc?sv=sig",
			ManifestUrl:      "https://storage.example.com/manifest.json?sv=sig",
			DecryptionKeyHex: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ExpiresAt:        time.Now().Add(time.Hour),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewLicenseClient(server.URL)
	resp, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

	if err != nil {
		t.Fatalf("Authorize() error = %v, want nil", err)
	}

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
}

func TestAuthorize_Denied_401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "denied",
			"reason": "invalid_credentials",
		})
	}))
	defer server.Close()

	client := NewLicenseClient(server.URL)
	_, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

	if err == nil {
		t.Fatal("Authorize() error = nil, want error for 401")
	}

	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Errorf("error should be ErrAuthorizationDenied, got: %v", err)
	}

	if !IsTerminalDenial(err) {
		t.Error("error should be terminal denial")
	}
}

func TestAuthorize_Denied_403(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "denied",
			"reason": "subscription_inactive",
		})
	}))
	defer server.Close()

	client := NewLicenseClient(server.URL)
	_, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

	if err == nil {
		t.Fatal("Authorize() error = nil, want error for 403")
	}

	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Errorf("error should be ErrAuthorizationDenied, got: %v", err)
	}

	// Verify reason is captured
	if !strings.Contains(err.Error(), "subscription_inactive") {
		t.Errorf("error should contain reason, got: %v", err)
	}
}

func TestAuthorize_Denied_Status(t *testing.T) {
	// Server returns 200 but with denied status
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "denied",
			"reason": "contract_expired",
		})
	}))
	defer server.Close()

	client := NewLicenseClient(server.URL)
	_, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

	if err == nil {
		t.Fatal("Authorize() error = nil, want error for denied status")
	}

	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Errorf("error should be ErrAuthorizationDenied, got: %v", err)
	}
}

func TestAuthorize_Retry_500(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)

		if count < 2 {
			// First attempt fails with 500
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
			return
		}

		// Second attempt succeeds
		resp := AuthResponse{
			Status:           "authorized",
			SASUrl:           "https://storage.example.com/model.tbenc",
			DecryptionKeyHex: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Use short delays for testing
	client := NewLicenseClient(server.URL, WithRetryConfig(3, 10*time.Millisecond, 100*time.Millisecond))
	resp, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

	if err != nil {
		t.Fatalf("Authorize() error = %v, want nil (should retry and succeed)", err)
	}

	if resp.Status != "authorized" {
		t.Errorf("Status = %q, want authorized", resp.Status)
	}

	if attempts < 2 {
		t.Errorf("attempts = %d, want at least 2 (retry)", attempts)
	}
}

func TestAuthorize_MaxRetries(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	// Use short delays for testing
	client := NewLicenseClient(server.URL, WithRetryConfig(2, 10*time.Millisecond, 100*time.Millisecond))
	_, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

	if err == nil {
		t.Fatal("Authorize() error = nil, want error after max retries")
	}

	if !errors.Is(err, ErrMaxRetriesExceeded) {
		t.Errorf("error should be ErrMaxRetriesExceeded, got: %v", err)
	}

	// Should have attempted 1 initial + 2 retries = 3 total
	expectedAttempts := int32(3)
	if attempts != expectedAttempts {
		t.Errorf("attempts = %d, want %d", attempts, expectedAttempts)
	}
}

func TestAuthorize_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than client timeout
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client with very short timeout
	shortTimeoutClient := &http.Client{Timeout: 50 * time.Millisecond}
	client := NewLicenseClient(server.URL,
		WithHTTPClient(shortTimeoutClient),
		WithRetryConfig(0, 10*time.Millisecond, 100*time.Millisecond), // No retries for this test
	)

	_, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

	if err == nil {
		t.Fatal("Authorize() error = nil, want timeout error")
	}

	// Error should be network-related
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Logf("Error is not AuthError but that's ok for timeout: %v", err)
	}
}

func TestAuthorize_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	client := NewLicenseClient(server.URL, WithRetryConfig(0, 10*time.Millisecond, 100*time.Millisecond))
	_, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

	if err == nil {
		t.Fatal("Authorize() error = nil, want error for invalid JSON")
	}

	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention parsing failure, got: %v", err)
	}
}

func TestAuthorize_MissingFields(t *testing.T) {
	tests := []struct {
		name     string
		response AuthResponse
		wantErr  string
	}{
		{
			name: "missing_sas_url",
			response: AuthResponse{
				Status:           "authorized",
				DecryptionKeyHex: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
			wantErr: "sas_url",
		},
		{
			name: "missing_decryption_key",
			response: AuthResponse{
				Status: "authorized",
				SASUrl: "https://storage.example.com/model.tbenc",
			},
			wantErr: "decryption_key_hex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer server.Close()

			client := NewLicenseClient(server.URL)
			_, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

			if err == nil {
				t.Fatalf("Authorize() error = nil, want error for missing %s", tt.wantErr)
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want mention of %s", err, tt.wantErr)
			}
		})
	}
}

func TestAuthorize_ExpiresAtParsing(t *testing.T) {
	expiresAt := time.Date(2026, 1, 7, 12, 0, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status":             "authorized",
			"sas_url":            "https://storage.example.com/model.tbenc",
			"decryption_key_hex": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"expires_at":         expiresAt.Format(time.RFC3339),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewLicenseClient(server.URL)
	resp, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}

	if resp.ExpiresAt.IsZero() {
		t.Error("ExpiresAt is zero, should be parsed")
	}

	if !resp.ExpiresAt.Equal(expiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", resp.ExpiresAt, expiresAt)
	}
}

func TestAuthorize_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	client := NewLicenseClient(server.URL)
	_, err := client.Authorize(ctx, "contract-123", "asset-456", "hw-789")

	if err == nil {
		t.Fatal("Authorize() error = nil, want context cancellation error")
	}
}

func TestAuthorize_RateLimited(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)

		if count < 2 {
			// First attempt rate limited
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}

		// Second attempt succeeds
		resp := AuthResponse{
			Status:           "authorized",
			SASUrl:           "https://storage.example.com/model.tbenc",
			DecryptionKeyHex: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewLicenseClient(server.URL, WithRetryConfig(3, 10*time.Millisecond, 100*time.Millisecond))
	resp, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

	if err != nil {
		t.Fatalf("Authorize() error = %v, should retry on 429", err)
	}

	if resp.Status != "authorized" {
		t.Errorf("Status = %q, want authorized", resp.Status)
	}
}

func TestAuthorize_BadRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("missing required field"))
	}))
	defer server.Close()

	// Bad request should not be retried
	client := NewLicenseClient(server.URL, WithRetryConfig(3, 10*time.Millisecond, 100*time.Millisecond))
	_, err := client.Authorize(context.Background(), "contract-123", "asset-456", "hw-789")

	if err == nil {
		t.Fatal("Authorize() error = nil, want error for 400")
	}

	// Verify it contains the error message
	if !strings.Contains(err.Error(), "bad_request") {
		t.Errorf("error should mention bad_request, got: %v", err)
	}
}

func TestAuthorizeWithAttestation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req AuthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode request: %v", err)
			return
		}

		// Verify attestation is included
		if req.Attestation != "test-attestation-token" {
			t.Errorf("Attestation = %q, want test-attestation-token", req.Attestation)
		}

		resp := AuthResponse{
			Status:           "authorized",
			SASUrl:           "https://storage.example.com/model.tbenc",
			DecryptionKeyHex: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewLicenseClient(server.URL)
	_, err := client.AuthorizeWithAttestation(
		context.Background(),
		"contract-123",
		"asset-456",
		"hw-789",
		"test-attestation-token",
	)

	if err != nil {
		t.Fatalf("AuthorizeWithAttestation() error = %v", err)
	}
}

func TestNewLicenseClient_Options(t *testing.T) {
	customClient := &http.Client{Timeout: 60 * time.Second}

	client := NewLicenseClient("https://example.com",
		WithHTTPClient(customClient),
		WithClientVersion("test/1.0.0"),
		WithRetryConfig(5, 5*time.Second, 60*time.Second),
	)

	if client.httpClient != customClient {
		t.Error("httpClient not set by option")
	}
	if client.clientVersion != "test/1.0.0" {
		t.Errorf("clientVersion = %q, want test/1.0.0", client.clientVersion)
	}
	if client.maxRetries != 5 {
		t.Errorf("maxRetries = %d, want 5", client.maxRetries)
	}
	if client.initialDelay != 5*time.Second {
		t.Errorf("initialDelay = %v, want 5s", client.initialDelay)
	}
	if client.maxDelay != 60*time.Second {
		t.Errorf("maxDelay = %v, want 60s", client.maxDelay)
	}
}

func TestCalculateBackoff(t *testing.T) {
	client := &LicenseClient{
		initialDelay: 2 * time.Second,
		maxDelay:     30 * time.Second,
	}

	// Test that backoff increases exponentially
	delay1 := client.calculateBackoff(1)
	delay2 := client.calculateBackoff(2)
	delay3 := client.calculateBackoff(3)

	// Allow for jitter (10%)
	if delay1 < 1800*time.Millisecond || delay1 > 2200*time.Millisecond {
		t.Errorf("delay1 = %v, expected ~2s", delay1)
	}

	if delay2 < 3600*time.Millisecond || delay2 > 4400*time.Millisecond {
		t.Errorf("delay2 = %v, expected ~4s", delay2)
	}

	if delay3 < 7200*time.Millisecond || delay3 > 8800*time.Millisecond {
		t.Errorf("delay3 = %v, expected ~8s", delay3)
	}
}

func TestCalculateBackoff_MaxDelay(t *testing.T) {
	client := &LicenseClient{
		initialDelay: 2 * time.Second,
		maxDelay:     5 * time.Second,
	}

	// After enough attempts, should be capped at maxDelay
	delay := client.calculateBackoff(10)

	// Should be around maxDelay (with jitter)
	if delay < 4500*time.Millisecond || delay > 5500*time.Millisecond {
		t.Errorf("delay = %v, expected ~5s (capped at maxDelay)", delay)
	}
}

func TestAuthError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *AuthError
		contains []string
	}{
		{
			name: "with_reason",
			err: &AuthError{
				StatusCode: 403,
				Status:     "denied",
				Reason:     "subscription_inactive",
			},
			contains: []string{"denied", "subscription_inactive", "403"},
		},
		{
			name: "with_underlying_error",
			err: &AuthError{
				Status: "error",
				Err:    errors.New("connection refused"),
			},
			contains: []string{"connection refused"},
		},
		{
			name: "basic",
			err: &AuthError{
				StatusCode: 500,
				Status:     "error",
			},
			contains: []string{"500", "error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errStr := tt.err.Error()
			for _, substr := range tt.contains {
				if !strings.Contains(errStr, substr) {
					t.Errorf("Error() = %q, want to contain %q", errStr, substr)
				}
			}
		})
	}
}

func TestIsTerminalDenial(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "auth_denied_error",
			err:      NewAuthDeniedError(403, "test"),
			expected: true,
		},
		{
			name:     "bare_denied_error",
			err:      ErrAuthorizationDenied,
			expected: true,
		},
		{
			name:     "server_error",
			err:      NewAuthServerError(500, errors.New("test")),
			expected: false,
		},
		{
			name:     "network_error",
			err:      NewAuthNetworkError(errors.New("timeout")),
			expected: false,
		},
		{
			name:     "other_error",
			err:      errors.New("random error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTerminalDenial(tt.err)
			if result != tt.expected {
				t.Errorf("IsTerminalDenial() = %v, want %v", result, tt.expected)
			}
		})
	}
}
