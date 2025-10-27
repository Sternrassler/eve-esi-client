// Package cache provides ESI caching functionality with Redis backend.
//
// The cache manager implements ESI-compliant caching with the following features:
//
// - Strict respect of ESI expires headers (prevents IP bans)
// - ETag support for conditional requests (If-None-Match)
// - Last-Modified support (If-Modified-Since)
// - Automatic TTL management based on expires header
// - Prometheus metrics for observability
// - Deterministic cache key generation
//
// # Basic Usage
//
//	// Create Redis client
//	redisClient := redis.NewClient(&redis.Options{
//		Addr: "localhost:6379",
//	})
//
//	// Create cache manager
//	manager := cache.NewManager(redisClient)
//
//	// Create cache key
//	key := cache.CacheKey{
//		Endpoint: "/v1/markets/10000002/orders/",
//		QueryParams: url.Values{"order_type": []string{"all"}},
//	}
//
//	// Get from cache
//	entry, err := manager.Get(ctx, key)
//	if err == cache.ErrCacheMiss {
//		// Cache miss - fetch from ESI
//	}
//
// # HTTP Response Caching
//
//	// Convert HTTP response to cache entry
//	entry, err := cache.ResponseToEntry(resp)
//	if err != nil {
//		return err
//	}
//
//	// Store in cache
//	if err := manager.Set(ctx, key, entry); err != nil {
//		return err
//	}
//
// # Conditional Requests
//
//	// Check if we should make a conditional request
//	if cache.ShouldMakeConditionalRequest(entry) {
//		cache.AddConditionalHeaders(req, entry)
//		// Make request - ESI will return 304 if not modified
//	}
//
// # Metrics
//
// The cache manager exports Prometheus metrics:
//
//   - esi_cache_hits_total{layer="redis"} - Cache hits
//   - esi_cache_misses_total - Cache misses
//   - esi_cache_size_bytes{layer="redis"} - Cache size
//   - esi_304_responses_total - Conditional request successes
//   - esi_cache_errors_total{operation} - Cache operation errors
//
// # ESI Compliance
//
// This package strictly follows ESI caching requirements:
//
// - MUST respect expires header (cache for at least that long)
// - MUST NOT circumvent caching (risk of permanent IP ban)
// - SHOULD use conditional requests (If-None-Match) when possible
// - 304 Not Modified responses do NOT count against error limit
//
// See ADR-007: ESI Caching Strategy for full architecture details.
package cache
