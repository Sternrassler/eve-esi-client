package client
// Package client provides the core ESI HTTP client with rate limiting,
// caching, and error handling.
package client

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client is the main ESI client.
type Client struct {
	httpClient *http.Client
	redis      *redis.Client
	config     Config
	userAgent  string
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

	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		redis:     cfg.Redis,
		config:    cfg,
		userAgent: cfg.UserAgent,
	}, nil
}

// Get performs a GET request to an ESI endpoint.
func (c *Client) Get(ctx context.Context, endpoint string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://esi.evetech.net"+endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	// TODO: Implement rate limiting, caching, error handling
	// For now, just pass through to ESI
	return c.httpClient.Do(req)
}

// Close closes the client and releases resources.
func (c *Client) Close() error {
	// TODO: Cleanup resources
	return nil
}
