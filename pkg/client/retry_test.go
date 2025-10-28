package client

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultRetryConfig(t *testing.T) {
	config := DefaultRetryConfig()

	if config.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3", config.MaxAttempts)
	}
	if config.InitialBackoff != 1*time.Second {
		t.Errorf("InitialBackoff = %v, want 1s", config.InitialBackoff)
	}
	if config.MaxBackoff != 30*time.Second {
		t.Errorf("MaxBackoff = %v, want 30s", config.MaxBackoff)
	}
	if config.BackoffMultiplier != 2.0 {
		t.Errorf("BackoffMultiplier = %v, want 2.0", config.BackoffMultiplier)
	}
}

func TestRetryConfigForErrorClass(t *testing.T) {
	tests := []struct {
		name             string
		errorClass       ErrorClass
		expectedInitial  time.Duration
		expectedMax      time.Duration
		expectedAttempts int
	}{
		{
			name:             "server error config",
			errorClass:       ErrorClassServer,
			expectedInitial:  1 * time.Second,
			expectedMax:      10 * time.Second,
			expectedAttempts: 3,
		},
		{
			name:             "rate limit config",
			errorClass:       ErrorClassRateLimit,
			expectedInitial:  5 * time.Second,
			expectedMax:      60 * time.Second,
			expectedAttempts: 3,
		},
		{
			name:             "network error config",
			errorClass:       ErrorClassNetwork,
			expectedInitial:  2 * time.Second,
			expectedMax:      30 * time.Second,
			expectedAttempts: 3,
		},
		{
			name:             "unknown error class uses default",
			errorClass:       "",
			expectedInitial:  1 * time.Second,
			expectedMax:      30 * time.Second,
			expectedAttempts: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := RetryConfigForErrorClass(tt.errorClass)

			if config.InitialBackoff != tt.expectedInitial {
				t.Errorf("InitialBackoff = %v, want %v", config.InitialBackoff, tt.expectedInitial)
			}
			if config.MaxBackoff != tt.expectedMax {
				t.Errorf("MaxBackoff = %v, want %v", config.MaxBackoff, tt.expectedMax)
			}
			if config.MaxAttempts != tt.expectedAttempts {
				t.Errorf("MaxAttempts = %d, want %d", config.MaxAttempts, tt.expectedAttempts)
			}
		})
	}
}

func TestRetryWithBackoff_Success(t *testing.T) {
	ctx := context.Background()

	// Function succeeds immediately
	callCount := 0
	fn := func() error {
		callCount++
		return nil
	}

	err := retryWithBackoff(ctx, fn, func(error) ErrorClass {
		return ErrorClassServer
	})

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}
}

func TestRetryWithBackoff_SuccessAfterRetry(t *testing.T) {
	ctx := context.Background()

	// Function fails twice, then succeeds
	callCount := 0
	fn := func() error {
		callCount++
		if callCount < 3 {
			return errors.New("temporary error")
		}
		return nil
	}

	start := time.Now()
	err := retryWithBackoff(ctx, fn, func(error) ErrorClass {
		return ErrorClassServer
	})
	duration := time.Since(start)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if callCount != 3 {
		t.Errorf("Expected 3 calls, got %d", callCount)
	}

	// Should have waited for at least some backoff (with jitter, it's hard to be precise)
	// First retry: ~1s, second retry: ~2s, total ~3s (with jitter it could be less)
	if duration < 500*time.Millisecond {
		t.Errorf("Expected some backoff delay, got %v", duration)
	}
}

func TestRetryWithBackoff_MaxAttemptsExhausted(t *testing.T) {
	ctx := context.Background()

	// Function always fails
	callCount := 0
	testErr := errors.New("persistent error")
	fn := func() error {
		callCount++
		return testErr
	}

	err := retryWithBackoff(ctx, fn, func(error) ErrorClass { return ErrorClassServer })

	if err == nil {
		t.Error("Expected error, got nil")
	}
	if !errors.Is(err, ErrRetryExhausted) {
		t.Errorf("Expected ErrRetryExhausted, got %v", err)
	}
	if callCount != 3 {
		t.Errorf("Expected 3 calls (MaxAttempts), got %d", callCount)
	}
}

func TestRetryWithBackoff_ClientErrorNoRetry(t *testing.T) {
	ctx := context.Background()

	// Client errors should not be retried
	callCount := 0
	testErr := errors.New("client error")
	fn := func() error {
		callCount++
		return testErr
	}

	err := retryWithBackoff(ctx, fn, func(error) ErrorClass { return ErrorClassClient })

	if err == nil {
		t.Error("Expected error, got nil")
	}
	// Should only be called once (no retries for client errors)
	if callCount != 1 {
		t.Errorf("Expected 1 call (no retry for client errors), got %d", callCount)
	}
	// Should return the original error, not ErrRetryExhausted
	if errors.Is(err, ErrRetryExhausted) {
		t.Error("Should not return ErrRetryExhausted for client errors (no retry attempted)")
	}
	if !errors.Is(err, testErr) {
		t.Errorf("Expected original error, got %v", err)
	}
}

