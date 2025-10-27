# Library Usage Example

This example demonstrates how to use the EVE ESI Client as a Go library in your application.

## Prerequisites

- Go 1.21 or higher
- Redis server running (default: localhost:6379)

## Quick Start

### 1. Start Redis (if not already running)

```bash
# Using Docker
docker run -d -p 6379:6379 redis:7-alpine

# Or using your local Redis installation
redis-server
```

### 2. Run the Example

```bash
# From the repository root
cd examples/library-usage
go run main.go
```

### 3. Expected Output

```
✅ Connected to Redis
✅ ESI client initialized

📊 Fetching market orders from region 10000002...
✅ Retrieved 15234 market orders

📋 Sample Orders:
─────────────────────────────────────────────────────────
SELL | TypeID:    34 | Price:    100000.00 ISK | Volume:     1000
SELL | TypeID:    35 | Price:    250000.00 ISK | Volume:      500
BUY  | TypeID:    36 | Price:     95000.00 ISK | Volume:     2000
SELL | TypeID:    37 | Price:    150000.00 ISK | Volume:      750
SELL | TypeID:    38 | Price:    300000.00 ISK | Volume:      300
─────────────────────────────────────────────────────────

🔄 Making second request (should use cache)...
✅ 304 Not Modified - cache is working!

🔍 Testing error handling with invalid endpoint...
⚠️  ESI returned error status: 404

📈 Example completed successfully!

Key Features Demonstrated:
  ✅ Automatic rate limiting
  ✅ Redis-backed caching
  ✅ ETag-based conditional requests
  ✅ Error handling and retries
  ✅ Structured logging
  ✅ Prometheus metrics (exposed at /metrics)
```

## What This Example Demonstrates

### 1. **ESI Client Initialization**

```go
// Create client with default configuration
cfg := client.DefaultConfig(redisClient, "MyApp/1.0.0 (contact@example.com)")
esiClient, err := client.New(cfg)
```

**Important**: The User-Agent must follow ESI requirements: `AppName/Version (contact@example.com)`

### 2. **Configuration Options**

```go
cfg := client.DefaultConfig(redisClient, userAgent)

// Customize settings
cfg.MaxRetries = 3              // Retry failed requests up to 3 times
cfg.InitialBackoff = 1 * time.Second  // Start with 1s backoff
cfg.ErrorThreshold = 10         // Block when < 10 errors remaining
cfg.MaxConcurrency = 5          // Max 5 parallel requests
```

### 3. **Making Requests**

```go
ctx := context.Background()
resp, err := esiClient.Get(ctx, "/v1/markets/10000002/orders/")
if err != nil {
    log.Fatalf("Request failed: %v", err)
}
defer resp.Body.Close()
```

The client automatically handles:
- ✅ Rate limit checking (prevents ESI bans)
- ✅ Cache lookup (Redis)
- ✅ Conditional requests (If-None-Match with ETag)
- ✅ Response caching
- ✅ Retry logic for server errors
- ✅ Metrics collection

### 4. **Handling Responses**

```go
// Check status code
if resp.StatusCode != 200 {
    body, _ := io.ReadAll(resp.Body)
    log.Printf("Error: %d - %s", resp.StatusCode, string(body))
    return
}

// Parse JSON
var data interface{}
body, _ := io.ReadAll(resp.Body)
json.Unmarshal(body, &data)
```

### 5. **Caching Behavior**

The client automatically:
1. **Checks cache** before making ESI requests
2. **Uses conditional requests** (If-None-Match) if cached data exists
3. **Handles 304 Not Modified** by returning cached data
4. **Updates cache** on successful 200 OK responses

```
Request 1: Cache Miss → ESI Request → Cache Store → Return Data
Request 2: Cache Hit → Conditional Request → 304 Not Modified → Return Cached Data
```

### 6. **Error Handling**

```go
resp, err := esiClient.Get(ctx, endpoint)
if err != nil {
    // Handle errors:
    // - Network errors (will retry)
    // - Rate limit blocks
    // - Context cancellation
    return err
}

// Check HTTP status
switch resp.StatusCode {
case 200:
    // Success
case 304:
    // Not Modified (cache valid)
case 404:
    // Not Found (client error - no retry)
case 500:
    // Server Error (will retry automatically)
case 520:
    // ESI Rate Limit (will retry with backoff)
}
```

### 7. **Rate Limit Protection**

The client monitors ESI error headers and **automatically blocks requests** when approaching the error limit:

