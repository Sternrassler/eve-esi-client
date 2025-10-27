package ratelimit

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestUpdateFromHeaders_ValidHeaders(t *testing.T) {
	tests := []struct {
		name            string
		remainHeader    string
		resetHeader     string
		expectedRemain  int
		expectedHealthy bool
		shouldError     bool
	}{
		{
			name:            "healthy state",
			remainHeader:    "100",
			resetHeader:     "60",
			expectedRemain:  100,
			expectedHealthy: true,
			shouldError:     false,
		},
		{
			name:            "warning state",
			remainHeader:    "15",
			resetHeader:     "30",
			expectedRemain:  15,
			expectedHealthy: false,
			shouldError:     false,
		},
		{
			name:            "critical state",
			remainHeader:    "3",
			resetHeader:     "45",
			expectedRemain:  3,
			expectedHealthy: false,
			shouldError:     false,
		},
		{
			name:            "at healthy threshold",
			remainHeader:    "50",
			resetHeader:     "60",
			expectedRemain:  50,
			expectedHealthy: true,
			shouldError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test headers
			headers := http.Header{}
			headers.Set("X-ESI-Error-Limit-Remain", tt.remainHeader)
			headers.Set("X-ESI-Error-Limit-Reset", tt.resetHeader)

			// Parse headers (mimicking tracker logic)
			if val := headers.Get("X-ESI-Error-Limit-Remain"); val != "" {
				// This simulates the tracker's parsing
				var err error
				if _, err = parseIntHeader(val); err != nil && !tt.shouldError {
					t.Errorf("Failed to parse remain header: %v", err)
					return
				}
			}

			if val := headers.Get("X-ESI-Error-Limit-Reset"); val != "" {
				var err error
				if _, err = parseIntHeader(val); err != nil && !tt.shouldError {
					t.Errorf("Failed to parse reset header: %v", err)
					return
				}
			}

			// Verify the values would create correct state
			state := &RateLimitState{
				ErrorsRemaining: parseIntOrZero(tt.remainHeader),
				ResetAt:         time.Now().Add(time.Duration(parseIntOrZero(tt.resetHeader)) * time.Second),
				LastUpdate:      time.Now(),
			}
			state.UpdateHealth()

			if state.ErrorsRemaining != tt.expectedRemain {
				t.Errorf("ErrorsRemaining = %d, want %d", state.ErrorsRemaining, tt.expectedRemain)
			}

			if state.IsHealthy != tt.expectedHealthy {
				t.Errorf("IsHealthy = %v, want %v", state.IsHealthy, tt.expectedHealthy)
			}
		})
	}
}

func TestUpdateFromHeaders_InvalidHeaders(t *testing.T) {
	logger := zerolog.New(os.Stderr).Level(zerolog.Disabled)
	tracker := NewTracker(nil, logger)

	tests := []struct {
		name         string
		remainHeader string
		resetHeader  string
		shouldError  bool
	}{
		{
			name:         "missing remain header",
			remainHeader: "",
			resetHeader:  "60",
			shouldError:  false, // Should return nil for missing headers
		},
		{
			name:         "invalid remain header",
			remainHeader: "invalid",
			resetHeader:  "60",
			shouldError:  true,
		},
		{
			name:         "invalid reset header",
			remainHeader: "100",
			resetHeader:  "invalid",
			shouldError:  true,
		},
		{
			name:         "both headers missing",
			remainHeader: "",
			resetHeader:  "",
			shouldError:  false, // Should return nil for missing headers
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			if tt.remainHeader != "" {
				headers.Set("X-ESI-Error-Limit-Remain", tt.remainHeader)
			}
			if tt.resetHeader != "" {
				headers.Set("X-ESI-Error-Limit-Reset", tt.resetHeader)
			}

			err := tracker.UpdateFromHeaders(context.Background(), headers)

			if tt.shouldError && err == nil {
				t.Error("Expected error but got nil")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestShouldAllowRequest_Logic(t *testing.T) {
	tests := []struct {
		name            string
		errorsRemaining int
		expectBlock     bool
		expectThrottle  bool
	}{
		{
			name:            "healthy - allow immediately",
			errorsRemaining: 100,
			expectBlock:     false,
			expectThrottle:  false,
		},
		{
			name:            "at healthy threshold - allow immediately",
			errorsRemaining: ErrorThresholdHealthy,
			expectBlock:     false,
			expectThrottle:  false,
		},
		{
			name:            "warning - allow with throttle",
			errorsRemaining: 15,
			expectBlock:     false,
			expectThrottle:  true,
		},
		{
			name:            "critical - block",
			errorsRemaining: 3,
			expectBlock:     true,
			expectThrottle:  false,
		},
		{
			name:            "at critical threshold - allow",
			errorsRemaining: ErrorThresholdCritical,
			expectBlock:     false,
			expectThrottle:  true, // Still in warning range
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &RateLimitState{
				ErrorsRemaining: tt.errorsRemaining,
				ResetAt:         time.Now().Add(60 * time.Second),
				LastUpdate:      time.Now(),
			}
			state.UpdateHealth()

			shouldBlock := state.NeedsCriticalBlock()
			shouldThrottle := state.NeedsThrottling()

			if shouldBlock != tt.expectBlock {
				t.Errorf("NeedsCriticalBlock() = %v, want %v (errors=%d)", shouldBlock, tt.expectBlock, tt.errorsRemaining)
			}

			if shouldThrottle != tt.expectThrottle {
				t.Errorf("NeedsThrottling() = %v, want %v (errors=%d)", shouldThrottle, tt.expectThrottle, tt.errorsRemaining)
			}
		})
	}
}

// Helper functions for testing
func parseIntHeader(val string) (int, error) {
	result := parseIntOrZero(val)
	if result == 0 && val != "0" {
		return 0, http.ErrNotSupported // Dummy error
	}
	return result, nil
}

func parseIntOrZero(val string) int {
	var result int
	for _, ch := range val {
		if ch >= '0' && ch <= '9' {
			result = result*10 + int(ch-'0')
		} else {
			return 0
		}
	}
	return result
}
