// Standalone example for the ESI pagination batch fetcher.
//
// The BatchFetcher fetches all pages of a paginated ESI endpoint in parallel
// using a configurable worker pool, handling partial failures gracefully and
// returning whatever data was successfully retrieved.
//
// Run (requires Redis on localhost:6379 and a network connection to ESI):
//
//	cd examples/pagination-usage && go run main.go
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Sternrassler/eve-esi-client/pkg/client"
	"github.com/Sternrassler/eve-esi-client/pkg/pagination"
	"github.com/redis/go-redis/v9"
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

	esiClient, err := client.New(client.DefaultConfig(redisClient, "EVE-ESI-Pagination-Example/1.0 (you@example.com)"))
	if err != nil {
		fmt.Printf("Failed to create ESI client: %v\n", err)
		return
	}

	bf := pagination.NewBatchFetcher(esiClient, pagination.DefaultConfig())

	const endpoint = "/v1/markets/10000002/orders/"
	fmt.Printf("Fetching all pages from %s …\n", endpoint)

	results, err := bf.FetchAllPages(ctx, endpoint)
	if err != nil {
		// Partial-data error: some pages failed but we still have what succeeded.
		if strings.Contains(err.Error(), "partial data") {
			fmt.Printf("Note: partial fetch — %v\n", err)
		} else {
			fmt.Printf("Fetch failed: %v\n", err)
			return
		}
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		fmt.Printf("Context cancelled: %v\n", err)
		return
	}

	totalBytes := 0
	for _, data := range results {
		totalBytes += len(data)
	}

	fmt.Printf("Pages fetched : %d\n", len(results))
	fmt.Printf("Total bytes   : %d\n", totalBytes)
}
