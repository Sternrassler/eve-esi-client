package client

import (
	"errors"
	"fmt"
)

// Common errors returned by the client.
var (
	// ErrRetryExhausted is returned when all retry attempts are exhausted.
	ErrRetryExhausted = errors.New("retry attempts exhausted")

	// ErrContextCancelled is returned when the context is cancelled during retry.
	ErrContextCancelled = errors.New("context cancelled")
)

// ESIError represents an ESI-specific error with additional context.
type ESIError struct {
	StatusCode int
	ErrorClass ErrorClass
	Message    string
	Err        error
}

// Error implements the error interface.
func (e *ESIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("ESI %s error (status %d): %s: %v",
			e.ErrorClass, e.StatusCode, e.Message, e.Err)
	}
	return fmt.Sprintf("ESI %s error (status %d): %s",
		e.ErrorClass, e.StatusCode, e.Message)
}

// Unwrap implements error unwrapping for errors.Is/As.
func (e *ESIError) Unwrap() error {
	return e.Err
}

// shouldRetry determines if an error should be retried based on its classification.
func shouldRetry(errorClass ErrorClass) bool {
	switch errorClass {
	case ErrorClassClient:
		// 4xx errors should NOT be retried (wastes error budget)
		return false
	case ErrorClassServer:
		// 5xx server errors should be retried
		return true
	case ErrorClassRateLimit:
		// 520 rate limit errors should be retried
		return true
	case ErrorClassNetwork:
		// Network errors should be retried
		return true
	default:
		return false
	}
}
