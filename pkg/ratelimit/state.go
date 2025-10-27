// Package ratelimit implements ESI error rate limit tracking and request gating.
// It monitors the X-ESI-Error-Limit-Remain and X-ESI-Error-Limit-Reset headers
// to prevent IP bans due to error limit violations.
package ratelimit

import (
	"time"
)

// Redis keys for rate limit state storage.
const (
	RedisKeyErrorsRemaining = "esi:rate_limit:errors_remaining"
	RedisKeyResetTimestamp  = "esi:rate_limit:reset_timestamp"
	RedisKeyLastUpdate      = "esi:rate_limit:last_update"
)

// Thresholds for rate limit decisions.
const (
	// ErrorThresholdCritical blocks all requests when errors remaining falls below this value.
	// This prevents IP bans by stopping requests before hitting the limit.
	ErrorThresholdCritical = 5

	// ErrorThresholdWarning applies throttling when errors remaining falls below this value.
	// This slows down request rate to reduce error accumulation.
	ErrorThresholdWarning = 20

	// ErrorThresholdHealthy indicates normal operation.
	// When errors remaining is at or above this value, no restrictions apply.
	ErrorThresholdHealthy = 50
)

// RateLimitState represents the current ESI error rate limit state.
// This state is shared across all client instances via Redis.
type RateLimitState struct {
	// ErrorsRemaining is the number of errors allowed before ESI blocks requests.
	// Extracted from the X-ESI-Error-Limit-Remain header.
	ErrorsRemaining int `json:"errors_remaining"`

	// ResetAt is the timestamp when the error limit window resets.
	// Calculated from the X-ESI-Error-Limit-Reset header (seconds until reset).
	ResetAt time.Time `json:"reset_at"`

	// LastUpdate is the timestamp when this state was last updated.
	// Used to detect stale state and determine if data should be refreshed.
	LastUpdate time.Time `json:"last_update"`

	// IsHealthy indicates whether the error limit is in a healthy state.
	// True when ErrorsRemaining >= ErrorThresholdHealthy.
	IsHealthy bool `json:"is_healthy"`
}

// IsStale returns true if the state data is older than the given duration.
// Stale state should be refreshed from Redis or ESI headers.
func (s *RateLimitState) IsStale(maxAge time.Duration) bool {
	return time.Since(s.LastUpdate) > maxAge
}

// NeedsCriticalBlock returns true if requests should be blocked due to critical error limit.
func (s *RateLimitState) NeedsCriticalBlock() bool {
	return s.ErrorsRemaining < ErrorThresholdCritical
}

// NeedsThrottling returns true if requests should be throttled due to warning threshold.
func (s *RateLimitState) NeedsThrottling() bool {
	return s.ErrorsRemaining < ErrorThresholdWarning && !s.NeedsCriticalBlock()
}

// TimeUntilReset returns the duration until the error limit resets.
// Returns 0 if the reset time has already passed.
func (s *RateLimitState) TimeUntilReset() time.Duration {
	duration := time.Until(s.ResetAt)
	if duration < 0 {
		return 0
	}
	return duration
}

// UpdateHealth updates the IsHealthy field based on current ErrorsRemaining.
func (s *RateLimitState) UpdateHealth() {
	s.IsHealthy = s.ErrorsRemaining >= ErrorThresholdHealthy
}
