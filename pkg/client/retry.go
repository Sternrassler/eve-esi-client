package client

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/rs/zerolog/log"
)

// Prometheus metrics for retry operations.
// ...existing code...

// RetryConfig holds the configuration for retry logic.
type RetryConfig struct {
	// MaxAttempts is the maximum number of retry attempts (including the initial request).
	MaxAttempts int

	// InitialBackoff is the initial backoff duration.
	InitialBackoff time.Duration

	// MaxBackoff is the maximum backoff duration.
	MaxBackoff time.Duration

	// BackoffMultiplier is the multiplier for exponential backoff.
	BackoffMultiplier float64
}

// DefaultRetryConfig returns the default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:       3,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        30 * time.Second,
		BackoffMultiplier: 2.0,
	}
}

// RetryConfigForErrorClass returns the appropriate retry configuration for an error class.
func RetryConfigForErrorClass(errorClass ErrorClass) RetryConfig {
	switch errorClass {
	case ErrorClassServer:
		// 5xx server errors - shorter backoff
		return RetryConfig{
			MaxAttempts:       3,
			InitialBackoff:    1 * time.Second,
			MaxBackoff:        10 * time.Second,
			BackoffMultiplier: 2.0,
		}
	case ErrorClassRateLimit:
		// 520 rate limit - longer backoff
		return RetryConfig{
			MaxAttempts:       3,
			InitialBackoff:    5 * time.Second,
			MaxBackoff:        60 * time.Second,
			BackoffMultiplier: 2.0,
		}
	case ErrorClassNetwork:
		// Network errors - medium backoff
		return RetryConfig{
			MaxAttempts:       3,
			InitialBackoff:    2 * time.Second,
			MaxBackoff:        30 * time.Second,
			BackoffMultiplier: 2.0,
		}
	default:
		return DefaultRetryConfig()
	}
}

// retryWithBackoff executes a function with exponential backoff retry logic.
// It respects context cancellation and adds jitter to prevent thundering herd.
// The classifyFn callback is called after each error to determine the error class dynamically.
func retryWithBackoff(ctx context.Context, fn func() error, classifyFn func(error) ErrorClass) error {
	var lastErr error
	var currentClass ErrorClass
	var config RetryConfig
	var backoff time.Duration

	for attempt := 1; ; attempt++ {
		// Execute the function
		err := fn()
		if err == nil {
			// Success
			if attempt > 1 {
				// Log successful retry
				log.Info().
					Str("error_class", string(currentClass)).
					Int("attempt", attempt).
					Msg("Request succeeded after retry")
			}
			return nil
		}

		lastErr = err

		// Classify the error to get appropriate retry config
		currentClass = classifyFn(err)
		config = RetryConfigForErrorClass(currentClass)

		// Check if we should retry this error
		if !shouldRetry(currentClass) {
			// Don't retry client errors - return immediately
			return lastErr
		}

		// If this was the last attempt, don't wait
		if attempt >= config.MaxAttempts {
			break
		}

		// Initialize backoff on first retry
		if attempt == 1 {
			backoff = config.InitialBackoff
		}

		// Record retry metrics
		esiRetriesTotal.WithLabelValues(string(currentClass)).Inc()

		// Add jitter (±20% randomness)
		jitter := time.Duration(float64(backoff) * (0.8 + rand.Float64()*0.4))
		esiRetryBackoffSeconds.WithLabelValues(string(currentClass)).Observe(jitter.Seconds())

		log.Debug().
			Str("error_class", string(currentClass)).
			Int("attempt", attempt).
			Dur("backoff", jitter).
			Msg("Retrying request after backoff")

		// Wait with context cancellation support
		select {
		case <-ctx.Done():
			log.Warn().
				Str("error_class", string(currentClass)).
				Int("attempt", attempt).
				Msg("Context cancelled during retry backoff")
			return fmt.Errorf("%w: %v", ErrContextCancelled, ctx.Err())
		case <-time.After(jitter):
			// Continue to next attempt
		}

		// Calculate next backoff (exponential)
		backoff = time.Duration(float64(backoff) * config.BackoffMultiplier)
		if backoff > config.MaxBackoff {
			backoff = config.MaxBackoff
		}
	}

	// All retries exhausted
	esiRetryExhaustedTotal.WithLabelValues(string(currentClass)).Inc()
	log.Warn().
		Str("error_class", string(currentClass)).
		Int("max_attempts", config.MaxAttempts).
		Msg("Retry attempts exhausted")

	return fmt.Errorf("%w after %d attempts: %v", ErrRetryExhausted, config.MaxAttempts, lastErr)
}
