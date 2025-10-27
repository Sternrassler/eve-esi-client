# ESI Client Usage Examples

This document provides examples of using the ESI Client library.

## Basic Setup

```go
package main

import (
    "context"
    "fmt"
    "log"
    
    "github.com/Sternrassler/eve-esi-client/pkg/client"
    "github.com/redis/go-redis/v9"
)

func main() {
    // Create Redis client
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
        DB:   0,
    })
    defer redisClient.Close()
    
    // Create ESI client with default configuration
    cfg := client.DefaultConfig(redisClient, "MyApp/1.0.0 (contact@example.com)")
    esiClient, err := client.New(cfg)
    if err != nil {
        log.Fatalf("Failed to create ESI client: %v", err)
    }
    defer esiClient.Close()
    
    // Make a request
    ctx := context.Background()
    resp, err := esiClient.Get(ctx, "/v1/status/")
    if err != nil {
        log.Fatalf("Request failed: %v", err)
    }
    defer resp.Body.Close()
    
    fmt.Printf("Status: %d\n", resp.StatusCode)
}
```

## Custom Configuration

```go
cfg := client.Config{
    Redis:          redisClient,
    UserAgent:      "MyApp/1.0.0 (contact@example.com)",
    RateLimit:      15,                    // Requests per second
    ErrorThreshold: 10,                    // Stop when < 10 errors remaining
    MaxConcurrency: 10,                    // Max parallel requests
    MemoryCacheTTL: 60 * time.Second,     // In-memory cache TTL
    RespectExpires: true,                  // MUST be true
    MaxRetries:     3,
    InitialBackoff: 1 * time.Second,
}

esiClient, err := client.New(cfg)
if err != nil {
    log.Fatalf("Failed to create client: %v", err)
}
```

## Making Requests

### Simple GET Request

```go
resp, err := esiClient.Get(context.Background(), "/v4/universe/types/")
if err != nil {
    log.Printf("Request failed: %v", err)
    return
}
defer resp.Body.Close()

// Read response body
body, _ := io.ReadAll(resp.Body)
fmt.Println(string(body))
```

### Custom Request with Do()

```go
req, err := http.NewRequestWithContext(
    context.Background(),
    "GET",
    "https://esi.evetech.net/v1/markets/10000002/orders/",
    nil,
)
if err != nil {
    log.Fatalf("Failed to create request: %v", err)
}

// Add query parameters
q := req.URL.Query()
q.Add("order_type", "all")
req.URL.RawQuery = q.Encode()

resp, err := esiClient.Do(req)
if err != nil {
    log.Printf("Request failed: %v", err)
    return
}
defer resp.Body.Close()
```

## Features

### Automatic Rate Limiting

The client automatically:
- Monitors `X-ESI-Error-Limit-Remain` and `X-ESI-Error-Limit-Reset` headers
- Blocks requests when error limit is critical (< 5 errors remaining)
- Throttles requests when in warning state (< 20 errors remaining)

No manual intervention required!

### Automatic Caching

The client automatically:
- Caches responses according to ESI's `Expires` header
- Makes conditional requests using `If-None-Match` (ETag)
- Handles `304 Not Modified` responses
- Updates cache TTL from new expires headers

### Error Classification

Errors are classified for observability:
- `ErrorClassClient`: 4xx errors
- `ErrorClassServer`: 5xx errors
- `ErrorClassRateLimit`: 520 errors
- `ErrorClassNetwork`: Network/timeout errors

### Observability

Prometheus metrics are automatically exported:
- `esi_requests_total{endpoint, status}`: Total requests
- `esi_request_duration_seconds{endpoint}`: Request duration
- `esi_errors_total{class}`: Errors by classification

Structured logging with different levels:
- Debug: Request flow details
- Info: Successful operations
- Warn: Rate limit warnings, cache issues
- Error: Failed requests

## Advanced Usage

### Monitoring Rate Limit State

```go
// Access the rate limiter directly if needed
state, err := esiClient.RateLimiter().GetState(context.Background())
if err != nil {
    log.Printf("Failed to get rate limit state: %v", err)
    return
}

fmt.Printf("Errors remaining: %d\n", state.ErrorsRemaining)
fmt.Printf("Reset at: %s\n", state.ResetAt)
fmt.Printf("Is healthy: %v\n", state.IsHealthy)
```

### Cache Management

```go
// The cache is managed automatically, but you can access it if needed
cacheKey := cache.CacheKey{
    Endpoint: "/v1/status/",
}

entry, err := esiClient.Cache().Get(context.Background(), cacheKey)
if err == cache.ErrCacheMiss {
    fmt.Println("Cache miss")
} else if err != nil {
    log.Printf("Cache error: %v", err)
} else {
    fmt.Printf("Cached data: %s\n", entry.Data)
    fmt.Printf("Expires: %s\n", entry.Expires)
}
```

## Best Practices

1. **Always set a proper User-Agent**: Include your app name, version, and contact email
2. **Never set RespectExpires to false**: This can get you banned from ESI
3. **Use context for timeouts**: Always pass a context with timeout for production code
4. **Handle rate limit errors gracefully**: The client will block automatically, but you should handle errors appropriately
5. **Monitor metrics**: Use Prometheus to track your ESI usage
6. **Share Redis instance**: Multiple client instances can share the same Redis for distributed rate limiting

## Error Handling

```go
resp, err := esiClient.Get(ctx, "/v1/status/")
if err != nil {
    // Check if it's a rate limit block
    if strings.Contains(err.Error(), "rate limit critical") {
        log.Println("Rate limited, waiting for reset...")
        return
    }
    
    // Other errors
    log.Printf("Request failed: %v", err)
    return
}
defer resp.Body.Close()

// Check HTTP status
if resp.StatusCode >= 400 {
    log.Printf("ESI returned error: %d", resp.StatusCode)
    return
}
```

## Production Deployment

For production use:

1. Deploy Redis in a highly available configuration
2. Monitor Prometheus metrics
3. Set up alerts for:
   - Critical rate limit state
   - High error rates
   - Cache failures
4. Use multiple client instances behind a load balancer
5. Configure appropriate timeouts and retry strategies

## References

- [ESI Documentation](https://docs.esi.evetech.net/)
- [ADR-005: ESI Client Architecture](../docs/adr/ADR-005-esi-client-architecture.md)
- [ADR-006: Error & Rate Limit Handling](../docs/adr/ADR-006-esi-error-rate-limit-handling.md)
- [ADR-007: Caching Strategy](../docs/adr/ADR-007-esi-caching-strategy.md)
