package integration

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Sternrassler/eve-esi-client/internal/testutil"
	"github.com/Sternrassler/eve-esi-client/pkg/cache"
	"github.com/Sternrassler/eve-esi-client/pkg/client"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupRedis creates a Redis container for integration testing.
func setupRedis(t *testing.T) (*redis.Client, func()) {
	t.Helper()

	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start Redis container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("Failed to get container host: %v", err)
	}

	port, err := container.MappedPort(ctx, "6379")
	if err != nil {
		t.Fatalf("Failed to get container port: %v", err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: host + ":" + port.Port(),
	})

	cleanup := func() {
		redisClient.Close()
		container.Terminate(ctx)
	}

	return redisClient, cleanup
}

// testTransport wraps the mock server to redirect requests.
type testTransport struct {
	mockServer *testutil.MockESI
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Redirect to mock server
	req.URL.Scheme = "http"
	if req.URL.Host == "" || req.URL.Host == "esi.evetech.net" {
		mockURL := t.mockServer.URL()
		req.URL.Host = mockURL[7:] // Remove "http://"
	}
	return http.DefaultTransport.RoundTrip(req)
}

// TestFullRequestFlow tests the complete request flow: Rate Limit → Cache → ESI → Cache Update.
func TestFullRequestFlow(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	mockESI := testutil.NewMockESI()
	defer mockESI.Close()

	// Configure market orders endpoint
	mockESI.SetMarketOrdersResponse(10000002, testutil.NewHealthyResponse(`[
		{"order_id": 1, "type_id": 34, "price": 100.50},
		{"order_id": 2, "type_id": 35, "price": 200.75}
	]`))

	// Create client
	cfg := client.DefaultConfig(redisClient, "TestApp/1.0.0 (integration@test.com)")
	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer c.Close()

	// Override HTTP client to use mock
	c.SetHTTPClient(&http.Client{
		Transport: &testTransport{mockServer: mockESI},
		Timeout:   30 * time.Second,
	})

	ctx := context.Background()

	// Request 1: Initial request (Rate Limit Check → Cache Miss → ESI Request → Cache Store)
	t.Log("Request 1: Full flow - cache miss")
	resp1, err := c.Get(ctx, "/v1/markets/10000002/orders/")
	if err != nil {
		t.Fatalf("Request 1 failed: %v", err)
	}
	defer resp1.Body.Close()

	body1, _ := io.ReadAll(resp1.Body)
	t.Logf("Response 1: %s", string(body1))

	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Request 1 status = %d, want %d", resp1.StatusCode, http.StatusOK)
	}

	if mockESI.GetRequestCount() != 1 {
		t.Errorf("After request 1: ESI requests = %d, want 1", mockESI.GetRequestCount())
	}

	// Wait for cache write
	time.Sleep(100 * time.Millisecond)

	// Request 2: Should hit cache and make conditional request
	t.Log("Request 2: Cache hit with conditional request")
	resp2, err := c.Get(ctx, "/v1/markets/10000002/orders/")
	if err != nil {
		t.Fatalf("Request 2 failed: %v", err)
	}
	defer resp2.Body.Close()

	if mockESI.GetRequestCount() != 2 {
		t.Errorf("After request 2: ESI requests = %d, want 2", mockESI.GetRequestCount())
	}

	if mockESI.GetConditionalCount() != 1 {
		t.Errorf("Conditional requests = %d, want 1", mockESI.GetConditionalCount())
	}

	// Verify metrics incremented
	// Metrics are tested separately in metrics tests
}

// TestCacheHit tests that cached responses skip ESI calls.
func TestCacheHit(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	mockESI := testutil.NewMockESI()
	defer mockESI.Close()

	// Configure endpoint
	mockESI.SetResponse("/v1/status/", testutil.NewHealthyResponse(`{"status": "ok"}`))

	cfg := client.DefaultConfig(redisClient, "TestApp/1.0.0")
	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer c.Close()

	c.SetHTTPClient(&http.Client{
		Transport: &testTransport{mockServer: mockESI},
		Timeout:   30 * time.Second,
	})

	ctx := context.Background()

	// First request
	resp1, err := c.Get(ctx, "/v1/status/")
	if err != nil {
		t.Fatalf("First request failed: %v", err)
	}
	resp1.Body.Close()

	initialCount := mockESI.GetRequestCount()
	if initialCount != 1 {
		t.Errorf("Initial ESI requests = %d, want 1", initialCount)
	}

	time.Sleep(50 * time.Millisecond)

	// Second request - should use cache
	resp2, err := c.Get(ctx, "/v1/status/")
	if err != nil {
		t.Fatalf("Second request failed: %v", err)
	}
	resp2.Body.Close()

	// Should have made a conditional request (304 response expected)
	finalCount := mockESI.GetRequestCount()
	if finalCount != 2 {
		t.Errorf("Final ESI requests = %d, want 2 (with conditional)", finalCount)
	}
}

