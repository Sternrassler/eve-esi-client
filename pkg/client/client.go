// Package client provides the core ESI HTTP client with rate limiting,
// caching, and error handling.
package client

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Sternrassler/eve-esi-client/pkg/cache"
	"github.com/Sternrassler/eve-esi-client/pkg/ratelimit"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Prometheus metrics for ESI client operations.
var (
	esiRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "esi_requests_total",
		Help: "Total ESI requests by endpoint and status",
	}, []string{"endpoint", "status"})

	esiRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "esi_request_duration_seconds",
		Help:    "ESI request duration in seconds by endpoint",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10},
	}, []string{"endpoint"})

	esiErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "esi_errors_total",
		Help: "Total ESI errors by class",
	}, []string{"class"})

	esiRetriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "esi_retries_total",
		Help: "Total number of retry attempts by error class",
	}, []string{"error_class"})

	esiRetryBackoffSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "esi_retry_backoff_seconds",
		Help:    "Backoff duration for retries by error class",
		Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60},
	}, []string{"error_class"})

	esiRetryExhaustedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "esi_retry_exhausted_total",
		Help: "Total number of times retry attempts were exhausted by error class",
	}, []string{"error_class"})
)

// ErrorClass represents a classification of HTTP errors.
type ErrorClass string

const (
	// ErrorClassClient represents 4xx client errors.
	ErrorClassClient ErrorClass = "client"

	// ErrorClassServer represents 5xx server errors.
	ErrorClassServer ErrorClass = "server"

	// ErrorClassRateLimit represents 520 rate limit errors.
	ErrorClassRateLimit ErrorClass = "rate_limit"

	// ErrorClassNetwork represents network/timeout errors.
	ErrorClassNetwork ErrorClass = "network"
)

// Client is the main ESI client.
type Client struct {
	httpClient  *http.Client
	redis       *redis.Client
	rateLimiter *ratelimit.Tracker
	cache       *cache.Manager
	config      Config
	logger      zerolog.Logger
}

// Config holds the client configuration.
type Config struct {
	// Redis client for caching and rate limit state
	Redis *redis.Client

	// User-Agent header (REQUIRED by ESI)
	// Format: "AppName/Version (contact@example.com)"
	UserAgent string

	// Rate Limiting
	RateLimit      int // Requests per second
	ErrorThreshold int // Stop requests when errors remaining < threshold

	// Concurrency
	MaxConcurrency int // Max parallel requests

	// Caching
	MemoryCacheTTL time.Duration // In-memory cache TTL
	RespectExpires bool          // Honor ESI expires header (MUST be true)

	// Retry
	MaxRetries     int
	InitialBackoff time.Duration
}

// DefaultConfig returns a safe default configuration.
func DefaultConfig(redis *redis.Client, userAgent string) Config {
	return Config{
		Redis:          redis,
		UserAgent:      userAgent,
		RateLimit:      10,
		ErrorThreshold: 10,
		MaxConcurrency: 5,
		MemoryCacheTTL: 60 * time.Second,
		RespectExpires: true, // MUST be true for ESI compliance
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
	}
}

// New creates a new ESI client.
func New(cfg Config) (*Client, error) {
	if cfg.Redis == nil {
		return nil, fmt.Errorf("redis client is required")
	}

	if cfg.UserAgent == "" {
		return nil, fmt.Errorf("user-agent is required")
	}

	if !cfg.RespectExpires {
		return nil, fmt.Errorf("respect_expires must be true (ESI requirement)")
	}

	if cfg.ErrorThreshold < 5 {
		return nil, fmt.Errorf("error_threshold must be >= 5 (got %d)", cfg.ErrorThreshold)
	}

	// Initialize logger
	logger := log.With().Str("component", "esi-client").Logger()

	// Create rate limit tracker
	rateLimiter := ratelimit.NewTracker(cfg.Redis, logger)

	// Create cache manager
	cacheManager := cache.NewManager(cfg.Redis)

	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		redis:       cfg.Redis,
		rateLimiter: rateLimiter,
		cache:       cacheManager,
		config:      cfg,
		logger:      logger,
	}, nil
}

