package ratelimit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// Prometheus metrics for rate limit tracking.
var (
	esiErrorsRemaining = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "esi_errors_remaining",
		Help: "Number of errors remaining in current ESI rate limit window",
	})

	esiRateLimitBlocksTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "esi_rate_limit_blocks_total",
		Help: "Total number of requests blocked due to critical error limit",
	})

	esiRateLimitThrottlesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "esi_rate_limit_throttles_total",
		Help: "Total number of requests throttled due to warning error limit",
	})
)

// Tracker monitors ESI error rate limits and gates requests.
type Tracker struct {
	redis  *redis.Client
	logger zerolog.Logger
}

// NewTracker creates a new rate limit tracker.
func NewTracker(redisClient *redis.Client, logger zerolog.Logger) *Tracker {
	return &Tracker{
		redis:  redisClient,
		logger: logger,
	}
}

// GetState retrieves the current rate limit state from Redis.
// Returns a default healthy state if no data exists in Redis.
func (t *Tracker) GetState(ctx context.Context) (*RateLimitState, error) {
	// Fetch all state fields from Redis
	errorsRemaining, err := t.redis.Get(ctx, RedisKeyErrorsRemaining).Int()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get errors remaining: %w", err)
	}

	resetTimestamp, err := t.redis.Get(ctx, RedisKeyResetTimestamp).Int64()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get reset timestamp: %w", err)
	}

	lastUpdateStr, err := t.redis.Get(ctx, RedisKeyLastUpdate).Result()
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get last update: %w", err)
	}

	// If no state exists in Redis, return default healthy state
	if err == redis.Nil {
		t.logger.Debug().Msg("No rate limit state in Redis, returning default healthy state")
		return &RateLimitState{
			ErrorsRemaining: 100, // Assume healthy until we get real data
			ResetAt:         time.Now().Add(60 * time.Second),
			LastUpdate:      time.Now(),
			IsHealthy:       true,
		}, nil
	}

	var lastUpdate time.Time
	if lastUpdateStr != "" {
		if err := json.Unmarshal([]byte(lastUpdateStr), &lastUpdate); err != nil {
			return nil, fmt.Errorf("parse last update: %w", err)
		}
	}

	state := &RateLimitState{
		ErrorsRemaining: errorsRemaining,
		ResetAt:         time.Unix(resetTimestamp, 0),
		LastUpdate:      lastUpdate,
	}
	state.UpdateHealth()

	return state, nil
}

// UpdateFromHeaders parses ESI rate limit headers and updates Redis state.
func (t *Tracker) UpdateFromHeaders(ctx context.Context, headers http.Header) error {
	// Parse X-ESI-Error-Limit-Remain header
	remainStr := headers.Get("X-ESI-Error-Limit-Remain")
	if remainStr == "" {
		// Header not present - this is OK for non-ESI responses or some endpoints
		return nil
	}

	remain, err := strconv.Atoi(remainStr)
	if err != nil {
		return fmt.Errorf("parse X-ESI-Error-Limit-Remain header: %w", err)
	}

	// Parse X-ESI-Error-Limit-Reset header
	resetStr := headers.Get("X-ESI-Error-Limit-Reset")
	if resetStr == "" {
		return fmt.Errorf("X-ESI-Error-Limit-Reset header missing")
	}

	resetSeconds, err := strconv.Atoi(resetStr)
	if err != nil {
		return fmt.Errorf("parse X-ESI-Error-Limit-Reset header: %w", err)
	}

	// Create updated state
	now := time.Now()
	state := &RateLimitState{
		ErrorsRemaining: remain,
		ResetAt:         now.Add(time.Duration(resetSeconds) * time.Second),
		LastUpdate:      now,
	}
	state.UpdateHealth()

	// Store in Redis atomically
	pipe := t.redis.Pipeline()
	pipe.Set(ctx, RedisKeyErrorsRemaining, remain, 0)
	pipe.Set(ctx, RedisKeyResetTimestamp, state.ResetAt.Unix(), 0)
	
	lastUpdateJSON, err := json.Marshal(state.LastUpdate)
	if err != nil {
		return fmt.Errorf("marshal last update: %w", err)
	}
	pipe.Set(ctx, RedisKeyLastUpdate, lastUpdateJSON, 0)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("store rate limit state in redis: %w", err)
	}

	// Update Prometheus metrics
	esiErrorsRemaining.Set(float64(remain))

	// Log state update
	logEvent := t.logger.Info().
		Int("errors_remaining", remain).
		Time("reset_at", state.ResetAt).
		Bool("is_healthy", state.IsHealthy)

	if state.NeedsCriticalBlock() {
		logEvent = t.logger.Error()
		logEvent.Msg("ESI error limit CRITICAL - requests will be blocked")
	} else if state.NeedsThrottling() {
		logEvent = t.logger.Warn()
		logEvent.Msg("ESI error limit WARNING - requests will be throttled")
	} else {
		logEvent.Msg("ESI error limit state updated")
	}

	return nil
}

// ShouldAllowRequest checks if a request should be allowed based on current rate limit state.
// Returns false if the request should be blocked due to critical error limit.
// Returns true but may sleep for throttling if in warning state.
func (t *Tracker) ShouldAllowRequest(ctx context.Context) (bool, error) {
	state, err := t.GetState(ctx)
	if err != nil {
		return false, fmt.Errorf("get rate limit state: %w", err)
	}

	// Critical: Block all requests
	if state.NeedsCriticalBlock() {
		waitDuration := state.TimeUntilReset()
		
		t.logger.Error().
			Int("errors_remaining", state.ErrorsRemaining).
			Dur("wait_duration", waitDuration).
			Msg("ESI error limit critical - blocking request")

		esiRateLimitBlocksTotal.Inc()
		return false, nil
	}

	// Warning: Apply throttling (1 second sleep)
	if state.NeedsThrottling() {
		t.logger.Warn().
			Int("errors_remaining", state.ErrorsRemaining).
			Msg("ESI error limit warning - throttling request")

		esiRateLimitThrottlesTotal.Inc()
		time.Sleep(1 * time.Second)
	}

	// Healthy: Allow request
	return true, nil
}
