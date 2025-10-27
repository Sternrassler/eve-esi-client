package cache

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// DefaultTTL is the fallback TTL when no expires header is present
	DefaultTTL = 5 * time.Minute
)

// ResponseToEntry converts an HTTP response to a CacheEntry.
// It parses expires and last-modified headers and reads the response body.
// The response body is restored after reading.
func ResponseToEntry(resp *http.Response) (*CacheEntry, error) {
	if resp == nil {
		return nil, fmt.Errorf("response cannot be nil")
	}

	// Read body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	resp.Body.Close()

	// Restore body for caller
	resp.Body = io.NopCloser(bytes.NewReader(body))

	entry := &CacheEntry{
		Data:       body,
		ETag:       resp.Header.Get("ETag"),
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		CachedAt:   time.Now(),
	}

	// Parse Expires header (MUST respect per ESI documentation)
	entry.Expires = parseExpires(resp.Header)

	// Parse Last-Modified header
	if lastModStr := resp.Header.Get("Last-Modified"); lastModStr != "" {
		if lastMod, err := http.ParseTime(lastModStr); err == nil {
			entry.LastModified = lastMod
		}
	}

	return entry, nil
}

// parseExpires parses the Expires header from HTTP headers.
// Returns the parsed expiration time, or current time + DefaultTTL if parsing fails.
func parseExpires(headers http.Header) time.Time {
	expiresStr := headers.Get("Expires")
	if expiresStr == "" {
		// No expires header - use default TTL
		return time.Now().Add(DefaultTTL)
	}

	expires, err := http.ParseTime(expiresStr)
	if err != nil {
		// Failed to parse expires header - use default TTL
		return time.Now().Add(DefaultTTL)
	}

	// Validate that TTL is not negative
	if expires.Before(time.Now()) {
		// Already expired - use minimal TTL
		return time.Now()
	}

	return expires
}

// ShouldMakeConditionalRequest determines if we should add conditional
// request headers (If-None-Match or If-Modified-Since) based on the cache entry.
func ShouldMakeConditionalRequest(entry *CacheEntry) bool {
	if entry == nil {
		return false
	}
	// We can make a conditional request if we have either ETag or Last-Modified
	return entry.ETag != "" || !entry.LastModified.IsZero()
}

// AddConditionalHeaders adds If-None-Match (ETag) or If-Modified-Since headers
// to the request if the cache entry supports conditional requests.
func AddConditionalHeaders(req *http.Request, entry *CacheEntry) {
	if entry == nil || req == nil {
		return
	}

	// Prefer ETag over Last-Modified (more accurate)
	if entry.ETag != "" {
		req.Header.Set("If-None-Match", entry.ETag)
	} else if !entry.LastModified.IsZero() {
		req.Header.Set("If-Modified-Since", entry.LastModified.Format(http.TimeFormat))
	}
}
