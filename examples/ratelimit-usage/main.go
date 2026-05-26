// Standalone example for the ESI rate-limit tracker.
//
// The Tracker monitors ESI's error-limit headers (shared across instances via
// Redis) and decides whether a request is currently safe to send — preventing
// the permanent IP ban that follows from exhausting the ESI error budget.
//
// Run (requires Redis on localhost:6379):
//
//	cd examples/ratelimit-usage && go run main.go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Sternrassler/eve-esi-client/pkg/ratelimit"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

func main() {
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})
	defer func() { _ = redisClient.Close() }()

	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		fmt.Printf("Redis not available: %v\n", err)
		fmt.Println("This example requires a running Redis instance on localhost:6379")
		return
	}

	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	tracker := ratelimit.NewTracker(redisClient, logger)

	// Ask the tracker whether a request is allowed under the current error budget.
	allowed, err := tracker.ShouldAllowRequest(ctx)
	if err != nil {
		fmt.Printf("Failed to check rate limit: %v\n", err)
		return
	}

	if !allowed {
		state, _ := tracker.GetState(ctx)
		fmt.Printf("🛑 Request blocked — errors remaining: %d (resets at %s)\n",
			state.ErrorsRemaining, state.ResetAt.Format("15:04:05"))
		return
	}
	fmt.Println("✅ Request allowed by rate limiter")

	// ... make your ESI HTTP request here ...
	//
	// After receiving the response, feed ESI's error-limit headers back so the
	// shared state stays in sync across all instances:
	//
	//   blocked, err := tracker.UpdateFromHeaders(ctx, resp.Header)

	state, _ := tracker.GetState(ctx)
	fmt.Printf("Current state — errors remaining: %d\n", state.ErrorsRemaining)
}
