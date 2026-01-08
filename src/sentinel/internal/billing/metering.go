package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Default configuration for Azure Marketplace Metering API
const (
	DefaultMeteringEndpoint = "https://marketplaceapi.microsoft.com"
	DefaultMeteringTimeout  = 30 * time.Second
	DefaultIMDSEndpoint     = "http://169.254.169.254"
	MeteringAPIVersion      = "2018-08-31"
	IMDSAPIVersion          = "2019-08-01"
)

// Errors that indicate the contract should be suspended.
var (
	ErrQuotaExceeded        = errors.New("billing: quota exceeded")
	ErrSubscriptionInactive = errors.New("billing: subscription inactive")
	ErrBillingDisabled      = errors.New("billing: metering disabled")
	ErrResourceNotFound     = errors.New("billing: resource not found")
	ErrUnauthorized         = errors.New("billing: unauthorized")
)

// IsSuspendableError returns true if the error should trigger contract suspension.
func IsSuspendableError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrQuotaExceeded) ||
		errors.Is(err, ErrSubscriptionInactive) ||
		errors.Is(err, ErrBillingDisabled) ||
		errors.Is(err, ErrResourceNotFound) ||
		errors.Is(err, ErrUnauthorized)
}

// MeteringConfig holds Azure Marketplace Metering API configuration.
type MeteringConfig struct {
	Endpoint   string        // API endpoint (default: https://marketplaceapi.microsoft.com)
	ResourceID string        // Azure resource ID
	PlanID     string        // Marketplace plan ID
	Dimension  string        // Billing dimension name
	Timeout    time.Duration // HTTP request timeout
}

// UsageEvent represents an Azure Marketplace usage event.
type UsageEvent struct {
	ResourceID         string    `json:"resourceId"`
	Quantity           float64   `json:"quantity"`
	Dimension          string    `json:"dimension"`
	EffectiveStartTime time.Time `json:"effectiveStartTime"`
	PlanID             string    `json:"planId,omitempty"`
}

// UsageEventResponse is the API response for a usage event.
type UsageEventResponse struct {
	UsageEventID string         `json:"usageEventId,omitempty"`
	Status       string         `json:"status"` // "Accepted", "Expired", "Duplicate", "Error"
	MessageTime  string         `json:"messageTime,omitempty"`
	ResourceID   string         `json:"resourceId,omitempty"`
	Quantity     float64        `json:"quantity,omitempty"`
	Dimension    string         `json:"dimension,omitempty"`
	PlanID       string         `json:"planId,omitempty"`
	Error        *MeteringError `json:"error,omitempty"`
}

// MeteringError represents an error from the Metering API.
type MeteringError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *MeteringError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// TokenFunc provides the bearer token for API authentication.
type TokenFunc func(ctx context.Context) (string, error)

// MeteringClient implements MeterReporter for Azure Marketplace.
type MeteringClient struct {
	config     MeteringConfig
	httpClient *http.Client
	tokenFunc  TokenFunc
	logger     Logger
}

// MeteringClientOption configures the MeteringClient.
type MeteringClientOption func(*MeteringClient)

