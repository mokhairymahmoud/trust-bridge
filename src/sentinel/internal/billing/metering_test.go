package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewMeteringClient(t *testing.T) {
	config := MeteringConfig{
		ResourceID: "test-resource",
		PlanID:     "test-plan",
		Dimension:  "requests",
	}

	client := NewMeteringClient(config)

	if client == nil {
		t.Fatal("NewMeteringClient returned nil")
	}

	// Check defaults
	if client.config.Endpoint != DefaultMeteringEndpoint {
		t.Errorf("Endpoint = %q, want %q", client.config.Endpoint, DefaultMeteringEndpoint)
	}
	if client.config.Timeout != DefaultMeteringTimeout {
		t.Errorf("Timeout = %v, want %v", client.config.Timeout, DefaultMeteringTimeout)
	}
}

func TestMeteringClient_Report_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != http.MethodPost {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Missing or incorrect Authorization header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}

		// Parse request body
		var event UsageEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Errorf("Failed to decode request body: %v", err)
		}

		if event.Quantity != 100 {
			t.Errorf("Quantity = %f, want 100", event.Quantity)
		}

		// Return success response
		resp := UsageEventResponse{
			UsageEventID: "event-123",
			Status:       "Accepted",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewMeteringClient(MeteringConfig{
		Endpoint:   server.URL,
		ResourceID: "test-resource",
		PlanID:     "test-plan",
		Dimension:  "requests",
	}, WithTokenFunc(StaticTokenFunc("test-token")))

	metrics := UsageMetrics{
		RequestCount: 100,
		PeriodStart:  time.Now().Add(-time.Minute),
		PeriodEnd:    time.Now(),
	}

	ctx := context.Background()
	if err := client.Report(ctx, metrics); err != nil {
		t.Fatalf("Report() failed: %v", err)
	}
}

func TestMeteringClient_Report_Duplicate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := UsageEventResponse{
			Status: "Duplicate",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewMeteringClient(MeteringConfig{
		Endpoint:   server.URL,
		ResourceID: "test-resource",
		Dimension:  "requests",
	}, WithTokenFunc(StaticTokenFunc("test-token")))

	metrics := UsageMetrics{RequestCount: 100}

	ctx := context.Background()
	// Duplicate should not return an error
	if err := client.Report(ctx, metrics); err != nil {
		t.Fatalf("Report() should not fail on Duplicate: %v", err)
	}
}

func TestMeteringClient_Report_QuotaExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := UsageEventResponse{
			Status: "Error",
			Error: &MeteringError{
				Code:    "QuotaExceeded",
				Message: "Usage quota exceeded",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewMeteringClient(MeteringConfig{
		Endpoint:   server.URL,
		ResourceID: "test-resource",
		Dimension:  "requests",
	}, WithTokenFunc(StaticTokenFunc("test-token")))

	metrics := UsageMetrics{RequestCount: 100}

	ctx := context.Background()
	err := client.Report(ctx, metrics)

	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("Expected ErrQuotaExceeded, got %v", err)
	}
}

