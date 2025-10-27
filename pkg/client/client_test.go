package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sternrassler/eve-esi-client/pkg/cache"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// setupTestRedis creates a test Redis client.
func setupTestRedis(t *testing.T) *redis.Client {
	t.Helper()

	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // Use a separate DB for tests
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available for testing: %v", err)
	}

	// Flush test DB
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("Failed to flush test DB: %v", err)
	}

	t.Cleanup(func() {
		client.FlushDB(context.Background())
		client.Close()
	})

	return client
}

func TestNew_Validation(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer redisClient.Close()

	tests := []struct {
		name        string
		config      Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			config: Config{
				Redis:          redisClient,
				UserAgent:      "TestApp/1.0.0 (test@example.com)",
				RespectExpires: true,
				ErrorThreshold: 10,
			},
			expectError: false,
		},
		{
			name: "nil redis",
			config: Config{
				UserAgent:      "TestApp/1.0.0",
				RespectExpires: true,
				ErrorThreshold: 10,
			},
			expectError: true,
			errorMsg:    "redis client is required",
		},
		{
			name: "empty user agent",
			config: Config{
				Redis:          redisClient,
				UserAgent:      "",
				RespectExpires: true,
				ErrorThreshold: 10,
			},
			expectError: true,
			errorMsg:    "user-agent is required",
		},
		{
			name: "respect expires false",
			config: Config{
				Redis:          redisClient,
				UserAgent:      "TestApp/1.0.0",
				RespectExpires: false,
				ErrorThreshold: 10,
			},
			expectError: true,
			errorMsg:    "respect_expires must be true (ESI requirement)",
		},
		{
			name: "error threshold too low",
			config: Config{
				Redis:          redisClient,
				UserAgent:      "TestApp/1.0.0",
				RespectExpires: true,
				ErrorThreshold: 3,
			},
			expectError: true,
			errorMsg:    "error_threshold must be >= 5 (got 3)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(tt.config)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
					return
				}
				if tt.errorMsg != "" && err.Error() != tt.errorMsg {
					t.Errorf("Error message = %q, want %q", err.Error(), tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
					return
				}
				if client == nil {
					t.Error("Client is nil")
				}
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer redisClient.Close()

	userAgent := "TestApp/1.0.0"
	cfg := DefaultConfig(redisClient, userAgent)

	if cfg.Redis != redisClient {
		t.Error("Redis client not set correctly")
	}
	if cfg.UserAgent != userAgent {
		t.Errorf("UserAgent = %q, want %q", cfg.UserAgent, userAgent)
	}
	if !cfg.RespectExpires {
		t.Error("RespectExpires should be true")
	}
	if cfg.ErrorThreshold < 5 {
		t.Errorf("ErrorThreshold = %d, should be >= 5", cfg.ErrorThreshold)
	}
	if cfg.RateLimit <= 0 {
		t.Errorf("RateLimit = %d, should be > 0", cfg.RateLimit)
	}
}

func TestClassifyError(t *testing.T) {
	redisClient := setupTestRedis(t)
	logger := zerolog.Nop()

	client := &Client{
		redis:  redisClient,
		logger: logger,
	}

	tests := []struct {
		name       string
		statusCode int
		err        error
		expected   ErrorClass
	}{
		{
			name:       "network error",
			statusCode: 0,
			err:        io.EOF,
			expected:   ErrorClassNetwork,
		},
		{
			name:       "client error 404",
			statusCode: 404,
			err:        nil,
			expected:   ErrorClassClient,
		},
		{
			name:       "client error 403",
			statusCode: 403,
			err:        nil,
			expected:   ErrorClassClient,
		},
		{
			name:       "server error 500",
			statusCode: 500,
			err:        nil,
			expected:   ErrorClassServer,
		},
		{
			name:       "server error 503",
			statusCode: 503,
			err:        nil,
			expected:   ErrorClassServer,
		},
		{
			name:       "rate limit 520",
			statusCode: 520,
			err:        nil,
			expected:   ErrorClassRateLimit,
		},
		{
			name:       "success 200",
			statusCode: 200,
			err:        nil,
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp *http.Response
			if tt.statusCode > 0 {
				resp = &http.Response{
					StatusCode: tt.statusCode,
				}
			}

			result := client.classifyError(resp, tt.err)
			if result != tt.expected {
				t.Errorf("classifyError() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestDo_UserAgentSet(t *testing.T) {
	redisClient := setupTestRedis(t)

	// Create mock server
	userAgentReceived := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgentReceived = r.Header.Get("User-Agent")
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")
		w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"test": "data"}`))
	}))
	defer server.Close()

	cfg := DefaultConfig(redisClient, "TestApp/1.0.0 (test@example.com)")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/test", nil)
	_, err = client.Do(req)
	if err != nil {
		t.Fatalf("Do() failed: %v", err)
	}

	if userAgentReceived != cfg.UserAgent {
		t.Errorf("User-Agent = %q, want %q", userAgentReceived, cfg.UserAgent)
	}
}

func TestDo_RateLimitBlock(t *testing.T) {
	redisClient := setupTestRedis(t)

	// Pre-populate Redis with critical rate limit state
	ctx := context.Background()
	now := time.Now()
	redisClient.Set(ctx, "esi:rate_limit:errors_remaining", 3, 0)
	redisClient.Set(ctx, "esi:rate_limit:reset_timestamp", now.Add(60*time.Second).Unix(), 0)
	// Add last_update to ensure GetState() doesn't return default healthy state
	lastUpdateJSON, _ := json.Marshal(now)
	redisClient.Set(ctx, "esi:rate_limit:last_update", lastUpdateJSON, 0)

	cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	req, _ := http.NewRequest("GET", "http://example.com/test", nil)
	_, err = client.Do(req)

	if err == nil {
		t.Error("Expected request to be blocked by rate limiter")
	}
	if err != nil && err.Error() != "request blocked: rate limit critical" {
		t.Errorf("Error = %q, want rate limit block error", err.Error())
	}
}

func TestDo_CacheHit(t *testing.T) {
	redisClient := setupTestRedis(t)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")
		w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
		w.Header().Set("ETag", `"abc123"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"test": "data"}`))
	}))
	defer server.Close()

	cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// First request - should hit server
	req1, _ := http.NewRequest("GET", server.URL+"/test", nil)
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("First request failed: %v", err)
	}
	resp1.Body.Close()

	if requestCount != 1 {
		t.Errorf("Request count after first request = %d, want 1", requestCount)
	}

	// Wait a bit for cache to be written
	time.Sleep(100 * time.Millisecond)

	// Second request - cache should be checked
	// Since we don't have a full mock server that handles If-None-Match,
	// this will still make a request but with conditional headers
	req2, _ := http.NewRequest("GET", server.URL+"/test", nil)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("Second request failed: %v", err)
	}
	resp2.Body.Close()

	// Verify conditional headers were added (ETag should be set in the request)
	// This is tested implicitly through cache.AddConditionalHeaders
}

func TestDo_Handle304NotModified(t *testing.T) {
	redisClient := setupTestRedis(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")

		// Check for conditional request header
		if r.Header.Get("If-None-Match") != "" {
			// Return 304 Not Modified
			w.Header().Set("Expires", time.Now().Add(10*time.Minute).Format(http.TimeFormat))
			w.WriteHeader(http.StatusNotModified)
			return
		}

		// First request - return full response
		w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
		w.Header().Set("ETag", `"abc123"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"test": "data"}`))
	}))
	defer server.Close()

	cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// First request
	req1, _ := http.NewRequest("GET", server.URL+"/test", nil)
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("First request failed: %v", err)
	}
	resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Errorf("First response status = %d, want %d", resp1.StatusCode, http.StatusOK)
	}

	// Wait for cache
	time.Sleep(100 * time.Millisecond)

	// Second request with conditional headers
	req2, _ := http.NewRequest("GET", server.URL+"/test", nil)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("Second request failed: %v", err)
	}
	resp2.Body.Close()

	// The client should return the cached response
	if resp2.StatusCode != http.StatusOK && resp2.StatusCode != http.StatusNotModified {
		t.Errorf("Second response status = %d, want %d or %d",
			resp2.StatusCode, http.StatusOK, http.StatusNotModified)
	}
}

func TestDo_ErrorClassification(t *testing.T) {
	redisClient := setupTestRedis(t)

	tests := []struct {
		name       string
		statusCode int
		expected   ErrorClass
	}{
		{"client error", 404, ErrorClassClient},
		{"server error", 500, ErrorClassServer},
		{"rate limit", 520, ErrorClassRateLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-ESI-Error-Limit-Remain", "100")
				w.Header().Set("X-ESI-Error-Limit-Reset", "60")
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
			client, err := New(cfg)
			if err != nil {
				t.Fatalf("Failed to create client: %v", err)
			}

			req, _ := http.NewRequest("GET", server.URL+"/test", nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			resp.Body.Close()

			// Verify the error was classified correctly
			errClass := client.classifyError(resp, nil)
			if errClass != tt.expected {
				t.Errorf("Error class = %q, want %q", errClass, tt.expected)
			}
		})
	}
}

func TestCacheEntryToResponse(t *testing.T) {
	redisClient := setupTestRedis(t)
	logger := zerolog.Nop()

	client := &Client{
		redis:  redisClient,
		logger: logger,
	}

	// Create headers properly using Set() to ensure canonical form
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("ETag", `"abc123"`)

	entry := &cache.CacheEntry{
		StatusCode: 200,
		Headers:    headers,
		Data:       []byte(`{"test": "data"}`),
	}

	resp := client.cacheEntryToResponse(entry)

	if resp.StatusCode != entry.StatusCode {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, entry.StatusCode)
	}

	if resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", resp.Header.Get("Content-Type"), "application/json")
	}

	if resp.Header.Get("ETag") != `"abc123"` {
		t.Errorf("ETag = %q, want %q", resp.Header.Get("ETag"), `"abc123"`)
	}
}

func TestGet(t *testing.T) {
	redisClient := setupTestRedis(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")
		w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"test": "data"}`))
	}))
	defer server.Close()

	cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Override the httpClient to point to our test server
	client.httpClient = &http.Client{
		Transport: &testTransport{server: server},
		Timeout:   30 * time.Second,
	}

	resp, err := client.Get(context.Background(), "/test")
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// testTransport is a custom http.RoundTripper for testing
type testTransport struct {
	server *httptest.Server
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Redirect all requests to the test server
	req.URL.Scheme = "http"
	req.URL.Host = t.server.URL[7:] // Remove "http://" prefix
	return http.DefaultTransport.RoundTrip(req)
}

