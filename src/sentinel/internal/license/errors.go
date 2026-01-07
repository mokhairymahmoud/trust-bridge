package license

import (
	"errors"
	"fmt"
)

// Sentinel error values for authorization failures.
var (
	// ErrAuthorizationDenied indicates the Control Plane explicitly denied authorization.
	// This is a terminal error - do not retry.
	ErrAuthorizationDenied = errors.New("authorization denied")

	// ErrInvalidResponse indicates the Control Plane returned a malformed response.
	ErrInvalidResponse = errors.New("invalid response from control plane")

	// ErrMaxRetriesExceeded indicates all retry attempts were exhausted.
	ErrMaxRetriesExceeded = errors.New("max retries exceeded")

	// ErrRequestTimeout indicates the request timed out.
	ErrRequestTimeout = errors.New("request timeout")

	// ErrMissingRequiredField indicates a required field was missing from the response.
	ErrMissingRequiredField = errors.New("missing required field in response")
)

// AuthError represents an authorization-specific error with additional context.
type AuthError struct {
	StatusCode int    // HTTP status code (if applicable)
	Status     string // Authorization status from response (e.g., "denied")
	Reason     string // Reason for denial (from response)
	Retryable  bool   // Whether this error is retryable
	Err        error  // Underlying error
}

// Error implements the error interface.
func (e *AuthError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("authorization error: %s (reason: %s, status code: %d)", e.Status, e.Reason, e.StatusCode)
	}
	if e.Err != nil {
		return fmt.Sprintf("authorization error: %v", e.Err)
	}
	return fmt.Sprintf("authorization error: status=%s, code=%d", e.Status, e.StatusCode)
}

// Unwrap returns the underlying error for errors.Is/As compatibility.
func (e *AuthError) Unwrap() error {
	return e.Err
}

// IsRetryable returns true if the error can be retried.
func (e *AuthError) IsRetryable() bool {
	return e.Retryable
}

// NewAuthDeniedError creates an AuthError for explicit denial.
func NewAuthDeniedError(statusCode int, reason string) *AuthError {
	return &AuthError{
		StatusCode: statusCode,
		Status:     "denied",
		Reason:     reason,
		Retryable:  false,
		Err:        ErrAuthorizationDenied,
	}
}

// NewAuthServerError creates an AuthError for server-side errors.
func NewAuthServerError(statusCode int, err error) *AuthError {
	return &AuthError{
		StatusCode: statusCode,
		Status:     "error",
		Retryable:  true,
		Err:        err,
	}
}

// NewAuthNetworkError creates an AuthError for network-related errors.
func NewAuthNetworkError(err error) *AuthError {
	return &AuthError{
		Status:    "network_error",
		Retryable: true,
		Err:       err,
	}
}

// IsTerminalDenial returns true if the error represents a terminal denial
// that should not be retried.
func IsTerminalDenial(err error) bool {
	var authErr *AuthError
	if errors.As(err, &authErr) {
		return !authErr.Retryable && errors.Is(authErr.Err, ErrAuthorizationDenied)
	}
	return errors.Is(err, ErrAuthorizationDenied)
}