func TestMeteringClient_Report_SubscriptionInactive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := UsageEventResponse{
			Status: "Error",
			Error: &MeteringError{
				Code:    "SubscriptionSuspended",
				Message: "Subscription is suspended",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewMeteringClient(MeteringConfig{
		Endpoint:   server.URL,
		ResourceID: "test-resource",
		Dimension:  "requests",
	}, WithTokenFunc(StaticTokenFunc("test-token")))

	metrics := UsageMetrics{RequestCount: 100}

	ctx := context.Background()
	err := client.Report(ctx, metrics)

	if !errors.Is(err, ErrSubscriptionInactive) {
		t.Fatalf("Expected ErrSubscriptionInactive, got %v", err)
	}
}

func TestMeteringClient_Report_HTTPError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    error
	}{
		{
			name:       "401 Unauthorized",
			statusCode: http.StatusUnauthorized,
			body:       `{"error": {"code": "Unauthorized", "message": "Invalid token"}}`,
			wantErr:    ErrUnauthorized,
		},
		{
			name:       "403 Forbidden",
			statusCode: http.StatusForbidden,
			body:       `{}`,
			wantErr:    ErrSubscriptionInactive,
		},
		{
			name:       "404 Not Found",
			statusCode: http.StatusNotFound,
			body:       `{}`,
			wantErr:    ErrResourceNotFound,
		},
		{
			name:       "429 Too Many Requests",
			statusCode: http.StatusTooManyRequests,
			body:       `{}`,
			wantErr:    ErrQuotaExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := NewMeteringClient(MeteringConfig{
				Endpoint:   server.URL,
				ResourceID: "test-resource",
				Dimension:  "requests",
			}, WithTokenFunc(StaticTokenFunc("test-token")))

			metrics := UsageMetrics{RequestCount: 100}
			ctx := context.Background()
			err := client.Report(ctx, metrics)

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestMeteringClient_Report_TokenError(t *testing.T) {
	client := NewMeteringClient(MeteringConfig{
		ResourceID: "test-resource",
		Dimension:  "requests",
	}, WithTokenFunc(func(ctx context.Context) (string, error) {
		return "", errors.New("token error")
	}))

	metrics := UsageMetrics{RequestCount: 100}
	ctx := context.Background()
	err := client.Report(ctx, metrics)

	if err == nil {
		t.Fatal("Expected error when token retrieval fails")
	}
}

func TestMeteringClient_MetricsToQuantity(t *testing.T) {
	tests := []struct {
		dimension string
		metrics   UsageMetrics
		want      float64
	}{
		{
			dimension: "requests",
			metrics:   UsageMetrics{RequestCount: 100},
			want:      100,
		},
		{
			dimension: "bytes",
			metrics:   UsageMetrics{BytesIn: 500, BytesOut: 1500},
			want:      2000,
		},
		{
			dimension: "gb_transferred",
			metrics:   UsageMetrics{BytesIn: 1073741824, BytesOut: 1073741824}, // 2 GB
			want:      2.0,
		},
		{
			dimension: "unknown",
			metrics:   UsageMetrics{RequestCount: 50},
			want:      50, // Falls back to request count
		},
	}

	for _, tt := range tests {
		t.Run(tt.dimension, func(t *testing.T) {
			client := NewMeteringClient(MeteringConfig{
				Dimension: tt.dimension,
			})

			got := client.metricsToQuantity(tt.metrics)
			if got != tt.want {
				t.Errorf("metricsToQuantity() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestIsSuspendableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"quota exceeded", ErrQuotaExceeded, true},
		{"subscription inactive", ErrSubscriptionInactive, true},
		{"billing disabled", ErrBillingDisabled, true},
		{"resource not found", ErrResourceNotFound, true},
		{"unauthorized", ErrUnauthorized, true},
		{"wrapped quota exceeded", errors.New("wrapped: " + ErrQuotaExceeded.Error()), false},
		{"generic error", errors.New("something went wrong"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSuspendableError(tt.err); got != tt.want {
				t.Errorf("IsSuspendableError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestMeteringError_Error(t *testing.T) {
	err := &MeteringError{
		Code:    "TestError",
		Message: "This is a test error",
	}

	want := "TestError: This is a test error"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestStaticTokenFunc(t *testing.T) {
	tokenFunc := StaticTokenFunc("my-static-token")

	ctx := context.Background()
	token, err := tokenFunc(ctx)

	if err != nil {
		t.Fatalf("StaticTokenFunc returned error: %v", err)
	}
	if token != "my-static-token" {
		t.Errorf("token = %q, want %q", token, "my-static-token")
	}
}

func TestMeteringClient_Report_Expired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := UsageEventResponse{
			Status: "Expired",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewMeteringClient(MeteringConfig{
		Endpoint:   server.URL,
		ResourceID: "test-resource",
		Dimension:  "requests",
	}, WithTokenFunc(StaticTokenFunc("test-token")))

	metrics := UsageMetrics{RequestCount: 100}

	ctx := context.Background()
	err := client.Report(ctx, metrics)

	if err == nil {
		t.Fatal("Expected error on Expired status")
	}
}

func TestMeteringClient_WithOptions(t *testing.T) {
	logger := &mockLogger{}
	customClient := &http.Client{Timeout: 5 * time.Second}

	client := NewMeteringClient(
		MeteringConfig{ResourceID: "test"},
		WithMeteringLogger(logger),
		WithHTTPClient(customClient),
		WithTokenFunc(StaticTokenFunc("custom-token")),
	)

	if client.httpClient != customClient {
		t.Error("WithHTTPClient did not set custom client")
	}

	ctx := context.Background()
	token, _ := client.tokenFunc(ctx)
	if token != "custom-token" {
		t.Errorf("token = %q, want %q", token, "custom-token")
	}
}

func TestMeteringClient_ClassifyError(t *testing.T) {
	tests := []struct {
		name    string
		apiErr  *MeteringError
		wantErr error
	}{
		{
			name:    "QuotaExceeded",
			apiErr:  &MeteringError{Code: "QuotaExceeded", Message: "Quota exceeded"},
			wantErr: ErrQuotaExceeded,
		},
		{
			name:    "UsageQuotaExceeded",
			apiErr:  &MeteringError{Code: "UsageQuotaExceeded", Message: "Quota exceeded"},
			wantErr: ErrQuotaExceeded,
		},
		{
			name:    "SubscriptionNotFound",
			apiErr:  &MeteringError{Code: "SubscriptionNotFound", Message: "Not found"},
			wantErr: ErrSubscriptionInactive,
		},
		{
			name:    "SubscriptionSuspended",
			apiErr:  &MeteringError{Code: "SubscriptionSuspended", Message: "Suspended"},
			wantErr: ErrSubscriptionInactive,
		},
		{
			name:    "ResourceNotFound",
			apiErr:  &MeteringError{Code: "ResourceNotFound", Message: "Not found"},
			wantErr: ErrResourceNotFound,
		},
		{
			name:    "InvalidResourceId",
			apiErr:  &MeteringError{Code: "InvalidResourceId", Message: "Invalid"},
			wantErr: ErrResourceNotFound,
		},
		{
			name:    "Unauthorized",
			apiErr:  &MeteringError{Code: "Unauthorized", Message: "Unauthorized"},
			wantErr: ErrUnauthorized,
		},
		{
			name:    "InvalidToken",
			apiErr:  &MeteringError{Code: "InvalidToken", Message: "Invalid token"},
			wantErr: ErrUnauthorized,
		},
	}

	client := NewMeteringClient(MeteringConfig{})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.classifyError(tt.apiErr)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("classifyError() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