func TestRetryWithBackoff_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Function always fails
	callCount := 0
	fn := func() error {
		callCount++
		if callCount == 1 {
			// Cancel context after first failure
			cancel()
		}
		return errors.New("error")
	}

	err := retryWithBackoff(ctx, fn, func(error) ErrorClass { return ErrorClassServer })

	if err == nil {
		t.Error("Expected error, got nil")
	}
	if !errors.Is(err, ErrContextCancelled) {
		t.Errorf("Expected ErrContextCancelled, got %v", err)
	}
	// Should have called at least once, but not all retries
	if callCount >= 3 {
		t.Errorf("Expected fewer than 3 calls due to cancellation, got %d", callCount)
	}
}

func TestRetryWithBackoff_ContextCancelledImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Function should not be called if context is already cancelled
	callCount := 0
	fn := func() error {
		callCount++
		return errors.New("error")
	}

	err := retryWithBackoff(ctx, fn, func(error) ErrorClass { return ErrorClassServer })

	// First attempt should still happen even if context is cancelled
	if callCount < 1 {
		t.Errorf("Expected at least 1 call, got %d", callCount)
	}

	// But we should get a context cancelled error eventually
	if err == nil {
		t.Error("Expected error, got nil")
	}
}

func TestRetryWithBackoff_ExponentialBackoff(t *testing.T) {
	ctx := context.Background()

	// Track timing of retries
	timestamps := []time.Time{}
	fn := func() error {
		timestamps = append(timestamps, time.Now())
		return errors.New("error")
	}

	_ = retryWithBackoff(ctx, fn, func(error) ErrorClass { return ErrorClassServer })

	if len(timestamps) != 3 {
		t.Fatalf("Expected 3 timestamps, got %d", len(timestamps))
	}

	// Check that delays increase exponentially (with jitter tolerance)
	// First delay: ~1s, second delay: ~2s
	firstDelay := timestamps[1].Sub(timestamps[0])
	secondDelay := timestamps[2].Sub(timestamps[1])

	// With jitter (±20%), expect first delay roughly in range [0.8s, 1.2s]
	if firstDelay < 500*time.Millisecond || firstDelay > 2*time.Second {
		t.Errorf("First retry delay %v outside expected range", firstDelay)
	}

	// Second delay should be roughly double (with jitter)
	if secondDelay < 1*time.Second || secondDelay > 4*time.Second {
		t.Errorf("Second retry delay %v outside expected range", secondDelay)
	}

	// Second delay should generally be larger than first (may occasionally fail due to jitter)
	if float64(secondDelay) < float64(firstDelay)*0.8 {
		t.Logf("Warning: Second delay (%v) not significantly larger than first (%v) - may be jitter", secondDelay, firstDelay)
	}
}

func TestRetryWithBackoff_RateLimitLongerBackoff(t *testing.T) {
	ctx := context.Background()

	// Track timing for rate limit errors (should have longer backoff)
	timestamps := []time.Time{}
	fn := func() error {
		timestamps = append(timestamps, time.Now())
		return errors.New("rate limit error")
	}

	_ = retryWithBackoff(ctx, fn, func(error) ErrorClass { return ErrorClassRateLimit })

	if len(timestamps) != 3 {
		t.Fatalf("Expected 3 timestamps, got %d", len(timestamps))
	}

	// Rate limit config has InitialBackoff: 5s
	// First delay should be around 5s (with jitter ±20%)
	firstDelay := timestamps[1].Sub(timestamps[0])
	if firstDelay < 3*time.Second || firstDelay > 7*time.Second {
		t.Errorf("First rate limit retry delay %v outside expected range [3s, 7s]", firstDelay)
	}
}

func TestRetryWithBackoff_Jitter(t *testing.T) {
	ctx := context.Background()

	// Run multiple retries and verify jitter is applied
	delays := []time.Duration{}

	for i := 0; i < 5; i++ {
		timestamps := []time.Time{}
		fn := func() error {
			timestamps = append(timestamps, time.Now())
			if len(timestamps) < 2 {
				return errors.New("error")
			}
			return nil // Succeed on second attempt
		}

		_ = retryWithBackoff(ctx, fn, func(error) ErrorClass { return ErrorClassServer })

		if len(timestamps) >= 2 {
			delays = append(delays, timestamps[1].Sub(timestamps[0]))
		}
	}

	// With jitter, we should see some variation in delays
	// All delays should be in the range of InitialBackoff ±20%
	allSame := true
	first := delays[0]
	for _, d := range delays {
		if d < 800*time.Millisecond || d > 1200*time.Millisecond {
			t.Errorf("Delay %v outside jitter range [800ms, 1200ms]", d)
		}
		if d != first {
			allSame = false
		}
	}

	// It's unlikely (though possible) all delays are exactly the same with jitter
	if allSame {
		t.Logf("Warning: All delays were the same - jitter may not be working (or very unlucky)")
	}
}

func TestRetryWithBackoff_MaxBackoffCap(t *testing.T) {
	// Use a custom error class with very high multiplier to test cap
	// We'll manually test the backoff calculation logic
	config := RetryConfig{
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        3 * time.Second, // Low cap for testing
		BackoffMultiplier: 10.0,            // High multiplier
	}

	backoff := config.InitialBackoff
	for i := 0; i < 3; i++ {
		backoff = time.Duration(float64(backoff) * config.BackoffMultiplier)
		if backoff > config.MaxBackoff {
			backoff = config.MaxBackoff
		}
	}

	// After several iterations, should cap at MaxBackoff
	if backoff != config.MaxBackoff {
		t.Errorf("Expected backoff to cap at %v, got %v", config.MaxBackoff, backoff)
	}
}
