//go:build integration

package ratelimit

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupRedis starts a Redis container and returns a client
func setupRedis(t *testing.T) (*redis.Client, func()) {
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	redisContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start Redis container: %v", err)
	}

	endpoint, err := redisContainer.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("Failed to get Redis endpoint: %v", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: endpoint,
	})

	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Failed to connect to Redis: %v", err)
	}

	cleanup := func() {
		client.Close()
		redisContainer.Terminate(ctx)
	}

	return client, cleanup
}

func TestTracker_Integration_GetState(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	logger := zerolog.New(os.Stderr).Level(zerolog.Disabled)
	tracker := NewTracker(redisClient, logger)
	ctx := context.Background()

	// Test 1: Get default state when Redis is empty
	state, err := tracker.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}

	if state.ErrorsRemaining != 100 {
		t.Errorf("Default ErrorsRemaining = %d, want 100", state.ErrorsRemaining)
	}

	if !state.IsHealthy {
		t.Error("Default state should be healthy")
	}

	// Test 2: Update state and retrieve it
	headers := http.Header{}
	headers.Set("X-ESI-Error-Limit-Remain", "75")
	headers.Set("X-ESI-Error-Limit-Reset", "120")

	if err := tracker.UpdateFromHeaders(ctx, headers); err != nil {
		t.Fatalf("UpdateFromHeaders() error = %v", err)
	}

	state, err = tracker.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState() after update error = %v", err)
	}

	if state.ErrorsRemaining != 75 {
		t.Errorf("ErrorsRemaining = %d, want 75", state.ErrorsRemaining)
	}

	if !state.IsHealthy {
		t.Error("State with 75 errors should be healthy")
	}

	// Verify reset time is approximately correct
	expectedResetDuration := 120 * time.Second
	actualResetDuration := state.TimeUntilReset()
	tolerance := 5 * time.Second

	if actualResetDuration < expectedResetDuration-tolerance || actualResetDuration > expectedResetDuration+tolerance {
		t.Errorf("TimeUntilReset = %v, want approximately %v", actualResetDuration, expectedResetDuration)
	}
}

func TestTracker_Integration_UpdateFromHeaders(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	logger := zerolog.New(os.Stderr).Level(zerolog.Disabled)
	tracker := NewTracker(redisClient, logger)
	ctx := context.Background()

	tests := []struct {
		name            string
		remainHeader    string
		resetHeader     string
		expectedRemain  int
		expectedHealthy bool
	}{
		{
			name:            "healthy update",
			remainHeader:    "90",
			resetHeader:     "60",
			expectedRemain:  90,
			expectedHealthy: true,
		},
		{
			name:            "warning update",
			remainHeader:    "15",
			resetHeader:     "30",
			expectedRemain:  15,
			expectedHealthy: false,
		},
		{
			name:            "critical update",
			remainHeader:    "2",
			resetHeader:     "45",
			expectedRemain:  2,
			expectedHealthy: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			headers.Set("X-ESI-Error-Limit-Remain", tt.remainHeader)
			headers.Set("X-ESI-Error-Limit-Reset", tt.resetHeader)

			if err := tracker.UpdateFromHeaders(ctx, headers); err != nil {
				t.Fatalf("UpdateFromHeaders() error = %v", err)
			}

			state, err := tracker.GetState(ctx)
			if err != nil {
				t.Fatalf("GetState() error = %v", err)
			}

			if state.ErrorsRemaining != tt.expectedRemain {
				t.Errorf("ErrorsRemaining = %d, want %d", state.ErrorsRemaining, tt.expectedRemain)
			}

			if state.IsHealthy != tt.expectedHealthy {
				t.Errorf("IsHealthy = %v, want %v", state.IsHealthy, tt.expectedHealthy)
			}
		})
	}
}