// TestNotModified tests 304 Not Modified responses use cached data.
func TestNotModified(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	mockESI := testutil.NewMockESI()
	defer mockESI.Close()

	etag := `"stable-etag-123"`
	testData := `{"market": "data"}`

	// Configure conditional handler
	mockESI.SetHandler("/v1/markets/10000002/orders/", testutil.NewConditionalHandler(etag, testData))

	cfg := client.DefaultConfig(redisClient, "TestApp/1.0.0")
	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer c.Close()

	c.SetHTTPClient(&http.Client{
		Transport: &testTransport{mockServer: mockESI},
		Timeout:   30 * time.Second,
	})

	ctx := context.Background()

	// First request - get full response
	resp1, err := c.Get(ctx, "/v1/markets/10000002/orders/")
	if err != nil {
		t.Fatalf("First request failed: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()

	if string(body1) != testData {
		t.Errorf("First response body = %s, want %s", string(body1), testData)
	}

	time.Sleep(100 * time.Millisecond)

	// Second request - should get 304 and return cached data
	resp2, err := c.Get(ctx, "/v1/markets/10000002/orders/")
	if err != nil {
		t.Fatalf("Second request failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	// Even though server returned 304, client should return cached body
	if string(body2) != testData {
		t.Errorf("Second response body = %s, want %s (cached)", string(body2), testData)
	}

	if mockESI.GetConditionalCount() != 1 {
		t.Errorf("Conditional requests = %d, want 1", mockESI.GetConditionalCount())
	}
}

// TestRateLimitBlock tests that requests are blocked when rate limit is critical.
func TestRateLimitBlock(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	mockESI := testutil.NewMockESI()
	defer mockESI.Close()

	ctx := context.Background()

	// Pre-seed Redis with critical rate limit state (< 5 errors remaining)
	// Set all required keys as the tracker checks all of them
	redisClient.Set(ctx, "esi:rate_limit:errors_remaining", 3, 0)
	redisClient.Set(ctx, "esi:rate_limit:reset_timestamp", time.Now().Add(60*time.Second).Unix(), 0)
	redisClient.Set(ctx, "esi:rate_limit:last_update", time.Now().Format(time.RFC3339), 0)

	// Small delay to ensure Redis persistence
	time.Sleep(50 * time.Millisecond)

	cfg := client.DefaultConfig(redisClient, "TestApp/1.0.0")
	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer c.Close()

	c.SetHTTPClient(&http.Client{
		Transport: &testTransport{mockServer: mockESI},
		Timeout:   30 * time.Second,
	})

	// This request should be blocked
	_, err = c.Get(ctx, "/v1/status/")
	if err == nil {
		t.Error("Expected request to be blocked by rate limiter, but it succeeded")
	}

	// Verify no request was made to ESI
	if mockESI.GetRequestCount() != 0 {
		t.Errorf("ESI requests = %d, want 0 (blocked)", mockESI.GetRequestCount())
	}
}

// TestRetry5xxErrors tests that 5xx errors trigger retries.
func TestRetry5xxErrors(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	mockESI := testutil.NewMockESI()
	defer mockESI.Close()

	requestCount := 0
	mockESI.SetHandler("/v1/status/", func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		w.Header().Set("X-ESI-Error-Limit-Remain", "95")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")

		// First 2 attempts fail with 500
		if requestCount <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "server error"}`))
			return
		}

		// Third attempt succeeds
		w.Header().Set("ETag", `"success"`)
		w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	})

	cfg := client.DefaultConfig(redisClient, "TestApp/1.0.0")
	cfg.MaxRetries = 3
	cfg.InitialBackoff = 100 * time.Millisecond // Speed up test

	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer c.Close()

	c.SetHTTPClient(&http.Client{
		Transport: &testTransport{mockServer: mockESI},
		Timeout:   30 * time.Second,
	})

	ctx := context.Background()

	// Should retry and eventually succeed
	resp, err := c.Get(ctx, "/v1/status/")
	if err != nil {
		t.Fatalf("Request failed after retries: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Final status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if requestCount != 3 {
		t.Errorf("Request attempts = %d, want 3 (2 retries + 1 success)", requestCount)
	}
}

// TestNoRetry4xxErrors tests that 4xx errors do NOT trigger retries.
func TestNoRetry4xxErrors(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	mockESI := testutil.NewMockESI()
	defer mockESI.Close()

	mockESI.SetHandler("/v1/invalid/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-ESI-Error-Limit-Remain", "95")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "not found"}`))
	})

	cfg := client.DefaultConfig(redisClient, "TestApp/1.0.0")
	cfg.MaxRetries = 3

	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer c.Close()

	c.SetHTTPClient(&http.Client{
		Transport: &testTransport{mockServer: mockESI},
		Timeout:   30 * time.Second,
	})

	ctx := context.Background()

	// Should NOT retry 4xx errors
	resp, err := c.Get(ctx, "/v1/invalid/")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	// Should only make 1 request (no retries)
	if mockESI.GetRequestCount() != 1 {
		t.Errorf("ESI requests = %d, want 1 (no retries for 4xx)", mockESI.GetRequestCount())
	}
}

