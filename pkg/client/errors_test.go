package client

import (
	"errors"
	"testing"
)

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name       string
		errorClass ErrorClass
		expected   bool
	}{
		{
			name:       "client error should not retry",
			errorClass: ErrorClassClient,
			expected:   false,
		},
		{
			name:       "server error should retry",
			errorClass: ErrorClassServer,
			expected:   true,
		},
		{
			name:       "rate limit should retry",
			errorClass: ErrorClassRateLimit,
			expected:   true,
		},
		{
			name:       "network error should retry",
			errorClass: ErrorClassNetwork,
			expected:   true,
		},
		{
			name:       "empty error class should not retry",
			errorClass: "",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldRetry(tt.errorClass)
			if result != tt.expected {
				t.Errorf("shouldRetry(%q) = %v, want %v", tt.errorClass, result, tt.expected)
			}
		})
	}
}

func TestESIError_Error(t *testing.T) {
	tests := []struct {
		name     string
		esiError *ESIError
		expected string
	}{
		{
			name: "error with wrapped error",
			esiError: &ESIError{
				StatusCode: 500,
				ErrorClass: ErrorClassServer,
				Message:    "internal server error",
				Err:        errors.New("connection refused"),
			},
			expected: "ESI server error (status 500): internal server error: connection refused",
		},
		{
			name: "error without wrapped error",
			esiError: &ESIError{
				StatusCode: 404,
				ErrorClass: ErrorClassClient,
				Message:    "not found",
				Err:        nil,
			},
			expected: "ESI client error (status 404): not found",
		},
		{
			name: "rate limit error",
			esiError: &ESIError{
				StatusCode: 520,
				ErrorClass: ErrorClassRateLimit,
				Message:    "rate limit exceeded",
				Err:        nil,
			},
			expected: "ESI rate_limit error (status 520): rate limit exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.esiError.Error()
			if result != tt.expected {
				t.Errorf("Error() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestESIError_Unwrap(t *testing.T) {
	wrappedErr := errors.New("wrapped error")
	esiError := &ESIError{
		StatusCode: 500,
		ErrorClass: ErrorClassServer,
		Message:    "server error",
		Err:        wrappedErr,
	}

	unwrapped := esiError.Unwrap()
	if unwrapped != wrappedErr {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, wrappedErr)
	}

	// Test errors.Is
	if !errors.Is(esiError, wrappedErr) {
		t.Error("errors.Is should work with wrapped error")
	}
}

func TestESIError_UnwrapNil(t *testing.T) {
	esiError := &ESIError{
		StatusCode: 404,
		ErrorClass: ErrorClassClient,
		Message:    "not found",
		Err:        nil,
	}

	unwrapped := esiError.Unwrap()
	if unwrapped != nil {
		t.Errorf("Unwrap() = %v, want nil", unwrapped)
	}
}