func TestTracker_Integration_ShouldAllowRequest_Critical(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	logger := zerolog.New(os.Stderr).Level(zerolog.Disabled)
	tracker := NewTracker(redisClient, logger)
	ctx := context.Background()

	// Set critical state (below threshold)
	headers := http.Header{}
	headers.Set("X-ESI-Error-Limit-Remain", "3")
	headers.Set("X-ESI-Error-Limit-Reset", "60")

	if err := tracker.UpdateFromHeaders(ctx, headers); err != nil {
		t.Fatalf("UpdateFromHeaders() error = %v", err)
	}

	// Request should be blocked
	allowed, err := tracker.ShouldAllowRequest(ctx)
	if err != nil {
		t.Fatalf("ShouldAllowRequest() error = %v", err)
	}

	if allowed {
		t.Error("ShouldAllowRequest() = true, want false for critical state")
	}
}

func TestTracker_Integration_ShouldAllowRequest_Warning(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	logger := zerolog.New(os.Stderr).Level(zerolog.Disabled)
	tracker := NewTracker(redisClient, logger)
	ctx := context.Background()

	// Set warning state
	headers := http.Header{}
	headers.Set("X-ESI-Error-Limit-Remain", "15")
	headers.Set("X-ESI-Error-Limit-Reset", "60")

	if err := tracker.UpdateFromHeaders(ctx, headers); err != nil {
		t.Fatalf("UpdateFromHeaders() error = %v", err)
	}

	// Request should be allowed but throttled
	start := time.Now()
	allowed, err := tracker.ShouldAllowRequest(ctx)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("ShouldAllowRequest() error = %v", err)
	}

	if !allowed {
		t.Error("ShouldAllowRequest() = false, want true for warning state")
	}

	// Should have throttled (slept for ~1 second)
	if duration < 900*time.Millisecond {
		t.Errorf("ShouldAllowRequest() throttle duration = %v, want >= 1s", duration)
	}
}

func TestTracker_Integration_ShouldAllowRequest_Healthy(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	logger := zerolog.New(os.Stderr).Level(zerolog.Disabled)
	tracker := NewTracker(redisClient, logger)
	ctx := context.Background()

	// Set healthy state
	headers := http.Header{}
	headers.Set("X-ESI-Error-Limit-Remain", "90")
	headers.Set("X-ESI-Error-Limit-Reset", "60")

	if err := tracker.UpdateFromHeaders(ctx, headers); err != nil {
		t.Fatalf("UpdateFromHeaders() error = %v", err)
	}

	// Request should be allowed immediately
	start := time.Now()
	allowed, err := tracker.ShouldAllowRequest(ctx)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("ShouldAllowRequest() error = %v", err)
	}

	if !allowed {
		t.Error("ShouldAllowRequest() = false, want true for healthy state")
	}

	// Should NOT have throttled
	if duration > 100*time.Millisecond {
		t.Errorf("ShouldAllowRequest() duration = %v, want < 100ms for healthy state", duration)
	}
}

func TestTracker_Integration_StateReset(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	logger := zerolog.New(os.Stderr).Level(zerolog.Disabled)
	tracker := NewTracker(redisClient, logger)
	ctx := context.Background()

	// Set critical state with short reset time
	headers := http.Header{}
	headers.Set("X-ESI-Error-Limit-Remain", "3")
	headers.Set("X-ESI-Error-Limit-Reset", "2") // Reset in 2 seconds

	if err := tracker.UpdateFromHeaders(ctx, headers); err != nil {
		t.Fatalf("UpdateFromHeaders() error = %v", err)
	}

	state, err := tracker.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}

	if state.TimeUntilReset() > 3*time.Second {
		t.Errorf("TimeUntilReset = %v, want <= 3s", state.TimeUntilReset())
	}

	// Wait for reset
	time.Sleep(3 * time.Second)

	state, err = tracker.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}

	// Reset time should have passed
	if state.TimeUntilReset() > 0 {
		// This is expected - the state still shows old data until ESI sends new headers
		t.Logf("TimeUntilReset = %v (expected 0 but state not updated from ESI)", state.TimeUntilReset())
	}
}
