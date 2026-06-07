package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
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

			_, err := tracker.UpdateFromHeaders(context.Background(), headers)

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

// setupTestRedis liefert einen Redis-Client oder skippt, wenn keiner läuft.
func setupTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	ctx := context.Background()
	if err := c.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	c.Del(ctx, RedisKeyErrorsRemaining, RedisKeyResetTimestamp, RedisKeyLastUpdate)
	if keys, err := c.Keys(ctx, "esi:ratelimit:*").Result(); err == nil && len(keys) > 0 {
		c.Del(ctx, keys...)
	}
	return c
}

func TestRecordError_DecrementsWhenKeyExists(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	tr := NewTracker(rc, zerolog.Nop())
	rc.Set(ctx, RedisKeyErrorsRemaining, 10, 0)
	if err := tr.RecordError(ctx); err != nil {
		t.Fatalf("RecordError: %v", err)
	}
	got, _ := rc.Get(ctx, RedisKeyErrorsRemaining).Int()
	if got != 9 {
		t.Errorf("errors_remaining = %d, want 9", got)
	}
}

func TestRecordError_DoesNotCreateKeyWhenAbsent(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	tr := NewTracker(rc, zerolog.Nop())
	if err := tr.RecordError(ctx); err != nil {
		t.Fatalf("RecordError: %v", err)
	}
	st, _ := tr.GetState(ctx)
	if st.ErrorsRemaining != 100 {
		t.Errorf("ErrorsRemaining = %d, want 100 (Default; Key darf nicht angelegt werden)", st.ErrorsRemaining)
	}
}

func TestShouldAllowRequest_ThrottleRespectsContext(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	tr := NewTracker(rc, zerolog.Nop())
	rc.Set(ctx, RedisKeyErrorsRemaining, 10, 0) // warning state (5<=10<20) → würde throtteln
	rc.Set(ctx, RedisKeyResetTimestamp, time.Now().Add(60*time.Second).Unix(), 0)
	// Complete state: last_update muss präsent sein, sonst liefert GetState jetzt
	// ErrIncompleteState (fail-loud) und der Throttle-Pfad würde gar nicht erreicht.
	if lu, err := json.Marshal(time.Now()); err == nil {
		rc.Set(ctx, RedisKeyLastUpdate, lu, 0)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	start := time.Now()
	allowed, err := tr.ShouldAllowRequest(cctx)
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("Throttle schlief trotz cancel ~%v", time.Since(start))
	}
	if allowed || err == nil {
		t.Errorf("erwartet allowed=false,err!=nil; got %v,%v", allowed, err)
	}
}

func TestGetState_AllKeysAbsentReturnsDefaultHealthy(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	tr := NewTracker(rc, zerolog.Nop())

	st, err := tr.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState with no keys must not error: %v", err)
	}
	if st.ErrorsRemaining != 100 || !st.IsHealthy {
		t.Errorf("expected default healthy state (100, healthy), got %d healthy=%v",
			st.ErrorsRemaining, st.IsHealthy)
	}
}

