package license

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// Default client configuration values.
const (
	DefaultClientVersion = "sentinel/0.1.0"
	DefaultMaxRetries    = 3
	DefaultInitialDelay  = 2 * time.Second
	DefaultMaxDelay      = 30 * time.Second
	DefaultRequestTimeout = 30 * time.Second
	authorizePath         = "/api/v1/license/authorize"
)

// AuthRequest represents the authorization request payload sent to the Control Plane.
type AuthRequest struct {
	ContractID    string `json:"contract_id"`
	AssetID       string `json:"asset_id"`
	HardwareID    string `json:"hw_id"`
	Attestation   string `json:"attestation,omitempty"`
	ClientVersion string `json:"client_version"`
}

// AuthResponse represents the authorization response from the Control Plane.
type AuthResponse struct {
	Status           string    `json:"status"`                       // "authorized" or "denied"
	SASUrl           string    `json:"sas_url,omitempty"`            // SAS URL for model.tbenc
	ManifestUrl      string    `json:"manifest_url,omitempty"`       // SAS URL for manifest
	DecryptionKeyHex string    `json:"decryption_key_hex,omitempty"` // 64 hex chars (32 bytes)
	ExpiresAt        time.Time `json:"expires_at,omitempty"`         // When authorization expires
	Reason           string    `json:"reason,omitempty"`             // Reason for denial
}

// LicenseClient handles communication with the Control Plane for authorization.
type LicenseClient struct {
	endpoint       string
	httpClient     *http.Client
	clientVersion  string
	maxRetries     int
	initialDelay   time.Duration
	maxDelay       time.Duration
}

// LicenseClientOption is a functional option for configuring LicenseClient.
type LicenseClientOption func(*LicenseClient)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) LicenseClientOption {
	return func(c *LicenseClient) {
		c.httpClient = client
	}
}

// WithClientVersion sets the client version string.
func WithClientVersion(version string) LicenseClientOption {
	return func(c *LicenseClient) {
		c.clientVersion = version
	}
}

// WithRetryConfig sets the retry configuration.
func WithRetryConfig(maxRetries int, initialDelay, maxDelay time.Duration) LicenseClientOption {
	return func(c *LicenseClient) {
		c.maxRetries = maxRetries
		c.initialDelay = initialDelay
		c.maxDelay = maxDelay
	}
}