```
Errors Remaining ≥ 50: ✅ Normal operation
Errors Remaining 20-49: ⚠️  Throttled (1s delay)
Errors Remaining < 5:  🛑 Blocked (wait for reset)
```

**Why This Matters**: Exceeding the ESI error limit results in **permanent IP ban**.

## Environment Variables

```bash
# Redis connection
export REDIS_URL="localhost:6379"
export REDIS_PASSWORD=""  # Optional

# Application settings (optional)
export LOG_LEVEL="info"   # debug, info, warn, error
```

## Integration in Your Application

### Minimal Example

```go
package main

import (
    "context"
    "log"
    
    "github.com/Sternrassler/eve-esi-client/pkg/client"
    "github.com/redis/go-redis/v9"
)

func main() {
    // Setup
    redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer redisClient.Close()
    
    cfg := client.DefaultConfig(redisClient, "MyApp/1.0.0 (contact@example.com)")
    esiClient, _ := client.New(cfg)
    defer esiClient.Close()
    
    // Make request
    resp, err := esiClient.Get(context.Background(), "/v1/status/")
    if err != nil {
        log.Fatal(err)
    }
    defer resp.Body.Close()
    
    log.Printf("ESI Status: %d", resp.StatusCode)
}
```

### With Timeout Context

```go
// Set 30 second timeout
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

resp, err := esiClient.Get(ctx, "/v1/markets/10000002/orders/")
if err != nil {
    if errors.Is(err, context.DeadlineExceeded) {
        log.Println("Request timed out")
    }
    return err
}
```

### Concurrent Requests

```go
var wg sync.WaitGroup
endpoints := []string{
    "/v1/markets/10000002/orders/",
    "/v1/markets/10000042/orders/",
    "/v1/markets/10000043/orders/",
}

for _, endpoint := range endpoints {
    wg.Add(1)
    go func(ep string) {
        defer wg.Done()
        
        resp, err := esiClient.Get(context.Background(), ep)
        if err != nil {
            log.Printf("Error fetching %s: %v", ep, err)
            return
        }
        defer resp.Body.Close()
        
        log.Printf("Fetched %s: %d", ep, resp.StatusCode)
    }(endpoint)
}

wg.Wait()
```

**Note**: The client handles concurrency limits automatically (`MaxConcurrency` setting).

## Monitoring

### Prometheus Metrics

The client exposes Prometheus metrics for monitoring:

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

// Expose metrics endpoint
http.Handle("/metrics", promhttp.Handler())
go http.ListenAndServe(":9090", nil)
```

Available metrics:
- `esi_requests_total` - Total requests by endpoint and status
- `esi_request_duration_seconds` - Request latency histogram
- `esi_errors_total` - Errors by classification
- `esi_cache_hits_total` - Cache hit rate
- `esi_errors_remaining` - Current ESI error limit
- `esi_rate_limit_blocks_total` - Requests blocked by rate limiter

### Structured Logging

The client uses structured JSON logging (zerolog):

```json
{"level":"info","component":"esi-client","endpoint":"/v1/status/","status":200,"duration":123,"time":"2025-01-01T12:00:00Z","message":"Request completed"}
```

## Best Practices

1. **Always set a User-Agent** with contact information (ESI requirement)
2. **Use context with timeouts** to prevent hanging requests
3. **Handle all status codes** properly (don't assume 200 OK)
4. **Monitor metrics** to detect issues early
5. **Set appropriate `ErrorThreshold`** (default 10 is safe)
6. **Use shared Redis** for distributed deployments (rate limit coordination)
7. **Close client on shutdown** to cleanup resources

## Troubleshooting

### "Request blocked: rate limit critical"

**Cause**: ESI error limit is critically low (< 5 errors remaining).

**Solution**: Wait for the error limit to reset (usually 60 seconds). Check logs for what's causing errors.

### "Cache miss" for every request

**Cause**: Redis connection issue or TTL too short.

**Solution**: Verify Redis is running and accessible. Check `Expires` headers from ESI.

### High retry rate

**Cause**: ESI server errors (5xx) or rate limits (520).

**Solution**: This is normal during ESI maintenance. Adjust `MaxRetries` and `InitialBackoff` if needed.

## Further Reading

- [ESI Documentation](https://docs.esi.evetech.net/)
- [ESI Best Practices](https://docs.esi.evetech.net/docs/best_practices.html)
- [Project README](../../README.md)
- [Configuration Guide](../../docs/configuration.md)
- [Monitoring Guide](../../docs/monitoring.md)

## License

MIT - See [LICENSE](../../LICENSE)