func TestGetState_PartialStateFailsLoud(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	tr := NewTracker(rc, zerolog.Nop())

	// Only errors_remaining present (e.g. set to a CRITICAL value); the other keys
	// are missing. The old code would only check the last read's redis.Nil and could
	// assume a healthy default, masking this critical state. Now it must fail loud.
	rc.Set(ctx, RedisKeyErrorsRemaining, 2, 0)

	_, err := tr.GetState(ctx)
	if err == nil {
		t.Fatal("GetState must fail loud on partial state, got nil error")
	}
	if !errors.Is(err, ErrIncompleteState) {
		t.Errorf("error = %v, want ErrIncompleteState", err)
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

// --- Stale-State-Regression (2026-06-07) ---------------------------------
// ESI liefert die X-ESI-Error-Limit-Header seit der X-Ratelimit-Migration
// nicht mehr; ein einmal niedriger errors_remaining-Wert wurde nie wieder
// angehoben (nur RecordError-Decrements) und drosselte mit TTL -1 dauerhaft
// JEDEN Request — Tage über das 60s-EVE-Fenster hinaus (Prod-Incident:
// Hauling 73s statt <10s). Abgelaufenes Fenster ⇒ State ist stale ⇒ healthy.

func seedCompleteState(t *testing.T, rc *redis.Client, remaining int, resetAt time.Time) {
	t.Helper()
	ctx := context.Background()
	rc.Set(ctx, RedisKeyErrorsRemaining, remaining, 0)
	rc.Set(ctx, RedisKeyResetTimestamp, resetAt.Unix(), 0)
	lu, err := json.Marshal(time.Now())
	if err != nil {
		t.Fatalf("marshal last update: %v", err)
	}
	rc.Set(ctx, RedisKeyLastUpdate, lu, 0)
}

func TestGetState_ExpiredWindowReturnsHealthy(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	tr := NewTracker(rc, zerolog.Nop())

	// Kritisch niedriger Wert, aber das EVE-Fenster ist seit 5 Minuten vorbei.
	seedCompleteState(t, rc, 5, time.Now().Add(-5*time.Minute))

	st, err := tr.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.ErrorsRemaining != 100 || !st.IsHealthy {
		t.Errorf("expired window must yield healthy default, got remaining=%d healthy=%v",
			st.ErrorsRemaining, st.IsHealthy)
	}
}

func TestGetState_ActiveWindowKeepsState(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	tr := NewTracker(rc, zerolog.Nop())

	// Fenster noch aktiv → Wert muss unverändert wirken.
	seedCompleteState(t, rc, 5, time.Now().Add(30*time.Second))

	st, err := tr.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.ErrorsRemaining != 5 || st.IsHealthy {
		t.Errorf("active window must keep stored state, got remaining=%d healthy=%v",
			st.ErrorsRemaining, st.IsHealthy)
	}
}

func TestShouldAllowRequest_NoThrottleWhenWindowExpired(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	tr := NewTracker(rc, zerolog.Nop())

	seedCompleteState(t, rc, 5, time.Now().Add(-5*time.Minute))

	start := time.Now()
	allowed, err := tr.ShouldAllowRequest(ctx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ShouldAllowRequest: %v", err)
	}
	if !allowed {
		t.Error("expired state must not block requests")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expired state must not throttle (slept %v)", elapsed)
	}
}

func TestUpdateFromHeaders_SetsTTLOnAllKeys(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	tr := NewTracker(rc, zerolog.Nop())

	headers := http.Header{}
	headers.Set("X-ESI-Error-Limit-Remain", "42")
	headers.Set("X-ESI-Error-Limit-Reset", "30")

	applied, err := tr.UpdateFromHeaders(ctx, headers)
	if err != nil || !applied {
		t.Fatalf("UpdateFromHeaders: applied=%v err=%v", applied, err)
	}

	for _, key := range []string{RedisKeyErrorsRemaining, RedisKeyResetTimestamp, RedisKeyLastUpdate} {
		ttl := rc.TTL(ctx, key).Val()
		if ttl <= 0 {
			t.Errorf("key %s must have a TTL (stale-state self-destruct), got %v", key, ttl)
		}
	}
}

func TestRecordError_EnsuresTTLOnLegacyKeys(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	tr := NewTracker(rc, zerolog.Nop())

	// Legacy-Zustand: Keys ohne TTL (so wie der vergiftete Prod-State).
	seedCompleteState(t, rc, 10, time.Now().Add(30*time.Second))

	if err := tr.RecordError(ctx); err != nil {
		t.Fatalf("RecordError: %v", err)
	}

	got, _ := rc.Get(ctx, RedisKeyErrorsRemaining).Int()
	if got != 9 {
		t.Errorf("errors_remaining = %d, want 9", got)
	}
	for _, key := range []string{RedisKeyErrorsRemaining, RedisKeyResetTimestamp, RedisKeyLastUpdate} {
		ttl := rc.TTL(ctx, key).Val()
		if ttl <= 0 {
			t.Errorf("key %s must get a TTL after RecordError, got %v", key, ttl)
		}
	}
}