// NewLicenseClient creates a new authorization client.
func NewLicenseClient(endpoint string, opts ...LicenseClientOption) *LicenseClient {
	c := &LicenseClient{
		endpoint:      endpoint,
		clientVersion: DefaultClientVersion,
		maxRetries:    DefaultMaxRetries,
		initialDelay:  DefaultInitialDelay,
		maxDelay:      DefaultMaxDelay,
		httpClient: &http.Client{
			Timeout: DefaultRequestTimeout,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Authorize calls the Control Plane to request authorization for an asset.
// It retries on transient failures but fails immediately on terminal denials (401/403).
func (c *LicenseClient) Authorize(ctx context.Context, contractID, assetID, hwID string) (*AuthResponse, error) {
	return c.AuthorizeWithAttestation(ctx, contractID, assetID, hwID, "")
}

// AuthorizeWithAttestation calls the Control Plane with an optional attestation token.
func (c *LicenseClient) AuthorizeWithAttestation(ctx context.Context, contractID, assetID, hwID, attestation string) (*AuthResponse, error) {
	req := &AuthRequest{
		ContractID:    contractID,
		AssetID:       assetID,
		HardwareID:    hwID,
		Attestation:   attestation,
		ClientVersion: c.clientVersion,
	}

	return c.doWithRetry(ctx, req)
}

// doWithRetry executes the request with exponential backoff retry logic.
func (c *LicenseClient) doWithRetry(ctx context.Context, req *AuthRequest) (*AuthResponse, error) {
	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			// Calculate backoff delay with jitter
			delay := c.calculateBackoff(attempt)

			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("authorize: context cancelled: %w", ctx.Err())
			case <-time.After(delay):
				// Continue with retry
			}
		}

		resp, err := c.doRequest(ctx, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Check if error is terminal (should not retry)
		if IsTerminalDenial(err) {
			return nil, err
		}

		// Check if error is retryable
		var authErr *AuthError
		if ok := isAuthError(err, &authErr); ok && !authErr.Retryable {
			return nil, err
		}

		// Log retry attempt (in production, use proper logging)
		// For now, continue silently
	}

	return nil, fmt.Errorf("authorize: %w: %v", ErrMaxRetriesExceeded, lastErr)
}

// doRequest executes a single authorization request.
func (c *LicenseClient) doRequest(ctx context.Context, req *AuthRequest) (*AuthResponse, error) {
	// Marshal request body
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("authorize: failed to marshal request: %w", err)
	}

	// Build URL
	url := c.endpoint + authorizePath

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("authorize: failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Execute request
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, NewAuthNetworkError(fmt.Errorf("request failed: %w", err))
	}
	defer httpResp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("authorize: failed to read response: %w", err)
	}

	// Handle HTTP status codes
	switch httpResp.StatusCode {
	case http.StatusOK:
		// Success - parse response
		return c.parseSuccessResponse(respBody)

	case http.StatusUnauthorized, http.StatusForbidden:
		// Terminal denial - do not retry
		var resp AuthResponse
		if err := json.Unmarshal(respBody, &resp); err == nil && resp.Reason != "" {
			return nil, NewAuthDeniedError(httpResp.StatusCode, resp.Reason)
		}
		return nil, NewAuthDeniedError(httpResp.StatusCode, "access denied")

	case http.StatusBadRequest:
		// Bad request - do not retry
		return nil, &AuthError{
			StatusCode: httpResp.StatusCode,
			Status:     "bad_request",
			Reason:     string(respBody),
			Retryable:  false,
			Err:        ErrInvalidResponse,
		}

	case http.StatusTooManyRequests:
		// Rate limited - retry
		return nil, NewAuthServerError(httpResp.StatusCode, fmt.Errorf("rate limited"))

	default:
		// Server errors (5xx) - retry
		if httpResp.StatusCode >= 500 {
			return nil, NewAuthServerError(httpResp.StatusCode, fmt.Errorf("server error: %s", string(respBody)))
		}
		// Other errors - don't retry
		return nil, &AuthError{
			StatusCode: httpResp.StatusCode,
			Status:     "error",
			Reason:     string(respBody),
			Retryable:  false,
			Err:        ErrInvalidResponse,
		}
	}
}

// parseSuccessResponse parses a successful authorization response.
func (c *LicenseClient) parseSuccessResponse(body []byte) (*AuthResponse, error) {
	var resp AuthResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("authorize: failed to parse response: %w", err)
	}

	// Check authorization status
	if resp.Status != "authorized" {
		if resp.Status == "denied" {
			return nil, NewAuthDeniedError(http.StatusOK, resp.Reason)
		}
		return nil, fmt.Errorf("authorize: unexpected status: %s", resp.Status)
	}

	// Validate required fields for authorized response
	if resp.SASUrl == "" {
		return nil, fmt.Errorf("authorize: %w: sas_url", ErrMissingRequiredField)
	}
	if resp.DecryptionKeyHex == "" {
		return nil, fmt.Errorf("authorize: %w: decryption_key_hex", ErrMissingRequiredField)
	}

	return &resp, nil
}

// calculateBackoff calculates the backoff delay for a retry attempt.
// Uses exponential backoff with jitter.
func (c *LicenseClient) calculateBackoff(attempt int) time.Duration {
	// Calculate base delay: initial * 2^(attempt-1)
	delay := c.initialDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay > c.maxDelay {
			delay = c.maxDelay
			break
		}
	}

	// Add jitter: +/- 10%
	jitter := float64(delay) * 0.1 * (2*rand.Float64() - 1)
	delay = time.Duration(float64(delay) + jitter)

	return delay
}

// isAuthError attempts to extract an AuthError from the given error.
func isAuthError(err error, target **AuthError) bool {
	if e, ok := err.(*AuthError); ok {
		*target = e
		return true
	}
	return false
}
