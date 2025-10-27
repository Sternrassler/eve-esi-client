//go:build integration

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sternrassler/eve-esi-client/pkg/cache"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupRedisContainer creates a Redis container for integration testing.
func setupRedisContainer(t *testing.T) (*redis.Client, func()) {
	t.Helper()

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

	host, err := redisContainer.Host(ctx)
	if err != nil {
		t.Fatalf("Failed to get container host: %v", err)
	}

	port, err := redisContainer.MappedPort(ctx, "6379")
	if err != nil {
		t.Fatalf("Failed to get container port: %v", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: host + ":" + port.Port(),
	})

	cleanup := func() {
		client.Close()
		redisContainer.Terminate(ctx)
	}

	return client, cleanup
}

func TestIntegration_FullRequestFlow(t *testing.T) {
	redisClient, cleanup := setupRedisContainer(t)
	defer cleanup()

	// Track request phases
	requestsMade := 0
	conditionalRequests := 0

	// Create mock ESI server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsMade++

		// Set rate limit headers
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")

		// Handle conditional requests
		if r.Header.Get("If-None-Match") != "" {
			conditionalRequests++
			w.Header().Set("Expires", time.Now().Add(10*time.Minute).Format(http.TimeFormat))
			w.WriteHeader(http.StatusNotModified)
			return
		}

		// First request - return full response
		w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
		w.Header().Set("ETag", `"test-etag-123"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok", "data": [1,2,3]}`))
	}))
	defer server.Close()

	// Create client
	cfg := DefaultConfig(redisClient, "TestApp/1.0.0 (integration@test.com)")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Override HTTP client to use test server
	client.httpClient = &http.Client{
		Transport: &testTransport{server: server},
		Timeout:   30 * time.Second,
	}

	ctx := context.Background()

	// Request 1: Initial request (should hit server)
	t.Log("Request 1: Initial request")
	resp1, err := client.Get(ctx, "/test/endpoint")
	if err != nil {
		t.Fatalf("Request 1 failed: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Request 1 status = %d, want %d", resp1.StatusCode, http.StatusOK)
	}

	if requestsMade != 1 {
		t.Errorf("After request 1: requestsMade = %d, want 1", requestsMade)
	}

	// Wait for cache to be written
	time.Sleep(200 * time.Millisecond)

	// Request 2: Should use cache and make conditional request
	t.Log("Request 2: Conditional request")
	resp2, err := client.Get(ctx, "/test/endpoint")
	if err != nil {
		t.Fatalf("Request 2 failed: %v", err)
	}
	defer resp2.Body.Close()

	// Should have made second request with conditional headers
	if requestsMade != 2 {
		t.Errorf("After request 2: requestsMade = %d, want 2", requestsMade)
	}

	if conditionalRequests != 1 {
		t.Errorf("conditionalRequests = %d, want 1", conditionalRequests)
	}

	// Verify cache contains the entry
	cacheKey := cache.CacheKey{
		Endpoint: "/test/endpoint",
	}
	cachedEntry, err := client.cache.Get(ctx, cacheKey)
	if err != nil {
		t.Errorf("Cache lookup failed: %v", err)
	}
	if cachedEntry == nil {
		t.Error("Expected cache entry but got nil")
	} else {
		if cachedEntry.ETag != `"test-etag-123"` {
			t.Errorf("Cached ETag = %q, want %q", cachedEntry.ETag, `"test-etag-123"`)
		}
	}
}

func TestIntegration_RateLimitIntegration(t *testing.T) {
	redisClient, cleanup := setupRedisContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Pre-seed Redis with critical rate limit state
	redisClient.Set(ctx, "esi:rate_limit:errors_remaining", 3, 0)
	redisClient.Set(ctx, "esi:rate_limit:reset_timestamp", time.Now().Add(60*time.Second).Unix(), 0)

	cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// This request should be blocked
	req, _ := http.NewRequest("GET", "http://example.com/test", nil)
	_, err = client.Do(req)

	if err == nil {
		t.Error("Expected request to be blocked by rate limiter")
	}

	// Verify rate limiter state
	state, err := client.rateLimiter.GetState(ctx)
	if err != nil {
		t.Fatalf("Failed to get rate limit state: %v", err)
	}

	if state.ErrorsRemaining != 3 {
		t.Errorf("ErrorsRemaining = %d, want 3", state.ErrorsRemaining)
	}

	if !state.NeedsCriticalBlock() {
		t.Error("Expected state to need critical block")
	}
}

func TestIntegration_ErrorClassificationMetrics(t *testing.T) {
	redisClient, cleanup := setupRedisContainer(t)
	defer cleanup()

	testCases := []struct {
		name       string
		statusCode int
		errClass   ErrorClass
	}{
		{"client error", 404, ErrorClassClient},
		{"server error", 500, ErrorClassServer},
		{"rate limit error", 520, ErrorClassRateLimit},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-ESI-Error-Limit-Remain", "100")
				w.Header().Set("X-ESI-Error-Limit-Reset", "60")
				w.WriteHeader(tc.statusCode)
			}))
			defer server.Close()

			cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
			client, err := New(cfg)
			if err != nil {
				t.Fatalf("Failed to create client: %v", err)
			}

			client.httpClient = &http.Client{
				Transport: &testTransport{server: server},
				Timeout:   30 * time.Second,
			}

			req, _ := http.NewRequest("GET", server.URL+"/test", nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer resp.Body.Close()

			errClass := client.classifyError(resp, nil)
			if errClass != tc.errClass {
				t.Errorf("Error class = %q, want %q", errClass, tc.errClass)
			}
		})
	}
}

func TestIntegration_CacheExpiration(t *testing.T) {
	redisClient, cleanup := setupRedisContainer(t)
	defer cleanup()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")
		// Very short expiration
		w.Header().Set("Expires", time.Now().Add(1*time.Second).Format(http.TimeFormat))
		w.Header().Set("ETag", `"short-lived"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"test": "data"}`))
	}))
	defer server.Close()

	cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	client.httpClient = &http.Client{
		Transport: &testTransport{server: server},
		Timeout:   30 * time.Second,
	}

	ctx := context.Background()

	// First request
	resp1, err := client.Get(ctx, "/test")
	if err != nil {
		t.Fatalf("First request failed: %v", err)
	}
	resp1.Body.Close()

	// Verify it's cached
	cacheKey := cache.CacheKey{Endpoint: "/test"}
	entry, err := client.cache.Get(ctx, cacheKey)
	if err != nil {
		t.Fatalf("Cache lookup failed: %v", err)
	}

	if entry.IsExpired() {
		t.Error("Entry should not be expired yet")
	}

	// Wait for expiration
	time.Sleep(2 * time.Second)

	// Check if expired
	entry2, err := client.cache.Get(ctx, cacheKey)
	if err != cache.ErrCacheMiss {
		t.Errorf("Expected cache miss after expiration, got: %v (entry: %v)", err, entry2)
	}
}
