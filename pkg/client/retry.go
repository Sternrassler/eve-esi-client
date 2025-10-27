package client

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
)

// Prometheus metrics for retry operations.
var (
	esiRetriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "esi_retries_total",
		Help: "Total number of retry attempts by error class",
	}, []string{"error_class"})

	esiRetryBackoffSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "esi_retry_backoff_seconds",
		Help:    "Backoff duration for retries by error class",
		Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60},
	}, []string{"error_class"})

	esiRetryExhaustedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "esi_retry_exhausted_total",
		Help: "Total number of times retry attempts were exhausted by error class",
	}, []string{"error_class"})
)

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
func retryWithBackoff(ctx context.Context, errorClass ErrorClass, fn func() error) error {
	config := RetryConfigForErrorClass(errorClass)

	var lastErr error
	backoff := config.InitialBackoff

	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		// Execute the function
		err := fn()
		if err == nil {
			// Success
			if attempt > 1 {
				// Log successful retry
				log.Info().
					Str("error_class", string(errorClass)).
					Int("attempt", attempt).
					Msg("Request succeeded after retry")
			}
			return nil
		}

		lastErr = err

		// Check if we should retry this error
		if !shouldRetry(errorClass) {
			// Don't retry client errors - return immediately
			return lastErr
		}

		// If this was the last attempt, don't wait
		if attempt >= config.MaxAttempts {
			break
		}

		// Record retry metrics
		esiRetriesTotal.WithLabelValues(string(errorClass)).Inc()

		// Add jitter (±20% randomness)
		jitter := time.Duration(float64(backoff) * (0.8 + rand.Float64()*0.4))
		esiRetryBackoffSeconds.WithLabelValues(string(errorClass)).Observe(jitter.Seconds())

		log.Debug().
			Str("error_class", string(errorClass)).
			Int("attempt", attempt).
			Dur("backoff", jitter).
			Msg("Retrying request after backoff")

		// Wait with context cancellation support
		select {
		case <-ctx.Done():
			log.Warn().
				Str("error_class", string(errorClass)).
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
	esiRetryExhaustedTotal.WithLabelValues(string(errorClass)).Inc()
	log.Warn().
		Str("error_class", string(errorClass)).
		Int("max_attempts", config.MaxAttempts).
		Msg("Retry attempts exhausted")

	return fmt.Errorf("%w after %d attempts: %v", ErrRetryExhausted, config.MaxAttempts, lastErr)
}
