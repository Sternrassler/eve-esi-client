package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// ErrIncompleteState signals that the rate limit state in Redis is partial:
// some keys are present while others are missing. The state cannot be trusted,
// so callers must treat it as an error rather than assuming a healthy default.
var ErrIncompleteState = errors.New("incomplete rate limit state in redis")

// decrIfExists dekrementiert errors_remaining nur, wenn der Key existiert
// (verhindert, dass ein fehlender Key fälschlich auf -1 gesetzt wird).
var decrIfExists = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
  return redis.call('DECR', KEYS[1])
end
return -1
`)

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

// RecordError dekrementiert das Error-Budget atomar um 1, sobald ein Fehler
// beobachtet wird, für den ESI KEIN Error-Limit-Header lieferte. Nebenläufige
// Requests sehen den niedrigeren Stand sofort (schließt das TOCTOU-Fenster).
func (t *Tracker) RecordError(ctx context.Context) error {
	return decrIfExists.Run(ctx, t.redis, []string{RedisKeyErrorsRemaining}).Err()
}

// GetState retrieves the current rate limit state from Redis.
// Returns a default healthy state only if ALL state keys are absent (a fresh
// tracker). Each read is checked independently: if some keys are present but
// others are missing, or a read fails, the state is incomplete and we surface an
// error instead of silently assuming a healthy default (e.g. 100 errors remaining)
// that could mask a real critical state.
func (t *Tracker) GetState(ctx context.Context) (*RateLimitState, error) {
	// Fetch all state fields from Redis, tracking each key's presence independently.
	errorsRemaining, err := t.redis.Get(ctx, RedisKeyErrorsRemaining).Int()
	errorsMissing := errors.Is(err, redis.Nil)
	if err != nil && !errorsMissing {
		return nil, fmt.Errorf("get errors remaining: %w", err)
	}

	resetTimestamp, err := t.redis.Get(ctx, RedisKeyResetTimestamp).Int64()
	resetMissing := errors.Is(err, redis.Nil)
	if err != nil && !resetMissing {
		return nil, fmt.Errorf("get reset timestamp: %w", err)
	}

	lastUpdateStr, err := t.redis.Get(ctx, RedisKeyLastUpdate).Result()
	lastUpdateMissing := errors.Is(err, redis.Nil)
	if err != nil && !lastUpdateMissing {
		return nil, fmt.Errorf("get last update: %w", err)
	}

	// If NO state exists in Redis (all keys absent), return default healthy state.
	if errorsMissing && resetMissing && lastUpdateMissing {
		t.logger.Debug().Msg("No rate limit state in Redis, returning default healthy state")
		return &RateLimitState{
			ErrorsRemaining: 100, // Assume healthy until we get real data
			ResetAt:         time.Now().Add(60 * time.Second),
			LastUpdate:      time.Now(),
			IsHealthy:       true,
		}, nil
	}

	// Partial state: some keys present, others absent. Assuming a default for the
	// missing fields could mask a critical error budget, so fail loud.
	if errorsMissing || resetMissing || lastUpdateMissing {
		return nil, fmt.Errorf("%w: errors_remaining_present=%t reset_present=%t last_update_present=%t",
			ErrIncompleteState, !errorsMissing, !resetMissing, !lastUpdateMissing)
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
// Returns (true, nil) when a header was successfully applied, (false, nil) when
// no header was present, or (false, err) on parse/storage errors.
func (t *Tracker) UpdateFromHeaders(ctx context.Context, headers http.Header) (bool, error) {
	// Parse X-ESI-Error-Limit-Remain header
	remainStr := headers.Get("X-ESI-Error-Limit-Remain")
	if remainStr == "" {
		// Header not present - this is OK for non-ESI responses or some endpoints
		return false, nil
	}

	remain, err := strconv.Atoi(remainStr)
	if err != nil {
		return false, fmt.Errorf("parse X-ESI-Error-Limit-Remain header: %w", err)
	}

	// Parse X-ESI-Error-Limit-Reset header
	resetStr := headers.Get("X-ESI-Error-Limit-Reset")
	if resetStr == "" {
		return false, fmt.Errorf("X-ESI-Error-Limit-Reset header missing")
	}

	resetSeconds, err := strconv.Atoi(resetStr)
	if err != nil {
		return false, fmt.Errorf("parse X-ESI-Error-Limit-Reset header: %w", err)
	}

	// Get previous state to detect resets
	previousState, _ := t.GetState(ctx)

	// Create updated state
	now := time.Now()
	state := &RateLimitState{
		ErrorsRemaining: remain,
		ResetAt:         now.Add(time.Duration(resetSeconds) * time.Second),
		LastUpdate:      now,
	}
	state.UpdateHealth()

	// Detect rate limit reset (errors remaining increased significantly)
	if previousState != nil && remain > previousState.ErrorsRemaining+50 {
		t.logger.Info().
			Int("previous", previousState.ErrorsRemaining).
			Int("current", remain).
			Msg("ESI error limit reset detected")
	}

	// Store in Redis atomically
	pipe := t.redis.Pipeline()
	pipe.Set(ctx, RedisKeyErrorsRemaining, remain, 0)
	pipe.Set(ctx, RedisKeyResetTimestamp, state.ResetAt.Unix(), 0)

	lastUpdateJSON, err := json.Marshal(state.LastUpdate)
	if err != nil {
		return false, fmt.Errorf("marshal last update: %w", err)
	}
	pipe.Set(ctx, RedisKeyLastUpdate, lastUpdateJSON, 0)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("store rate limit state in redis: %w", err)
	}

	// Log state update
	logEvent := t.logger.Info().
		Int("errors_remaining", remain).
		Time("reset_at", state.ResetAt).
		Bool("is_healthy", state.IsHealthy)

	switch {
	case state.NeedsCriticalBlock():
		logEvent = t.logger.Error()
		logEvent.Msg("ESI error limit CRITICAL - requests will be blocked")
	case state.NeedsThrottling():
		logEvent = t.logger.Warn()
		logEvent.Msg("ESI error limit WARNING - requests will be throttled")
	default:
		logEvent.Msg("ESI error limit state updated")
	}

	return true, nil
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

		return false, nil
	}

	// Warning: Apply throttling (1 second sleep)
	if state.NeedsThrottling() {
		t.logger.Warn().
			Int("errors_remaining", state.ErrorsRemaining).
			Msg("ESI error limit warning - throttling request")

		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}

	// Healthy: Allow request
	return true, nil
}
