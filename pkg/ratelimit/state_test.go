package ratelimit

import (
	"testing"
	"time"
)

func TestRateLimitState_IsStale(t *testing.T) {
	tests := []struct {
		name     string
		state    *RateLimitState
		maxAge   time.Duration
		expected bool
	}{
		{
			name: "fresh state",
			state: &RateLimitState{
				LastUpdate: time.Now(),
			},
			maxAge:   5 * time.Minute,
			expected: false,
		},
		{
			name: "stale state",
			state: &RateLimitState{
				LastUpdate: time.Now().Add(-10 * time.Minute),
			},
			maxAge:   5 * time.Minute,
			expected: true,
		},
		{
			name: "just under max age",
			state: &RateLimitState{
				LastUpdate: time.Now().Add(-4 * time.Minute),
			},
			maxAge:   5 * time.Minute,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.state.IsStale(tt.maxAge)
			if result != tt.expected {
				t.Errorf("IsStale() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestRateLimitState_NeedsCriticalBlock(t *testing.T) {
	tests := []struct {
		name            string
		errorsRemaining int
		expected        bool
	}{
		{
			name:            "well above critical threshold",
			errorsRemaining: 50,
			expected:        false,
		},
		{
			name:            "at critical threshold",
			errorsRemaining: ErrorThresholdCritical,
			expected:        false,
		},
		{
			name:            "just below critical threshold",
			errorsRemaining: ErrorThresholdCritical - 1,
			expected:        true,
		},
		{
			name:            "zero errors remaining",
			errorsRemaining: 0,
			expected:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &RateLimitState{
				ErrorsRemaining: tt.errorsRemaining,
			}
			result := state.NeedsCriticalBlock()
			if result != tt.expected {
				t.Errorf("NeedsCriticalBlock() = %v, want %v (errors_remaining=%d)", result, tt.expected, tt.errorsRemaining)
			}
		})
	}
}

func TestRateLimitState_NeedsThrottling(t *testing.T) {
	tests := []struct {
		name            string
		errorsRemaining int
		expected        bool
	}{
		{
			name:            "healthy state",
			errorsRemaining: 50,
			expected:        false,
		},
		{
			name:            "at warning threshold",
			errorsRemaining: ErrorThresholdWarning,
			expected:        false,
		},
		{
			name:            "just below warning threshold",
			errorsRemaining: ErrorThresholdWarning - 1,
			expected:        true,
		},
		{
			name:            "just above critical threshold",
			errorsRemaining: ErrorThresholdCritical + 1,
			expected:        true, // Should throttle (below warning but above critical)
		},
		{
			name:            "below critical threshold",
			errorsRemaining: ErrorThresholdCritical - 1,
			expected:        false, // Critical blocks, not throttles
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &RateLimitState{
				ErrorsRemaining: tt.errorsRemaining,
			}
			result := state.NeedsThrottling()
			if result != tt.expected {
				t.Errorf("NeedsThrottling() = %v, want %v (errors_remaining=%d)", result, tt.expected, tt.errorsRemaining)
			}
		})
	}
}

func TestRateLimitState_TimeUntilReset(t *testing.T) {
	tests := []struct {
		name      string
		resetAt   time.Time
		expected  time.Duration
		tolerance time.Duration
	}{
		{
			name:      "reset in future",
			resetAt:   time.Now().Add(5 * time.Minute),
			expected:  5 * time.Minute,
			tolerance: 1 * time.Second,
		},
		{
			name:      "reset already passed",
			resetAt:   time.Now().Add(-5 * time.Minute),
			expected:  0,
			tolerance: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &RateLimitState{
				ResetAt: tt.resetAt,
			}
			result := state.TimeUntilReset()

			if tt.expected == 0 {
				if result != 0 {
					t.Errorf("TimeUntilReset() = %v, want 0 for past reset time", result)
				}
			} else {
				diff := result - tt.expected
				if diff < 0 {
					diff = -diff
				}
				if diff > tt.tolerance {
					t.Errorf("TimeUntilReset() = %v, want approximately %v (tolerance %v)", result, tt.expected, tt.tolerance)
				}
			}
		})
	}
}

func TestRateLimitState_UpdateHealth(t *testing.T) {
	tests := []struct {
		name            string
		errorsRemaining int
		expectedHealthy bool
	}{
		{
			name:            "healthy state",
			errorsRemaining: 100,
			expectedHealthy: true,
		},
		{
			name:            "at healthy threshold",
			errorsRemaining: ErrorThresholdHealthy,
			expectedHealthy: true,
		},
		{
			name:            "just below healthy threshold",
			errorsRemaining: ErrorThresholdHealthy - 1,
			expectedHealthy: false,
		},
		{
			name:            "warning state",
			errorsRemaining: 15,
			expectedHealthy: false,
		},
		{
			name:            "critical state",
			errorsRemaining: 3,
			expectedHealthy: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &RateLimitState{
				ErrorsRemaining: tt.errorsRemaining,
				IsHealthy:       false, // Start as unhealthy
			}
			state.UpdateHealth()

			if state.IsHealthy != tt.expectedHealthy {
				t.Errorf("UpdateHealth() set IsHealthy = %v, want %v (errors_remaining=%d)",
					state.IsHealthy, tt.expectedHealthy, tt.errorsRemaining)
			}
		})
	}
}

func TestThresholdConstants(t *testing.T) {
	// Verify threshold ordering
	if ErrorThresholdCritical >= ErrorThresholdWarning {
		t.Errorf("ErrorThresholdCritical (%d) must be less than ErrorThresholdWarning (%d)",
			ErrorThresholdCritical, ErrorThresholdWarning)
	}

	if ErrorThresholdWarning >= ErrorThresholdHealthy {
		t.Errorf("ErrorThresholdWarning (%d) must be less than ErrorThresholdHealthy (%d)",
			ErrorThresholdWarning, ErrorThresholdHealthy)
	}
}
