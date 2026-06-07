package ratelimit

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func groupHeaders(group, limit, remaining string) http.Header {
	h := http.Header{}
	h.Set("X-Ratelimit-Group", group)
	h.Set("X-Ratelimit-Limit", limit)
	h.Set("X-Ratelimit-Remaining", remaining)
	h.Set("X-Ratelimit-Used", "2")
	return h
}

func TestParseRateLimitHeader(t *testing.T) {
	tests := []struct {
		in        string
		limit     int
		window    time.Duration
		shouldErr bool
	}{
		{"3600/15m", 3600, 15 * time.Minute, false},
		{"150/15m", 150, 15 * time.Minute, false},
		{"12000/15m", 12000, 15 * time.Minute, false},
		{"100/1h", 100, time.Hour, false},
		{"garbage", 0, 0, true},
		{"/15m", 0, 0, true},
		{"100/", 0, 0, true},
		{"0/15m", 0, 0, true},
	}
	for _, tt := range tests {
		limit, window, err := parseRateLimitHeader(tt.in)
		if tt.shouldErr {
			if err == nil {
				t.Errorf("parse(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil || limit != tt.limit || window != tt.window {
			t.Errorf("parse(%q) = (%d, %v, %v), want (%d, %v, nil)", tt.in, limit, window, err, tt.limit, tt.window)
		}
	}
}

func TestGroupUpdate_StoresStateAndMapping(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	gt := NewGroupTracker(rc, zerolog.Nop())

	applied, err := gt.Update(ctx, "/v1/markets/10000002/orders/", 200, groupHeaders("market-order", "12000/15m", "11991"))
	if err != nil || !applied {
		t.Fatalf("Update: applied=%v err=%v", applied, err)
	}

	group, err := rc.Get(ctx, RedisKeyEndpointPrefix+"/v1/markets/10000002/orders/").Result()
	if err != nil || group != "market-order" {
		t.Errorf("endpoint mapping = (%q, %v), want market-order", group, err)
	}
	for _, key := range []string{RedisKeyGroupPrefix + "market-order", RedisKeyEndpointPrefix + "/v1/markets/10000002/orders/"} {
		if ttl := rc.TTL(ctx, key).Val(); ttl <= 0 {
			t.Errorf("key %s must have TTL, got %v", key, ttl)
		}
	}
}

func TestGroupUpdate_NoHeadersIsLegacyRoute(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	gt := NewGroupTracker(rc, zerolog.Nop())

	applied, err := gt.Update(ctx, "/v3/universe/types/34/", 200, http.Header{})
	if err != nil || applied {
		t.Fatalf("Update without group headers must be (false, nil), got (%v, %v)", applied, err)
	}
	if n := len(rc.Keys(ctx, RedisKeyGroupPrefix+"*").Val()); n != 0 {
		t.Errorf("no group state must be stored, found %d keys", n)
	}
}

func TestGroupGate_UnknownEndpointAllowsImmediately(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	gt := NewGroupTracker(rc, zerolog.Nop())

	start := time.Now()
	if err := gt.Gate(context.Background(), "/v1/never/seen/"); err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("unknown endpoint must not wait, took %v", time.Since(start))
	}
}

func TestGroupGate_HealthyBudgetAllowsImmediately(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	gt := NewGroupTracker(rc, zerolog.Nop())

	if _, err := gt.Update(ctx, "/v1/route/a/b/", 200, groupHeaders("routes", "3600/15m", "3000")); err != nil {
		t.Fatalf("Update: %v", err)
	}
	start := time.Now()
	if err := gt.Gate(ctx, "/v1/route/a/b/"); err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("healthy budget must not wait, took %v", time.Since(start))
	}
}

func TestGroupGate_LowBudgetThrottles(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	gt := NewGroupTracker(rc, zerolog.Nop())

	// 150er-Limit → Schwelle max(7,10)=10; Remaining 5 < 10 ⇒ Throttle-Sleep.
	if _, err := gt.Update(ctx, "/v1/status/", 200, groupHeaders("status", "150/15m", "5")); err != nil {
		t.Fatalf("Update: %v", err)
	}
	start := time.Now()
	if err := gt.Gate(ctx, "/v1/status/"); err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond || elapsed > 2*time.Second {
		t.Errorf("low budget must throttle ~1s, took %v", elapsed)
	}
}

func TestGroupGate_429BlocksUntilRetryAfter(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	gt := NewGroupTracker(rc, zerolog.Nop())

	h := groupHeaders("market-order", "12000/15m", "0")
	h.Set("Retry-After", "1")
	if _, err := gt.Update(ctx, "/v1/markets/10000002/orders/", 429, h); err != nil {
		t.Fatalf("Update(429): %v", err)
	}

	start := time.Now()
	if err := gt.Gate(ctx, "/v1/markets/10000002/orders/"); err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 700*time.Millisecond || elapsed > 2*time.Second {
		t.Errorf("blocked group must wait ~Retry-After (1s), took %v", elapsed)
	}

	// Eine spätere Erfolgsantwort hebt die Sperre auf.
	if _, err := gt.Update(ctx, "/v1/markets/10000002/orders/", 200, groupHeaders("market-order", "12000/15m", "11000")); err != nil {
		t.Fatalf("Update(200): %v", err)
	}
	start = time.Now()
	if err := gt.Gate(ctx, "/v1/markets/10000002/orders/"); err != nil {
		t.Fatalf("Gate after unblock: %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("unblocked group must not wait, took %v", time.Since(start))
	}
}

func TestGroupGate_LongBlockFailsFast(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	gt := NewGroupTracker(rc, zerolog.Nop())

	h := groupHeaders("market-order", "12000/15m", "0")
	h.Set("Retry-After", "900") // 15 min > maxBlockWait
	if _, err := gt.Update(ctx, "/v1/markets/10000002/orders/", 429, h); err != nil {
		t.Fatalf("Update(429): %v", err)
	}

	start := time.Now()
	err := gt.Gate(ctx, "/v1/markets/10000002/orders/")
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("long block must fail fast, took %v", time.Since(start))
	}
	var rlErr *GroupRateLimitedError
	if err == nil || !asGroupRateLimited(err, &rlErr) {
		t.Fatalf("expected GroupRateLimitedError, got %v", err)
	}
	if rlErr.Group != "market-order" || rlErr.RetryAfter < 10*time.Minute {
		t.Errorf("unexpected error contents: %+v", rlErr)
	}
}

func asGroupRateLimited(err error, target **GroupRateLimitedError) bool {
	for err != nil {
		if e, ok := err.(*GroupRateLimitedError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestGroupGate_ContextCancelDuringThrottle(t *testing.T) {
	rc := setupTestRedis(t)
	defer func() { _ = rc.Close() }()
	ctx := context.Background()
	gt := NewGroupTracker(rc, zerolog.Nop())

	if _, err := gt.Update(ctx, "/v1/status/", 200, groupHeaders("status", "150/15m", "5")); err != nil {
		t.Fatalf("Update: %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	start := time.Now()
	if err := gt.Gate(cctx, "/v1/status/"); err == nil {
		t.Error("cancelled context must abort throttle with error")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("cancelled context must not sleep, took %v", time.Since(start))
	}
}
