# Configuration Guide

Complete reference for configuring the EVE ESI Client.

## Table of Contents

- [Configuration Structure](#configuration-structure)
- [Required Settings](#required-settings)
- [Rate Limiting](#rate-limiting)
- [Caching](#caching)
- [Retry Behavior](#retry-behavior)
- [Concurrency](#concurrency)
- [Environment Variables](#environment-variables)
- [Advanced Configuration](#advanced-configuration)

## Configuration Structure

The client is configured using the `client.Config` struct:

```go
type Config struct {
    // Required
    Redis     *redis.Client
    UserAgent string

    // Rate Limiting
    RateLimit      int
    ErrorThreshold int

    // Concurrency
    MaxConcurrency int

    // Caching
    MemoryCacheTTL time.Duration
    RespectExpires bool

    // Retry
    MaxRetries     int
    InitialBackoff time.Duration
}
```

## Required Settings

### Redis Client

**Required**: Yes  
**Type**: `*redis.Client`

Redis is used for:
- Distributed rate limit state (shared across instances)
- Response caching with TTL
- ETag storage for conditional requests

```go
redisClient := redis.NewClient(&redis.Options{
    Addr:     "localhost:6379",
    Password: "", // Set if required
    DB:       0,  // Use default DB
})
```

**Production Configuration:**

```go
redisClient := redis.NewClient(&redis.Options{
    Addr:         "redis:6379",
    Password:     os.Getenv("REDIS_PASSWORD"),
    DB:           0,
    MaxRetries:   3,
    PoolSize:     10,
    MinIdleConns: 5,
    DialTimeout:  5 * time.Second,
    ReadTimeout:  3 * time.Second,
    WriteTimeout: 3 * time.Second,
})
```

### User-Agent

**Required**: Yes  
**Type**: `string`  
**Format**: `AppName/Version (contact)`

ESI requires a properly formatted User-Agent with contact information.

```go
// Good examples
"MyTradingApp/1.0.0 (trader@example.com)"
"CorpDashboard/2.1.0 (https://github.com/user/repo)"
"MarketTool/1.5.0 (Discord: username#1234)"

// Bad examples (will be rejected)
"MyApp"           // Missing version and contact
"curl/7.68.0"     // Generic, no contact
```

**Why Required?** CCP needs to contact developers if there are issues with your application.

## Rate Limiting

### RateLimit

**Default**: `10`  
**Type**: `int`  
**Unit**: Requests per second

Maximum requests per second to ESI. This is a **soft limit** for your application.

```go
cfg.RateLimit = 10  // Max 10 requests/second
```

**Note**: ESI uses error-based rate limiting (not request-based), but this setting helps prevent hammering the API.

### ErrorThreshold

**Default**: `10`  
**Type**: `int`  
**Range**: 5-100

Blocks requests when ESI error limit falls below this threshold.

```go
cfg.ErrorThreshold = 10  // Block when < 10 errors remaining
```

**Recommendations:**
- **Conservative**: 20 (blocks early, very safe)
- **Balanced**: 10 (default, good for most use cases)
- **Aggressive**: 5 (minimum, risky if many errors occur)

**Why it matters**: Exceeding ESI's error limit results in **permanent IP ban**.

### Rate Limit States

The client operates in three states based on ESI error headers:

| State | Errors Remaining | Behavior |
|-------|-----------------|----------|
| 🟢 Healthy | ≥ 50 | Normal operation, no restrictions |
| 🟡 Warning | 20-49 | Throttled (1s delay between requests) |
| 🔴 Critical | < ErrorThreshold | All requests blocked until reset |

## Caching

### MemoryCacheTTL

**Default**: `60 * time.Second`  
**Type**: `time.Duration`

In-memory cache TTL for frequently accessed data (currently unused, reserved for future use).

```go
cfg.MemoryCacheTTL = 60 * time.Second
```

### RespectExpires

**Default**: `true`  
**Type**: `bool`  
**Required**: **MUST be true**

Honor ESI `Expires` header for cache TTL. This is an **ESI requirement**.

```go
cfg.RespectExpires = true  // REQUIRED
```

**Why MUST be true?**  
ESI compliance requires respecting cache expiration headers. Setting to `false` will cause client initialization to fail.

### Cache Behavior

The client implements a two-tier caching strategy:

1. **Redis Cache** (primary)
   - Stores full response body
   - Respects ESI `Expires` header
   - Shared across all client instances

2. **Conditional Requests**
   - Uses `If-None-Match` header with ETag
   - Receives `304 Not Modified` when cache is valid
   - Updates TTL from new `Expires` header

**Cache Flow:**

```
Request → Check Redis Cache
  ↓
Cache Hit?
  ↓ Yes                    ↓ No
Make conditional request   Make normal request
  ↓                        ↓
304 Not Modified?          Store in cache
  ↓ Yes      ↓ No           ↓
Return cached | Update cache | Return response
```

## Retry Behavior

### MaxRetries

**Default**: `3`  
**Type**: `int`  
**Range**: 0-10

Maximum number of retry attempts for failed requests.

```go
cfg.MaxRetries = 3  // Retry up to 3 times
```

**Retry Strategy by Error Type:**

| Error Class | Retry? | Max Attempts | Initial Backoff |
|------------|--------|--------------|-----------------|
| 4xx Client | ❌ No | - | - |
| 5xx Server | ✅ Yes | 3 | 1s |
| 520 Rate Limit | ✅ Yes | 3 | 5s |
| Network | ✅ Yes | 3 | 2s |

### InitialBackoff

**Default**: `1 * time.Second`  
**Type**: `time.Duration`

Initial backoff duration for exponential retry.

```go
cfg.InitialBackoff = 1 * time.Second
```

**Backoff Calculation:**

```
Attempt 1: No delay (initial request)
Attempt 2: InitialBackoff * (0.8 to 1.2) with jitter
Attempt 3: InitialBackoff * 2 * (0.8 to 1.2) with jitter
Attempt 4: InitialBackoff * 4 * (0.8 to 1.2) with jitter
```

**Example with InitialBackoff = 1s:**

```
Attempt 1: Immediate
Attempt 2: ~1s (0.8s - 1.2s)
Attempt 3: ~2s (1.6s - 2.4s)
Attempt 4: ~4s (3.2s - 4.8s)
```

**Custom Retry Configuration:**

```go
// Aggressive (fast retries, good for testing)
cfg.MaxRetries = 5
cfg.InitialBackoff = 100 * time.Millisecond

// Conservative (slow retries, production)
cfg.MaxRetries = 3
cfg.InitialBackoff = 2 * time.Second
```

## Concurrency

### MaxConcurrency

**Default**: `5`  
**Type**: `int`  
**Range**: 1-100

Maximum number of parallel ESI requests.

```go
cfg.MaxConcurrency = 5  // Max 5 concurrent requests
```

**Recommendations:**
- **Single Instance**: 5-10
- **Multiple Instances**: 3-5 per instance
- **High Volume**: Monitor rate limit state and adjust

**Trade-offs:**

| Setting | Throughput | Rate Limit Risk | Resource Usage |
|---------|-----------|-----------------|----------------|
| 1-2 | Low | Minimal | Low |
| 5-10 | Medium | Low | Medium |
| 20+ | High | **High** | High |

## Environment Variables

While the client is configured programmatically, you can use environment variables:

```go
import "os"

func loadConfig() client.Config {
    redisURL := os.Getenv("REDIS_URL")
    if redisURL == "" {
        redisURL = "localhost:6379"
    }

    redisClient := redis.NewClient(&redis.Options{
        Addr:     redisURL,
        Password: os.Getenv("REDIS_PASSWORD"),
    })

    userAgent := os.Getenv("USER_AGENT")
    if userAgent == "" {
        userAgent = "MyApp/1.0.0 (default@example.com)"
    }

    cfg := client.DefaultConfig(redisClient, userAgent)
    
    // Override defaults from env vars
    if maxRetries := os.Getenv("MAX_RETRIES"); maxRetries != "" {
        cfg.MaxRetries, _ = strconv.Atoi(maxRetries)
    }
    
    return cfg
}
```

**Example `.env` file:**

```bash
# Redis
REDIS_URL=localhost:6379
REDIS_PASSWORD=secret

# ESI Client
USER_AGENT="MyApp/1.0.0 (contact@example.com)"
MAX_RETRIES=3
ERROR_THRESHOLD=10
MAX_CONCURRENCY=5

# Logging
LOG_LEVEL=info
```

## Advanced Configuration

### Production Configuration

```go
func productionConfig() client.Config {
    // Redis with connection pooling
    redisClient := redis.NewClient(&redis.Options{
        Addr:         os.Getenv("REDIS_URL"),
        Password:     os.Getenv("REDIS_PASSWORD"),
        DB:           0,
        MaxRetries:   3,
        PoolSize:     20,
        MinIdleConns: 5,
        DialTimeout:  5 * time.Second,
        ReadTimeout:  3 * time.Second,
        WriteTimeout: 3 * time.Second,
    })

    cfg := client.Config{
        Redis:     redisClient,
        UserAgent: os.Getenv("USER_AGENT"),

        // Conservative rate limiting
        RateLimit:      10,
        ErrorThreshold: 15, // Block early

        // Moderate concurrency
        MaxConcurrency: 5,

        // Standard caching
        MemoryCacheTTL: 60 * time.Second,
        RespectExpires: true,

        // Robust retry
        MaxRetries:     3,
        InitialBackoff: 2 * time.Second,
    }

    return cfg
}
```

### Development Configuration

```go
func devConfig() client.Config {
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })

    cfg := client.DefaultConfig(
        redisClient,
        "DevApp/0.0.1 (dev@localhost)",
    )

    // Fast retries for development
    cfg.InitialBackoff = 100 * time.Millisecond
    cfg.MaxRetries = 2

    return cfg
}
```

### Testing Configuration

```go
func testConfig(t *testing.T) client.Config {
    // Use test Redis container
    redisClient, cleanup := setupTestRedis(t)
    t.Cleanup(cleanup)

    cfg := client.DefaultConfig(
        redisClient,
        "TestApp/0.0.1 (test@example.com)",
    )

    // Disable retries for predictable tests
    cfg.MaxRetries = 0
    cfg.ErrorThreshold = 5

    return cfg
}
```

## Configuration Validation

The client validates configuration on initialization:

```go
esiClient, err := client.New(cfg)
if err != nil {
    // Possible errors:
    // - Redis client is nil
    // - UserAgent is empty
    // - RespectExpires is false
    // - ErrorThreshold < 5
}
```

**Validation Rules:**
- ✅ `Redis` must not be nil
- ✅ `UserAgent` must not be empty
- ✅ `RespectExpires` must be true
- ✅ `ErrorThreshold` must be ≥ 5

## Configuration Best Practices

1. **Always use `DefaultConfig()` as a starting point**
   ```go
   cfg := client.DefaultConfig(redis, userAgent)
   // Then customize as needed
   ```

2. **Set appropriate ErrorThreshold for your use case**
   - High volume: 15-20 (safer)
   - Low volume: 10 (default)
   - Testing: 5 (minimum)

3. **Adjust MaxConcurrency based on deployment**
   - Single instance: 5-10
   - Multiple instances: 2-5 per instance

4. **Use longer InitialBackoff in production**
   - Development: 100ms-500ms
   - Production: 1s-2s

5. **Monitor metrics to tune configuration**
   - Watch `esi_rate_limit_blocks_total` (should be near 0)
   - Watch `esi_retry_exhausted_total` (indicates retry tuning needed)

6. **Use separate Redis instance for production**
   - Don't share with other applications
   - Use persistence (AOF or RDB)
   - Monitor memory usage

## See Also

- [Getting Started Guide](getting-started.md)
- [Monitoring Guide](monitoring.md)
- [Troubleshooting Guide](troubleshooting.md)
- [ADR-006: Error & Rate Limit Handling](adr/ADR-006-esi-error-rate-limit-handling.md)
- [ADR-007: Caching Strategy](adr/ADR-007-esi-caching-strategy.md)

## License

MIT License - See [LICENSE](../LICENSE)