// WithTokenFunc sets the function to retrieve bearer tokens.
func WithTokenFunc(fn TokenFunc) MeteringClientOption {
	return func(c *MeteringClient) {
		c.tokenFunc = fn
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) MeteringClientOption {
	return func(c *MeteringClient) {
		c.httpClient = client
	}
}

// WithMeteringLogger sets the logger for the metering client.
func WithMeteringLogger(logger Logger) MeteringClientOption {
	return func(c *MeteringClient) {
		c.logger = logger
	}
}

// NewMeteringClient creates a new Azure Marketplace Metering API client.
func NewMeteringClient(config MeteringConfig, opts ...MeteringClientOption) *MeteringClient {
	// Apply defaults
	if config.Endpoint == "" {
		config.Endpoint = DefaultMeteringEndpoint
	}
	if config.Timeout <= 0 {
		config.Timeout = DefaultMeteringTimeout
	}
	if config.Dimension == "" {
		config.Dimension = DefaultDimension
	}

	c := &MeteringClient{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		tokenFunc: DefaultTokenFunc(),
		logger:    defaultLogger{},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Report implements MeterReporter interface.
// It converts UsageMetrics to a usage event and sends it to Azure Marketplace.
func (c *MeteringClient) Report(ctx context.Context, metrics UsageMetrics) error {
	event := &UsageEvent{
		ResourceID:         c.config.ResourceID,
		Quantity:           c.metricsToQuantity(metrics),
		Dimension:          c.config.Dimension,
		EffectiveStartTime: metrics.PeriodStart,
		PlanID:             c.config.PlanID,
	}

	resp, err := c.sendUsageEvent(ctx, event)
	if err != nil {
		return err
	}

	// Check response status
	switch resp.Status {
	case "Accepted":
		c.logger.Info("Usage event accepted",
			"usage_event_id", resp.UsageEventID,
			"quantity", event.Quantity,
			"dimension", event.Dimension,
		)
		return nil
	case "Duplicate":
		// Duplicate is not an error - event was already processed
		c.logger.Info("Usage event duplicate (already processed)",
			"resource_id", event.ResourceID,
			"dimension", event.Dimension,
		)
		return nil
	case "Expired":
		c.logger.Error("Usage event expired",
			"resource_id", event.ResourceID,
			"effective_start_time", event.EffectiveStartTime,
		)
		return fmt.Errorf("usage event expired: effective time too old")
	case "Error":
		if resp.Error != nil {
			return c.classifyError(resp.Error)
		}
		return errors.New("unknown metering error")
	default:
		return fmt.Errorf("unexpected metering status: %s", resp.Status)
	}
}

// sendUsageEvent sends a single usage event to the Azure Marketplace API.
func (c *MeteringClient) sendUsageEvent(ctx context.Context, event *UsageEvent) (*UsageEventResponse, error) {
	// Get authentication token
	token, err := c.tokenFunc(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	// Build request body
	body, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event: %w", err)
	}

	// Build URL
	url := fmt.Sprintf("%s/api/usageEvent?api-version=%s", c.config.Endpoint, MeteringAPIVersion)

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Handle HTTP errors
	if resp.StatusCode >= 400 {
		return nil, c.handleHTTPError(resp.StatusCode, respBody)
	}

	// Parse response
	var result UsageEventResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// metricsToQuantity converts UsageMetrics to a billable quantity.
// The conversion depends on the billing dimension.
func (c *MeteringClient) metricsToQuantity(metrics UsageMetrics) float64 {
	switch c.config.Dimension {
	case "requests":
		return float64(metrics.RequestCount)
	case "tokens":
		// If we had token counts, we'd use them here
		// For now, fall back to request count
		return float64(metrics.RequestCount)
	case "bytes":
		return float64(metrics.TotalBytes())
	case "gb_transferred":
		// Convert bytes to GB
		return float64(metrics.TotalBytes()) / (1024 * 1024 * 1024)
	default:
		// Default to request count
		return float64(metrics.RequestCount)
	}
}

// handleHTTPError converts HTTP error responses to appropriate errors.
func (c *MeteringClient) handleHTTPError(statusCode int, body []byte) error {
	// Try to parse error response
	var apiError struct {
		Error *MeteringError `json:"error"`
	}
	if err := json.Unmarshal(body, &apiError); err == nil && apiError.Error != nil {
		return c.classifyError(apiError.Error)
	}

	// Fall back to status code based errors
	switch statusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrSubscriptionInactive
	case http.StatusNotFound:
		return ErrResourceNotFound
	case http.StatusTooManyRequests:
		return ErrQuotaExceeded
	default:
		return fmt.Errorf("HTTP error %d: %s", statusCode, string(body))
	}
}

// classifyError converts API error codes to appropriate sentinel errors.
func (c *MeteringClient) classifyError(apiErr *MeteringError) error {
	switch apiErr.Code {
	case "QuotaExceeded", "UsageQuotaExceeded":
		return ErrQuotaExceeded
	case "SubscriptionNotFound", "SubscriptionSuspended", "SubscriptionInactive":
		return ErrSubscriptionInactive
	case "ResourceNotFound", "InvalidResourceId":
		return ErrResourceNotFound
	case "Unauthorized", "InvalidToken":
		return ErrUnauthorized
	case "BadArgument", "InvalidRequest":
		// These are client errors, not suspendable
		return fmt.Errorf("metering API error: %s", apiErr.Error())
	default:
		return fmt.Errorf("metering API error: %s", apiErr.Error())
	}
}

// DefaultTokenFunc returns a TokenFunc that uses Azure Managed Identity.
// It retrieves tokens from the Azure Instance Metadata Service (IMDS).
func DefaultTokenFunc() TokenFunc {
	return func(ctx context.Context) (string, error) {
		return getIMDSToken(ctx, DefaultIMDSEndpoint, "https://marketplaceapi.microsoft.com")
	}
}

// getIMDSToken retrieves an access token from Azure IMDS.
func getIMDSToken(ctx context.Context, imdsEndpoint, resource string) (string, error) {
	url := fmt.Sprintf("%s/metadata/identity/oauth2/token?api-version=%s&resource=%s",
		imdsEndpoint, IMDSAPIVersion, resource)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create IMDS request: %w", err)
	}

	req.Header.Set("Metadata", "true")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("IMDS request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("IMDS returned status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresOn   string `json:"expires_on"`
		Resource    string `json:"resource"`
		TokenType   string `json:"token_type"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse IMDS response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", errors.New("IMDS returned empty access token")
	}

	return tokenResp.AccessToken, nil
}

// StaticTokenFunc returns a TokenFunc that always returns the same token.
// Useful for testing.
func StaticTokenFunc(token string) TokenFunc {
	return func(ctx context.Context) (string, error) {
		return token, nil
	}
}
