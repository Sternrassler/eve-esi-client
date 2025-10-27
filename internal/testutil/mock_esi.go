// Package testutil provides testing utilities for EVE ESI client.
package testutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// MockESIResponse defines the behavior for a mock ESI endpoint response.
type MockESIResponse struct {
	StatusCode int
	Body       string
	Headers    map[string]string
	Delay      time.Duration
}

// MockESI is a configurable mock ESI server for testing.
type MockESI struct {
	server   *httptest.Server
	mu       sync.RWMutex
	handlers map[string]func(w http.ResponseWriter, r *http.Request)
	
	// Tracking
	RequestCount      int
	ConditionalCount  int
	LastRequestHeader http.Header
}

// NewMockESI creates a new mock ESI server.
func NewMockESI() *MockESI {
	mock := &MockESI{
		handlers: make(map[string]func(w http.ResponseWriter, r *http.Request)),
	}

	mock.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		mock.RequestCount++
		mock.LastRequestHeader = r.Header.Clone()
		
		// Track conditional requests
		if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
			mock.ConditionalCount++
		}
		mock.mu.Unlock()

		// Check for custom handler
		mock.mu.RLock()
		handler, exists := mock.handlers[r.URL.Path]
		mock.mu.RUnlock()

		if exists {
			handler(w, r)
			return
		}

		// Default handler
		mock.defaultHandler(w, r)
	}))

	return mock
}

// URL returns the mock server URL.
func (m *MockESI) URL() string {
	return m.server.URL
}

// Close shuts down the mock server.
func (m *MockESI) Close() {
	m.server.Close()
}

// Reset clears all tracking counters.
func (m *MockESI) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RequestCount = 0
	m.ConditionalCount = 0
	m.LastRequestHeader = nil
}

// SetHandler sets a custom handler for a specific path.
func (m *MockESI) SetHandler(path string, handler func(w http.ResponseWriter, r *http.Request)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[path] = handler
}

// SetResponse configures a simple response for a path.
func (m *MockESI) SetResponse(path string, resp MockESIResponse) {
	m.SetHandler(path, func(w http.ResponseWriter, r *http.Request) {
		// Add delay if specified
		if resp.Delay > 0 {
			time.Sleep(resp.Delay)
		}

		// Set headers
		for key, value := range resp.Headers {
			w.Header().Set(key, value)
		}

		// Write status and body
		w.WriteHeader(resp.StatusCode)
		if resp.Body != "" {
			w.Write([]byte(resp.Body))
		}
	})
}

// SetMarketOrdersResponse configures a typical market orders endpoint response.
func (m *MockESI) SetMarketOrdersResponse(regionID int, resp MockESIResponse) {
	path := fmt.Sprintf("/v1/markets/%d/orders/", regionID)
	m.SetResponse(path, resp)
}

// GetRequestCount returns the number of requests made to the server.
func (m *MockESI) GetRequestCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.RequestCount
}

// GetConditionalCount returns the number of conditional requests.
func (m *MockESI) GetConditionalCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ConditionalCount
}

// defaultHandler provides default ESI-like responses.
func (m *MockESI) defaultHandler(w http.ResponseWriter, r *http.Request) {
	// Set default ESI headers
	w.Header().Set("X-ESI-Error-Limit-Remain", "100")
	w.Header().Set("X-ESI-Error-Limit-Reset", "60")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// Handle conditional requests
	if r.Header.Get("If-None-Match") != "" {
		w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// Default 200 OK response
	w.Header().Set("ETag", `"default-etag"`)
	w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "ok"}`))
}

// NewHealthyResponse creates a standard 200 OK response with ESI headers.
func NewHealthyResponse(data string) MockESIResponse {
	return MockESIResponse{
		StatusCode: http.StatusOK,
		Body:       data,
		Headers: map[string]string{
			"X-ESI-Error-Limit-Remain": "100",
			"X-ESI-Error-Limit-Reset":  "60",
			"ETag":                     `"test-etag-123"`,
			"Expires":                  time.Now().Add(5 * time.Minute).Format(http.TimeFormat),
			"Content-Type":             "application/json; charset=utf-8",
		},
	}
}

// NewNotModifiedResponse creates a 304 Not Modified response.
func NewNotModifiedResponse() MockESIResponse {
	return MockESIResponse{
		StatusCode: http.StatusNotModified,
		Headers: map[string]string{
			"X-ESI-Error-Limit-Remain": "100",
			"X-ESI-Error-Limit-Reset":  "60",
			"Expires":                  time.Now().Add(5 * time.Minute).Format(http.TimeFormat),
		},
	}
}

// NewRateLimitResponse creates a 429 Too Many Requests response.
func NewRateLimitResponse() MockESIResponse {
	return MockESIResponse{
		StatusCode: http.StatusTooManyRequests,
		Body:       `{"error": "Rate limit exceeded"}`,
		Headers: map[string]string{
			"X-ESI-Error-Limit-Remain": "5",
			"X-ESI-Error-Limit-Reset":  "30",
			"Content-Type":             "application/json; charset=utf-8",
		},
	}
}

// NewServerErrorResponse creates a 500 Internal Server Error response.
func NewServerErrorResponse() MockESIResponse {
	return MockESIResponse{
		StatusCode: http.StatusInternalServerError,
		Body:       `{"error": "Internal server error"}`,
		Headers: map[string]string{
			"X-ESI-Error-Limit-Remain": "95",
			"X-ESI-Error-Limit-Reset":  "60",
			"Content-Type":             "application/json; charset=utf-8",
		},
	}
}

// NewESIRateLimitResponse creates a 520 ESI-specific rate limit response.
func NewESIRateLimitResponse() MockESIResponse {
	return MockESIResponse{
		StatusCode: 520, // ESI-specific rate limit
		Body:       `{"error": "ESI rate limit exceeded"}`,
		Headers: map[string]string{
			"X-ESI-Error-Limit-Remain": "10",
			"X-ESI-Error-Limit-Reset":  "120",
			"Content-Type":             "application/json; charset=utf-8",
		},
	}
}

// NewConditionalHandler creates a handler that responds with 304 for conditional requests.
func NewConditionalHandler(etag string, data string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-ESI-Error-Limit-Remain", "100")
		w.Header().Set("X-ESI-Error-Limit-Reset", "60")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		// Check If-None-Match header
		if r.Header.Get("If-None-Match") == etag {
			w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
			w.WriteHeader(http.StatusNotModified)
			return
		}

		// Full response
		w.Header().Set("ETag", etag)
		w.Header().Set("Expires", time.Now().Add(5*time.Minute).Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(data))
	}
}
