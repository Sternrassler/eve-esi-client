// Package client provides the core ESI HTTP client with rate limiting,
// caching, and error handling.
package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sternrassler/eve-esi-client/pkg/cache"
	"github.com/Sternrassler/eve-esi-client/pkg/ratelimit"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

// ErrorClass represents a classification of HTTP errors.
type ErrorClass string

const (
	// ErrorClassClient represents 4xx client errors.
	ErrorClassClient ErrorClass = "client"

	// ErrorClassServer represents 5xx server errors.
	ErrorClassServer ErrorClass = "server"

	// ErrorClassRateLimit represents rate limit errors (429 group rate limit, 520).
	ErrorClassRateLimit ErrorClass = "rate_limit"

	// ErrorClassNetwork represents network/timeout errors.
	ErrorClassNetwork ErrorClass = "network"
)

// Client is the main ESI client.
type Client struct {
	httpClient   *http.Client
	redis        *redis.Client
	rateLimiter  *ratelimit.Tracker      // Legacy-Error-Limit (X-ESI-Error-Limit-*, nicht migrierte Routen)
	groupLimiter *ratelimit.GroupTracker // Gruppen-Rate-Limit (X-Ratelimit-*, neue Routen)
	cache        *cache.Manager
	config       Config
	logger       zerolog.Logger
	limiter      *rate.Limiter // req/s Token-Bucket (in-memory)
	sem          chan struct{} // Concurrency-Semaphore (in-memory)
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

	if cfg.RateLimit <= 0 {
		return nil, fmt.Errorf("rate_limit must be > 0 (got %d)", cfg.RateLimit)
	}

	if cfg.MaxConcurrency <= 0 {
		return nil, fmt.Errorf("max_concurrency must be > 0 (got %d)", cfg.MaxConcurrency)
	}

	// Initialize logger
	logger := log.With().Str("component", "esi-client").Logger()

	// Create rate limit tracker
	rateLimiter := ratelimit.NewTracker(cfg.Redis, logger)

	// Create cache manager
	cacheManager := cache.NewManager(cfg.Redis)
	cacheManager.SetLogger(logger)

	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		redis:        cfg.Redis,
		rateLimiter:  rateLimiter,
		groupLimiter: ratelimit.NewGroupTracker(cfg.Redis, logger),
		cache:        cacheManager,
		config:       cfg,
		logger:       logger,
		limiter:      rate.NewLimiter(rate.Limit(cfg.RateLimit), cfg.RateLimit),
		sem:          make(chan struct{}, cfg.MaxConcurrency),
	}, nil
}