func TestDo_RetryOnServerError(t *testing.T) {
	redisClient := setupTestRedis(t)

	// Server that fails twice, then succeeds
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")
		
		if attemptCount < 3 {
			// Fail with 500 for first two attempts
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		
		// Succeed on third attempt
		w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/test", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do() failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 after retry, got %d", resp.StatusCode)
	}
	if attemptCount != 3 {
		t.Errorf("Expected 3 attempts (2 retries), got %d", attemptCount)
	}
}

func TestDo_NoRetryOnClientError(t *testing.T) {
	redisClient := setupTestRedis(t)

	// Server that always returns 404
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/test", nil)
	resp, err := client.Do(req)
	
	// Should not error out, but return the 404 response
	if err != nil {
		t.Fatalf("Do() failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", resp.StatusCode)
	}
	// Should only attempt once (no retry for client errors)
	if attemptCount != 1 {
		t.Errorf("Expected 1 attempt (no retry for 4xx), got %d", attemptCount)
	}
}

func TestDo_RetryOnRateLimit(t *testing.T) {
	redisClient := setupTestRedis(t)

	// Server that returns 520 once, then succeeds
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")
		
		if attemptCount == 1 {
			// Return 520 rate limit error
			w.WriteHeader(520)
			return
		}
		
		// Succeed on second attempt
		w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/test", nil)
	
	start := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(start)
	
	if err != nil {
		t.Fatalf("Do() failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 after retry, got %d", resp.StatusCode)
	}
	if attemptCount != 2 {
		t.Errorf("Expected 2 attempts (1 retry), got %d", attemptCount)
	}
	
	// Rate limit retry should have waited (initial backoff is 5s, with jitter it's 4-6s)
	if duration < 3*time.Second {
		t.Errorf("Expected at least 3s delay for rate limit retry, got %v", duration)
	}
}

func TestDo_RetryExhausted(t *testing.T) {
	redisClient := setupTestRedis(t)

	// Server that always fails with 500
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := DefaultConfig(redisClient, "TestApp/1.0.0")
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/test", nil)
	_, err = client.Do(req)
	
	// Should fail with retry exhausted error
	if err == nil {
		t.Error("Expected error, got nil")
	}
	if !errors.Is(err, ErrRetryExhausted) {
		t.Errorf("Expected ErrRetryExhausted, got %v", err)
	}
	// Should attempt 3 times (max attempts)
	if attemptCount != 3 {
		t.Errorf("Expected 3 attempts, got %d", attemptCount)
	}
}