// Do performs an HTTP request with rate limiting, caching, and error handling.
// This is the core request method that orchestrates all ESI client features.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	endpoint := req.URL.Path

	// Start request timing
	startTime := time.Now()
	defer func() {
		esiRequestDuration.WithLabelValues(endpoint).Observe(time.Since(startTime).Seconds())
	}()

	// Step 1: Check Rate Limit
	allowed, err := c.rateLimiter.ShouldAllowRequest(ctx)
	if err != nil {
		c.logger.Error().Err(err).Msg("Rate limit check failed")
		return nil, fmt.Errorf("rate limit check: %w", err)
	}
	if !allowed {
		c.logger.Warn().
			Str("endpoint", endpoint).
			Msg("Request blocked by rate limiter")
		esiRequestsTotal.WithLabelValues(endpoint, "rate_limited").Inc()
		return nil, fmt.Errorf("request blocked: rate limit critical")
	}

	// Step 2: Check Cache
	cacheKey := cache.CacheKey{
		Endpoint:    endpoint,
		QueryParams: req.URL.Query(),
	}

	cachedEntry, err := c.cache.Get(ctx, cacheKey)
	if err != nil && err != cache.ErrCacheMiss {
		c.logger.Warn().Err(err).Str("endpoint", endpoint).Msg("Cache get error")
	}

	// Step 3: Make Conditional Request if cache hit
	if cachedEntry != nil && cache.ShouldMakeConditionalRequest(cachedEntry) {
		cache.AddConditionalHeaders(req, cachedEntry)
		cache.ConditionalRequestsSent.Inc()
		c.logger.Debug().
			Str("endpoint", endpoint).
			Str("etag", cachedEntry.ETag).
			Msg("Making conditional request")
	}

	// Step 4: Set User-Agent header
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "application/json")

	// Step 5: Execute HTTP Request with Retry Logic
	c.logger.Debug().
		Str("endpoint", endpoint).
		Str("method", req.Method).
		Msg("Executing ESI request")

	var resp *http.Response
	var lastErr error
	var errClass ErrorClass

	// Wrap the HTTP request in retry logic
	retryErr := retryWithBackoff(ctx, func() error {
		// Execute the HTTP request
		var reqErr error
		resp, reqErr = c.httpClient.Do(req)

		// Handle network errors
		if reqErr != nil {
			c.logger.Error().Err(reqErr).Str("endpoint", endpoint).Msg("HTTP request failed")
			errClass = c.classifyError(nil, reqErr)
			esiErrorsTotal.WithLabelValues(string(errClass)).Inc()
			esiRequestsTotal.WithLabelValues(endpoint, "network_error").Inc()
			lastErr = reqErr
			return reqErr
		}

		// Update Rate Limit from headers
		if err := c.rateLimiter.UpdateFromHeaders(ctx, resp.Header); err != nil {
			c.logger.Warn().Err(err).Msg("Failed to update rate limit from headers")
		}

		// Handle 304 Not Modified (not an error, return success)
		if resp.StatusCode == http.StatusNotModified {
			return nil
		}

		// Handle HTTP errors
		if resp.StatusCode >= 400 {
			errClass = c.classifyError(resp, nil)
			esiErrorsTotal.WithLabelValues(string(errClass)).Inc()
			esiRequestsTotal.WithLabelValues(endpoint, fmt.Sprintf("%d", resp.StatusCode)).Inc()

			c.logger.Warn().
				Str("endpoint", endpoint).
				Int("status", resp.StatusCode).
				Str("error_class", string(errClass)).
				Msg("ESI request error")

			// Check if we should retry this error
			if shouldRetry(errClass) {
				// Build error for retriable errors (server, rate_limit, network)
				lastErr = &ESIError{
					StatusCode: resp.StatusCode,
					ErrorClass: errClass,
					Message:    resp.Status,
				}
				resp.Body.Close() // Close the body before retrying
				return lastErr
			}

			// Don't retry client errors - return success (let caller handle status)
			return nil
		}

		// Success
		esiRequestsTotal.WithLabelValues(endpoint, fmt.Sprintf("%d", resp.StatusCode)).Inc()
		return nil
	}, func(err error) ErrorClass {
		// Classify error dynamically for retry logic
		return errClass
	})

	// Handle retry exhaustion
	if retryErr != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return nil, retryErr
	}

	// Step 7: Handle 304 Not Modified
	if resp.StatusCode == http.StatusNotModified {
		c.logger.Debug().Str("endpoint", endpoint).Msg("304 Not Modified - using cache")
		esiRequestsTotal.WithLabelValues(endpoint, "304").Inc()
		cache.NotModifiedResponses.Inc()

		// Update cache TTL from new expires header
		if expiresStr := resp.Header.Get("Expires"); expiresStr != "" {
			if newExpires, err := http.ParseTime(expiresStr); err == nil {
				if err := c.cache.UpdateTTL(ctx, cacheKey, newExpires); err != nil {
					c.logger.Warn().Err(err).Msg("Failed to update cache TTL")
				}
			}
		}

		// Return cached response
		resp.Body.Close()
		return c.cacheEntryToResponse(cachedEntry), nil
	}

	// Step 8: Update Cache on success
	if resp.StatusCode == http.StatusOK {
		entry, err := cache.ResponseToEntry(resp)
		if err != nil {
			c.logger.Warn().Err(err).Msg("Failed to create cache entry")
		} else if entry.TTL() > 0 {
			if err := c.cache.Set(ctx, cacheKey, entry); err != nil {
				c.logger.Warn().Err(err).Msg("Failed to cache response")
			} else {
				c.logger.Debug().
					Str("endpoint", endpoint).
					Dur("ttl", entry.TTL()).
					Msg("Cached response")
			}
		}
	}

	return resp, nil
}

// classifyError categorizes an error for observability and handling.
func (c *Client) classifyError(resp *http.Response, err error) ErrorClass {
	if err != nil {
		c.logger.Debug().Str("class", string(ErrorClassNetwork)).Msg("Error classified")
		return ErrorClassNetwork
	}

	switch {
	case resp.StatusCode == 520:
		c.logger.Debug().Str("class", string(ErrorClassRateLimit)).Msg("Error classified")
		return ErrorClassRateLimit
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		c.logger.Debug().Str("class", string(ErrorClassClient)).Msg("Error classified")
		return ErrorClassClient
	case resp.StatusCode >= 500:
		c.logger.Debug().Str("class", string(ErrorClassServer)).Msg("Error classified")
		return ErrorClassServer
	default:
		return ""
	}
}

// cacheEntryToResponse converts a cache entry back to an HTTP response.
func (c *Client) cacheEntryToResponse(entry *cache.CacheEntry) *http.Response {
	return cache.EntryToResponse(entry)
}

// Get performs a GET request to an ESI endpoint.
func (c *Client) Get(ctx context.Context, endpoint string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://esi.evetech.net"+endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	return c.Do(req)
}

// Close closes the client and releases resources.
func (c *Client) Close() error {
	// TODO: Cleanup resources
	return nil
}

// SetHTTPClient sets a custom HTTP client (for testing).
func (c *Client) SetHTTPClient(client *http.Client) {
	c.httpClient = client
}

// GetCache returns the cache manager (for testing).
func (c *Client) GetCache() *cache.Manager {
	return c.cache
}