// Do performs an HTTP request with rate limiting, caching, and error handling.
// This is the core request method that orchestrates all ESI client features.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	endpoint := req.URL.Path

	// Gate 0a: req/s-Limiter (in-memory Token-Bucket)
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter wait: %w", err)
	}

	// Gate 0b: Concurrency-Semaphore (in-memory)
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

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
		return nil, fmt.Errorf("request blocked: rate limit critical")
	}

	// Step 2: Check Cache
	cacheKey := cache.CacheKey{
		Endpoint:    endpoint,
		QueryParams: req.URL.Query(),
	}
	// Authentifizierte Requests: Hash des Authorization-Headers in den Key (kein Roh-Token).
	if authHeader := req.Header.Get("Authorization"); authHeader != "" {
		sum := sha256.Sum256([]byte(authHeader))
		cacheKey.Auth = hex.EncodeToString(sum[:])[:16]
	}

	cachedEntry, err := c.cache.Get(ctx, cacheKey)
	if err != nil && err != cache.ErrCacheMiss {
		c.logger.Warn().Err(err).Str("endpoint", endpoint).Msg("Cache get error")
	}

	// Step 3: Make Conditional Request if cache hit
	if cachedEntry != nil && cache.ShouldMakeConditionalRequest(cachedEntry) {
		cache.AddConditionalHeaders(req, cachedEntry)
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

	// E4: Body über Retries reproduzierbar machen. Falls GetBody fehlt, aber ein Body
	// vorhanden ist, einmalig puffern und GetBody setzen.
	if req.Body != nil && req.GetBody == nil {
		bodyBytes, berr := io.ReadAll(req.Body)
		if cerr := req.Body.Close(); cerr != nil {
			c.logger.Warn().Err(cerr).Str("endpoint", endpoint).Msg("Failed to close request body after buffering")
		}
		if berr != nil {
			return nil, fmt.Errorf("buffer request body: %w", berr)
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
	}

	// Wrap the HTTP request in retry logic
	retryErr := retryWithBackoff(ctx, func() error {
		// Gruppen-Gate (X-Ratelimit-*) pro Versuch: respektiert eine aktive
		// 429-Sperre (Retry-After) auch ZWISCHEN Retry-Versuchen und bremst,
		// wenn das Token-Budget der Gruppe zur Neige geht. Lange Sperren
		// (> maxBlockWait) kommen als GroupRateLimitedError zurück — weitere
		// Versuche wären zwecklos, deshalb als nicht-retriabel klassifiziert.
		if gerr := c.groupLimiter.Gate(ctx, endpoint); gerr != nil {
			c.logger.Warn().Err(gerr).Str("endpoint", endpoint).
				Msg("Request blocked by group rate limiter")
			errClass = ErrorClassClient
			lastErr = fmt.Errorf("group rate limit gate: %w", gerr)
			return lastErr
		}

		// Body vor jedem Versuch zurücksetzen (sonst nach Versuch 1 verbraucht).
		// Schlägt GetBody fehl, darf NICHT still mit dem konsumierten/stale Body
		// weitergemacht werden — der Versuch wird mit klarem Fehler abgebrochen.
		if req.GetBody != nil {
			body, gerr := req.GetBody()
			if gerr != nil {
				c.logger.Error().Err(gerr).Str("endpoint", endpoint).
					Msg("Failed to rewind request body for retry")
				// Body is consumed/broken — retrying is futile. Mark non-retriable so
				// retryWithBackoff aborts immediately and surfaces the error.
				errClass = ErrorClassClient
				lastErr = fmt.Errorf("rewind request body: %w", gerr)
				return lastErr
			}
			req.Body = body
		}

		// Execute the HTTP request
		var reqErr error
		resp, reqErr = c.httpClient.Do(req)

		// Handle network errors
		if reqErr != nil {
			c.logger.Error().Err(reqErr).Str("endpoint", endpoint).Msg("HTTP request failed")
			errClass = c.classifyError(nil, reqErr)
			lastErr = reqErr
			return reqErr
		}

		// Update Rate Limit from headers (authoritative when present)
		headerApplied, uerr := c.rateLimiter.UpdateFromHeaders(ctx, resp.Header)
		if uerr != nil {
			c.logger.Warn().Err(uerr).Msg("Failed to update rate limit from headers")
		}

		// Update Gruppen-Rate-Limit (X-Ratelimit-*; migrierte Routen). Lernt
		// zugleich das Endpoint→Gruppe-Mapping für das Gate.
		if _, gerr := c.groupLimiter.Update(ctx, endpoint, resp.StatusCode, resp.Header); gerr != nil {
			c.logger.Warn().Err(gerr).Msg("Failed to update group rate limit state")
		}

		// Handle 304 Not Modified (not an error, return success)
		if resp.StatusCode == http.StatusNotModified {
			return nil
		}

		// Handle HTTP errors
		if resp.StatusCode >= 400 {
			errClass = c.classifyError(resp, nil)
			// Optimistischer lokaler Decrement nur, wenn ESI keinen Header lieferte
			// (sonst ist der Header bereits autoritativ; DECR würde doppelt zählen).
			// 429 gehört zum NEUEN Gruppen-Rate-Limit (oben via groupLimiter.Update
			// verarbeitet, inkl. Retry-After-Sperre) und zählt laut Doku nicht aufs
			// Legacy-Error-Budget — kein RecordError.
			if !headerApplied && resp.StatusCode != http.StatusTooManyRequests {
				if rerr := c.rateLimiter.RecordError(ctx); rerr != nil {
					c.logger.Warn().Err(rerr).Msg("Failed to record error in rate limiter")
				}
			}

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
				if cerr := resp.Body.Close(); cerr != nil { // Close the body before retrying
					c.logger.Warn().Err(cerr).Str("endpoint", endpoint).Msg("Failed to close response body before retry")
				}
				return lastErr
			}

			// Don't retry client errors - return success (let caller handle status)
			return nil
		}

		// Success
		return nil
	}, func(err error) ErrorClass {
		// Classify error dynamically for retry logic
		return errClass
	})

	// Handle retry exhaustion
	if retryErr != nil {
		if resp != nil && resp.Body != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				c.logger.Warn().Err(cerr).Str("endpoint", endpoint).Msg("Failed to close response body after retry exhaustion")
			}
		}
		return nil, retryErr
	}

	// Step 7: Handle 304 Not Modified
	if resp.StatusCode == http.StatusNotModified {
		c.logger.Debug().Str("endpoint", endpoint).Msg("304 Not Modified - using cache")

		// Update cache TTL from new expires header
		if expiresStr := resp.Header.Get("Expires"); expiresStr != "" {
			if newExpires, err := http.ParseTime(expiresStr); err == nil {
				if err := c.cache.UpdateTTL(ctx, cacheKey, newExpires); err != nil {
					c.logger.Warn().Err(err).Msg("Failed to update cache TTL")
				}
			}
		}

		// Return cached response
		if cerr := resp.Body.Close(); cerr != nil {
			c.logger.Warn().Err(cerr).Str("endpoint", endpoint).Msg("Failed to close 304 response body")
		}
		return c.cacheEntryToResponse(cachedEntry), nil
	}

	// Step 8: Update Cache on success
	if resp.StatusCode == http.StatusOK {
		entry, err := cache.ResponseToEntry(resp)
		if err != nil {
			// Response is still returned to the caller; it is just not cached.
			// A malformed cache header (fail-loud signal from the cache layer) is
			// called out explicitly so the uncached outcome is not silent.
			logEvent := c.logger.Warn().Err(err).Str("endpoint", endpoint)
			if errors.Is(err, cache.ErrMalformedCacheHeader) {
				logEvent = logEvent.Bool("malformed_cache_header", true)
			}
			logEvent.Msg("Failed to create cache entry; response returned uncached")
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
	case resp.StatusCode == http.StatusTooManyRequests:
		// Gruppen-Rate-Limit (X-Ratelimit-*): retriabel — der nächste Versuch
		// läuft durchs Gruppen-Gate und wartet dort die Retry-After-Sperre ab.
		c.logger.Debug().Str("class", string(ErrorClassRateLimit)).Msg("Error classified")
		return ErrorClassRateLimit
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

// buildPagedEndpoint hängt den page-Query-Parameter robust an, ohne eine
// bestehende Query zu zerstören.
func buildPagedEndpoint(endpoint string, pageNum int) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		return fmt.Sprintf("%s%spage=%d", endpoint, sep, pageNum)
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(pageNum))
	u.RawQuery = q.Encode()
	return u.String()
}

