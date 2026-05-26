// Package main demonstrates realistic library usage of the EVE ESI client.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/Sternrassler/eve-esi-client/pkg/client"
	"github.com/redis/go-redis/v9"
)

// MarketOrder represents an ESI market order.
type MarketOrder struct {
	OrderID      int64   `json:"order_id"`
	TypeID       int     `json:"type_id"`
	LocationID   int64   `json:"location_id"`
	VolumeTotal  int     `json:"volume_total"`
	VolumeRemain int     `json:"volume_remain"`
	MinVolume    int     `json:"min_volume"`
	Price        float64 `json:"price"`
	IsBuyOrder   bool    `json:"is_buy_order"`
	Duration     int     `json:"duration"`
	Issued       string  `json:"issued"`
	Range        string  `json:"range"`
}

func main() {
	// 1. Setup Redis connection
	redisClient := redis.NewClient(&redis.Options{
		Addr:     getEnv("REDIS_URL", "localhost:6379"),
		Password: getEnv("REDIS_PASSWORD", ""),
		DB:       0,
	})
	defer func() { _ = redisClient.Close() }()

	// Test Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Printf("❌ Failed to connect to Redis: %v", err)
		return
	}
	fmt.Println("✅ Connected to Redis")

	// 2. Create ESI client with configuration
	cfg := client.DefaultConfig(redisClient, "EVE-ESI-Example/1.0.0 (your-email@example.com)")

	// Optional: Customize configuration
	cfg.MaxRetries = 3
	cfg.InitialBackoff = 1 * time.Second
	cfg.ErrorThreshold = 10 // Block requests when < 10 errors remaining

	esiClient, err := client.New(cfg)
	if err != nil {
		log.Printf("❌ Failed to create ESI client: %v", err)
		return
	}
	defer func() { _ = esiClient.Close() }()
	fmt.Println("✅ ESI client initialized")

	// 3. Fetch market orders for The Forge region (Jita)
	regionID := 10000002
	endpoint := fmt.Sprintf("/v1/markets/%d/orders/", regionID)

	fmt.Printf("\n📊 Fetching market orders from region %d...\n", regionID)

	resp, err := esiClient.Get(ctx, endpoint)
	if err != nil {
		log.Printf("❌ Request failed: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// 4. Handle response
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("❌ ESI returned status %d: %s", resp.StatusCode, string(body))
		return
	}

	// 5. Parse JSON response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("❌ Failed to read response: %v", err)
		return
	}

	var orders []MarketOrder
	if err := json.Unmarshal(body, &orders); err != nil {
		log.Printf("❌ Failed to parse orders: %v", err)
		return
	}

	// 6. Display results
	fmt.Printf("✅ Retrieved %d market orders\n\n", len(orders))

	// Show first 5 orders as example
	displayCount := 5
	if len(orders) < displayCount {
		displayCount = len(orders)
	}

	fmt.Println("📋 Sample Orders:")
	fmt.Println("─────────────────────────────────────────────────────────")
	for i := 0; i < displayCount; i++ {
		order := orders[i]
		orderType := "SELL"
		if order.IsBuyOrder {
			orderType = "BUY "
		}
		fmt.Printf("%s | TypeID: %5d | Price: %12.2f ISK | Volume: %8d\n",
			orderType, order.TypeID, order.Price, order.VolumeTotal)
	}
	fmt.Println("─────────────────────────────────────────────────────────")

	// 7. Demonstrate caching - second request should use cache
	fmt.Println("\n🔄 Making second request (should use cache)...")
	time.Sleep(100 * time.Millisecond) // Small delay to ensure cache is written

	resp2, err := esiClient.Get(ctx, endpoint)
	if err != nil {
		log.Printf("❌ Second request failed: %v", err)
		return
	}
	defer func() { _ = resp2.Body.Close() }()

	switch resp2.StatusCode {
	case 304:
		fmt.Println("✅ 304 Not Modified - cache is working!")
	case 200:
		fmt.Println("✅ 200 OK - data fetched (cache might be new)")
	}

	// 8. Demonstrate error handling with invalid endpoint
	fmt.Println("\n🔍 Testing error handling with invalid endpoint...")
	invalidResp, err := esiClient.Get(ctx, "/v1/invalid/endpoint/")
	if err != nil {
		fmt.Printf("❌ Expected error occurred: %v\n", err)
	} else {
		defer func() { _ = invalidResp.Body.Close() }()
		if invalidResp.StatusCode >= 400 {
			fmt.Printf("⚠️  ESI returned error status: %d\n", invalidResp.StatusCode)
		}
	}

	// 9. Show status information
	fmt.Println("\n📈 Example completed successfully!")
	fmt.Println("\nKey Features Demonstrated:")
	fmt.Println("  ✅ Automatic rate limiting")
	fmt.Println("  ✅ Redis-backed caching")
	fmt.Println("  ✅ ETag-based conditional requests")
	fmt.Println("  ✅ Error handling and retries")
	fmt.Println("  ✅ Structured logging")
}

// getEnv retrieves an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