// TestMetricsIncremented tests that metrics are correctly incremented.
func TestMetricsIncremented(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	mockESI := testutil.NewMockESI()
	defer mockESI.Close()

	mockESI.SetResponse("/v1/status/", testutil.NewHealthyResponse(`{"status": "ok"}`))

	cfg := client.DefaultConfig(redisClient, "TestApp/1.0.0")
	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer c.Close()

	c.SetHTTPClient(&http.Client{
		Transport: &testTransport{mockServer: mockESI},
		Timeout:   30 * time.Second,
	})

	ctx := context.Background()

	// Make a successful request
	resp, err := c.Get(ctx, "/v1/status/")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()

	// Verify the request was made
	if mockESI.GetRequestCount() != 1 {
		t.Errorf("ESI requests = %d, want 1", mockESI.GetRequestCount())
	}

	// Note: Detailed metrics verification is done in metrics package tests
	// Here we just verify the integration works end-to-end
}

// TestCacheExpiration tests that expired cache entries are not used.
func TestCacheExpiration(t *testing.T) {
	redisClient, cleanup := setupRedis(t)
	defer cleanup()

	mockESI := testutil.NewMockESI()
	defer mockESI.Close()

	// Configure short expiration
	mockESI.SetHandler("/v1/status/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")
		w.Header().Set("ETag", `"short-lived"`)
		w.Header().Set("Expires", time.Now().Add(1*time.Second).Format(http.TimeFormat))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	})

	cfg := client.DefaultConfig(redisClient, "TestApp/1.0.0")
	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer c.Close()

	c.SetHTTPClient(&http.Client{
		Transport: &testTransport{mockServer: mockESI},
		Timeout:   30 * time.Second,
	})

	ctx := context.Background()

	// First request - cache entry with 1s TTL
	resp1, err := c.Get(ctx, "/v1/status/")
	if err != nil {
		t.Fatalf("First request failed: %v", err)
	}
	resp1.Body.Close()

	time.Sleep(100 * time.Millisecond)

	// Verify it's cached
	cacheKey := cache.CacheKey{Endpoint: "/v1/status/"}
	entry, err := c.GetCache().Get(ctx, cacheKey)
	if err != nil {
		t.Fatalf("Cache lookup failed: %v", err)
	}
	if entry.IsExpired() {
		t.Error("Entry should not be expired yet")
	}

	// Wait for expiration
	time.Sleep(2 * time.Second)

	// Check if expired - cache should return miss
	entry2, err := c.GetCache().Get(ctx, cacheKey)
	if err != cache.ErrCacheMiss {
		t.Logf("Entry after expiration: %+v", entry2)
		t.Errorf("Expected cache miss after expiration, got: %v", err)
	}

	// Third request should hit ESI again (not use expired cache)
	resp3, err := c.Get(ctx, "/v1/status/")
	if err != nil {
		t.Fatalf("Third request failed: %v", err)
	}
	resp3.Body.Close()

	// Should have made at least 2 requests to ESI
	if mockESI.GetRequestCount() < 2 {
		t.Errorf("ESI requests = %d, want >= 2 (cache expired)", mockESI.GetRequestCount())
	}
}
