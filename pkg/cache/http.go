package cache

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
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
	if cerr := resp.Body.Close(); cerr != nil {
		// A close failure after a successful read signals a broken connection/reader;
		// surface it rather than silently discarding it so the response is not cached
		// under a false assumption of integrity.
		return nil, fmt.Errorf("close response body: %w", cerr)
	}

	// Restore body for caller
	resp.Body = io.NopCloser(bytes.NewReader(body))

	entry := &CacheEntry{
		Data:       body,
		ETag:       resp.Header.Get("ETag"),
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		CachedAt:   time.Now(),
	}

	// Cache-Control hat Vorrang vor Expires (no-store/no-cache → nicht cachen; max-age → TTL).
	// Ein MALFORMED Header ist fail-loud (Fehler); ein ABSENTER Header nutzt legitim DefaultTTL.
	expires, err := expiryFromHeaders(resp.Header)
	if err != nil {
		return nil, err
	}
	entry.Expires = expires

	// Parse Last-Modified header
	if lastModStr := resp.Header.Get("Last-Modified"); lastModStr != "" {
		if lastMod, err := http.ParseTime(lastModStr); err == nil {
			entry.LastModified = lastMod
		}
	}

	return entry, nil
}

// ErrMalformedCacheHeader signals that a cache-relevant header was PRESENT but
// could not be parsed. A malformed header must fail loud (the caller decides how
// to proceed) instead of being silently replaced by DefaultTTL, which would break
// the cache contract without any signal. An ABSENT header is not an error and
// legitimately uses DefaultTTL (ESI cache compliance).
var ErrMalformedCacheHeader = errors.New("malformed cache header")

// expiryFromHeaders bestimmt die Ablaufzeit aus Cache-Control (Vorrang) oder Expires.
// Ein vorhandener, aber unparsbarer Header (max-age, Expires) ergibt ErrMalformedCacheHeader.
func expiryFromHeaders(headers http.Header) (time.Time, error) {
	cc := strings.ToLower(headers.Get("Cache-Control"))
	if cc != "" {
		if strings.Contains(cc, "no-store") || strings.Contains(cc, "no-cache") {
			return time.Now(), nil // TTL 0 → wird nicht gecacht
		}
		for _, part := range strings.Split(cc, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "max-age=") {
				raw := strings.TrimPrefix(part, "max-age=")
				secs, err := strconv.Atoi(raw)
				if err != nil {
					return time.Time{}, fmt.Errorf("%w: Cache-Control max-age=%q: %v",
						ErrMalformedCacheHeader, raw, err)
				}
				if secs <= 0 {
					return time.Now(), nil
				}
				return time.Now().Add(time.Duration(secs) * time.Second), nil
			}
		}
	}
	return parseExpires(headers)
}

// parseExpires parses the Expires header from HTTP headers.
// Returns current time + DefaultTTL when the header is ABSENT (legitimate ESI default).
// Returns ErrMalformedCacheHeader when the header is PRESENT but unparsable (fail-loud).
func parseExpires(headers http.Header) (time.Time, error) {
	expiresStr := headers.Get("Expires")
	if expiresStr == "" {
		// No expires header - use default TTL (legitimate, ESI cache compliance)
		return time.Now().Add(DefaultTTL), nil
	}

	expires, err := http.ParseTime(expiresStr)
	if err != nil {
		// Header present but malformed - fail loud instead of silently substituting DefaultTTL
		return time.Time{}, fmt.Errorf("%w: Expires=%q: %v", ErrMalformedCacheHeader, expiresStr, err)
	}

	// Validate that TTL is not negative
	if expires.Before(time.Now()) {
		// Already expired - use minimal TTL
		return time.Now(), nil
	}

	return expires, nil
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

// EntryToResponse converts a cache entry back to an HTTP response.
func EntryToResponse(entry *CacheEntry) *http.Response {
	if entry == nil {
		return nil
	}

	return &http.Response{
		StatusCode: entry.StatusCode,
		Header:     entry.Headers.Clone(),
		Body:       io.NopCloser(bytes.NewReader(entry.Data)),
	}
}
