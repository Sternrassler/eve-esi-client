// Package cache provides ESI caching functionality with Redis backend
// and ETag support for conditional requests.
package cache

import (
	"net/http"
	"time"
)

// CacheEntry represents a cached ESI response.
type CacheEntry struct {
	// Data is the response body
	Data []byte `json:"data"`

	// ETag for conditional requests (If-None-Match)
	ETag string `json:"etag"`

	// Expires is when the cache entry becomes stale (from ESI expires header)
	Expires time.Time `json:"expires"`

	// LastModified is when the data was last modified (from ESI last-modified header)
	LastModified time.Time `json:"last_modified"`

	// StatusCode is the HTTP status code of the cached response
	StatusCode int `json:"status_code"`

	// Headers are the response headers
	Headers http.Header `json:"headers"`

	// CachedAt is when we cached this response
	CachedAt time.Time `json:"cached_at"`
}

// IsExpired returns true if the cache entry has expired.
func (e *CacheEntry) IsExpired() bool {
	return time.Now().After(e.Expires)
}

// TTL returns the time until expiration.
// Returns 0 if already expired.
func (e *CacheEntry) TTL() time.Duration {
	ttl := time.Until(e.Expires)
	if ttl < 0 {
		return 0
	}
	return ttl
}