// FetchPage implements pagination.PageFetcher interface for batch fetching
// Returns the response body data and total page count from X-Pages header
func (c *Client) FetchPage(ctx context.Context, endpoint string, pageNum int) ([]byte, int, error) {
	// Add page parameter
	fullEndpoint := buildPagedEndpoint(endpoint, pageNum)

	resp, err := c.Get(ctx, fullEndpoint)
	if err != nil {
		return nil, 0, fmt.Errorf("GET request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			c.logger.Warn().Err(cerr).Str("endpoint", fullEndpoint).Msg("Failed to close response body")
		}
	}()

	// Check status
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	// Parse X-Pages header
	totalPages := 1
	if xPages := resp.Header.Get("X-Pages"); xPages != "" {
		if parsed, err := strconv.Atoi(xPages); err == nil {
			totalPages = parsed
		} else {
			c.logger.Warn().
				Str("x_pages", xPages).
				Err(err).
				Msg("Failed to parse X-Pages header")
		}
	}

	// Read body
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read response body: %w", err)
	}

	return data, totalPages, nil
}

// Close is a no-op: the Redis client is injected and owned by the caller,
// so this client deliberately does not close it. Present for interface symmetry.
func (c *Client) Close() error {
	return nil
}

// SetHTTPClient sets a custom HTTP client.
// INTERNAL USE: Testing only. Not part of public API.
func (c *Client) SetHTTPClient(client *http.Client) {
	c.httpClient = client
}

// GetCache returns the cache manager.
// INTERNAL USE: Testing only. Not part of public API.
func (c *Client) GetCache() *cache.Manager {
	return c.cache
}
