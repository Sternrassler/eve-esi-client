package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Sternrassler/eve-esi-client/pkg/cache"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Setup Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})
	defer redisClient.Close()

	// Ping Redis to check connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		fmt.Printf("Redis not available: %v\n", err)
		fmt.Println("This example requires a running Redis instance on localhost:6379")
		return
	}

	// Create cache manager
	cacheManager := cache.NewManager(redisClient)

	// Example 1: Basic cache usage
	fmt.Println("=== Example 1: Basic Cache Usage ===")
	basicCacheExample(ctx, cacheManager)

	// Example 2: Conditional requests
	fmt.Println("\n=== Example 2: Conditional Requests ===")
	conditionalRequestExample(ctx, cacheManager)

	// Example 3: Cache key generation
	fmt.Println("\n=== Example 3: Cache Key Generation ===")
	cacheKeyExample()
}

func basicCacheExample(ctx context.Context, manager *cache.Manager) {
	// Create a cache key for market orders
	key := cache.CacheKey{
		Endpoint: "/v1/markets/10000002/orders/",
		QueryParams: url.Values{
			"order_type": []string{"all"},
			"page":       []string{"1"},
		},
	}

	fmt.Printf("Cache key: %s\n", key.String())

	// Try to get from cache
	entry, err := manager.Get(ctx, key)
	if err == cache.ErrCacheMiss {
		fmt.Println("Cache miss - would fetch from ESI here")

		// Simulate an ESI response
		entry = &cache.CacheEntry{
			Data:         []byte(`{"example": "market data"}`),
			ETag:         `"abc123"`,
			Expires:      time.Now().Add(5 * time.Minute),
			LastModified: time.Now().Add(-1 * time.Hour),
			StatusCode:   200,
			Headers:      http.Header{"Content-Type": []string{"application/json"}},
			CachedAt:     time.Now(),
		}

		// Store in cache
		if err := manager.Set(ctx, key, entry); err != nil {
			fmt.Printf("Error setting cache: %v\n", err)
			return
		}
		fmt.Println("Stored in cache")
	} else if err != nil {
		fmt.Printf("Cache error: %v\n", err)
		return
	} else {
		fmt.Println("Cache hit!")
		fmt.Printf("Data: %s\n", string(entry.Data))
		fmt.Printf("TTL: %v\n", entry.TTL())
	}
}

func conditionalRequestExample(ctx context.Context, manager *cache.Manager) {
	// Create cache entry with ETag
	key := cache.CacheKey{
		Endpoint: "/v3/universe/types/",
	}

	entry := &cache.CacheEntry{
		Data:         []byte(`{"type_data": "cached"}`),
		ETag:         `"xyz789"`,
		Expires:      time.Now().Add(1 * time.Hour),
		LastModified: time.Now().Add(-30 * time.Minute),
		StatusCode:   200,
		CachedAt:     time.Now(),
	}

	// Store in cache
	if err := manager.Set(ctx, key, entry); err != nil {
		fmt.Printf("Warning: Failed to store in cache: %v\n", err)
		return
	}

	// Check if we should make a conditional request
	if cache.ShouldMakeConditionalRequest(entry) {
		fmt.Println("Entry supports conditional requests")

		// Create a request
		req, _ := http.NewRequest("GET", "https://esi.evetech.net"+key.Endpoint, nil)

		// Add conditional headers
		cache.AddConditionalHeaders(req, entry)

		fmt.Printf("If-None-Match: %s\n", req.Header.Get("If-None-Match"))
		fmt.Println("Request would be sent to ESI with conditional headers")
		fmt.Println("If ESI returns 304, use cached data and update TTL")
	}
}

func cacheKeyExample() {
	// Example keys for different scenarios

	// 1. Public endpoint with query params
	key1 := cache.CacheKey{
		Endpoint: "/v1/markets/10000002/orders/",
		QueryParams: url.Values{
			"order_type": []string{"all"},
			"page":       []string{"1"},
		},
	}
	fmt.Printf("Public endpoint: %s\n", key1.String())

	// 2. Authenticated endpoint
	key2 := cache.CacheKey{
		Endpoint:    "/v4/characters/{character_id}/assets/",
		CharacterID: 123456789,
	}
	fmt.Printf("Authenticated endpoint: %s\n", key2.String())

	// 3. Endpoint with path parameters
	key3 := cache.CacheKey{
		Endpoint:   "/v1/universe/types/{type_id}/",
		PathParams: map[string]string{"type_id": "34"},
	}
	fmt.Printf("With path params: %s\n", key3.String())

	// 4. Complex key
	key4 := cache.CacheKey{
		Endpoint:   "/v4/markets/{region_id}/orders/",
		PathParams: map[string]string{"region_id": "10000002"},
		QueryParams: url.Values{
			"order_type": []string{"all"},
			"page":       []string{"1"},
		},
		CharacterID: 987654321,
	}
	fmt.Printf("Complex key: %s\n", key4.String())
}

// Example of handling an HTTP response
func handleESIResponse(resp *http.Response, manager *cache.Manager, key cache.CacheKey) error {
	ctx := context.Background()

	// Handle 304 Not Modified
	if resp.StatusCode == http.StatusNotModified {
		fmt.Println("304 Not Modified - using cached data")

		// Note: In production, metrics would be incremented by the Manager internally.
		// This is shown here for demonstration purposes.
		cache.NotModifiedResponses.Inc()

		// Update TTL from new expires header
		if expiresStr := resp.Header.Get("Expires"); expiresStr != "" {
			if newExpires, err := http.ParseTime(expiresStr); err == nil {
				manager.UpdateTTL(ctx, key, newExpires)
			}
		}

		// Get and use cached data
		entry, _ := manager.Get(ctx, key)
		fmt.Printf("Using cached data: %s\n", string(entry.Data))
		return nil
	}

	// Handle 200 OK
	if resp.StatusCode == http.StatusOK {
		// Convert response to cache entry
		entry, err := cache.ResponseToEntry(resp)
		if err != nil {
			return err
		}

		// Store in cache
		if err := manager.Set(ctx, key, entry); err != nil {
			return err
		}

		fmt.Printf("Cached new response (TTL: %v)\n", entry.TTL())

		// Use data
		fmt.Printf("Data: %s\n", string(entry.Data))
	}

	return nil
}

// Example with actual ESI call (commented out - requires valid ESI endpoint)
func exampleWithRealESI() {
	/*
		// This example shows how to use the cache with a real ESI call

		redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
		manager := cache.NewManager(redisClient)
		ctx := context.Background()

		key := cache.CacheKey{
			Endpoint: "/v1/status/",
		}

		// Try cache first
		entry, err := manager.Get(ctx, key)
		if err == cache.ErrCacheMiss {
			// Make request to ESI
			req, _ := http.NewRequest("GET", "https://esi.evetech.net/latest/status/", nil)
			req.Header.Set("User-Agent", "eve-esi-client-example/1.0 (your@email.com)")

			client := &http.Client{}
			resp, err := client.Do(req)
			if err != nil {
				log.Fatal(err)
			}
			defer resp.Body.Close()

			// Cache the response
			entry, _ = cache.ResponseToEntry(resp)
			manager.Set(ctx, key, entry)
		}

		// Use cached data
		fmt.Printf("ESI Status: %s\n", string(entry.Data))
	*/
}
