# Troubleshooting Guide

Common issues and solutions for the EVE ESI Client.

## Table of Contents

- [Request Issues](#request-issues)
- [Rate Limiting](#rate-limiting)
- [Caching Issues](#caching-issues)
- [Connection Problems](#connection-problems)
- [Performance Issues](#performance-issues)
- [Error Messages](#error-messages)
- [Debugging](#debugging)

## Request Issues

### "Request blocked: rate limit critical"

**Symptom**: Requests fail with rate limit block error.

**Cause**: ESI error limit is critically low (< ErrorThreshold).

**Solution**:

1. Wait for the rate limit to reset (check logs for `TimeUntilReset`)
2. Review recent logs to identify what's causing errors:
   ```bash
   # Look for error responses
   grep "ESI request error" app.log
   ```
3. Reduce request rate or increase `ErrorThreshold` if too conservative
4. Check if you're hitting invalid endpoints (404 errors count against limit)

**Prevention**:
```go
// Use higher threshold for safety margin
cfg.ErrorThreshold = 15  // Instead of default 10
```

### "Context deadline exceeded"

**Symptom**: Requests timeout.

**Cause**: Request took longer than context timeout.

**Solution**:

1. Increase context timeout:
   ```go
   ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
   defer cancel()
   ```

2. Check if ESI is slow (check ESI status page)
3. Verify network connectivity
4. Check if retry logic is causing delays

**For batch operations:**
```go
// Set appropriate timeout per request
for _, endpoint := range endpoints {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    resp, err := esiClient.Get(ctx, endpoint)
    cancel() // Cancel immediately after request
    
    if errors.Is(err, context.DeadlineExceeded) {
        log.Printf("Timeout on %s, continuing...", endpoint)
        continue
    }
}
```

### "Failed to parse JSON"

**Symptom**: JSON unmarshaling fails.

**Cause**: Response body is not valid JSON or doesn't match expected structure.

**Solution**:

1. Check response status code first:
   ```go
   if resp.StatusCode != 200 {
       body, _ := io.ReadAll(resp.Body)
       log.Printf("Non-200 response: %d - %s", resp.StatusCode, string(body))
       return
   }
   ```

2. Log raw response for inspection:
   ```go
   body, _ := io.ReadAll(resp.Body)
   log.Printf("Raw response: %s", string(body))
   ```

3. Verify endpoint URL is correct
4. Check ESI documentation for response structure changes

## Rate Limiting

### Constant rate limit warnings

**Symptom**: Frequent "ESI error limit warning" log messages.

**Cause**: Application is generating too many errors.

**Solution**:

1. Check error distribution:
   ```promql
   # In Prometheus
   rate(esi_errors_total[5m])
   ```

2. Identify error sources:
   ```bash
   # Check which endpoints are failing
   grep "ESI request error" app.log | cut -d' ' -f5 | sort | uniq -c
   ```

3. Common causes:
   - Invalid endpoint URLs (404 errors)
   - Malformed requests (400 errors)
   - Server issues (5xx errors - temporary)
   - Rate limit (520 errors)

4. Fix based on error type:
   - 4xx: Fix request parameters/endpoints
   - 5xx: Implement retry with backoff (already done automatically)
   - 520: Reduce request rate

### Rate limit state not shared between instances

**Symptom**: Multiple instances don't coordinate rate limiting.

**Cause**: Not using shared Redis or Redis connection issues.

**Solution**:

1. Verify all instances use same Redis:
   ```go
   // All instances should point to same Redis
   redisClient := redis.NewClient(&redis.Options{
       Addr: "shared-redis:6379",
   })
   ```

2. Test Redis connectivity:
   ```go
   err := redisClient.Ping(context.Background()).Err()
   if err != nil {
       log.Fatalf("Redis not accessible: %v", err)
   }
   ```

3. Check Redis keys exist:
   ```bash
   redis-cli keys "esi:rate_limit:*"
   ```

## Caching Issues

### Cache hit rate is 0%

**Symptom**: `esi_cache_hits_total` metric is always 0.

**Cause**: Cache not working or TTL too short.

**Solution**:

1. Verify Redis is working:
   ```bash
   redis-cli
   > keys "esi:cache:*"
   > ttl "esi:cache:some-key"
   ```

2. Check if `Expires` header is being honored:
   ```go
   // Must be true
   cfg.RespectExpires = true
   ```

3. Verify requests are identical (same endpoint + params):
   ```go
   // These are different cache keys:
   "/v1/markets/10000002/orders/"
   "/v1/markets/10000002/orders/?page=1"
   ```

4. Check cache TTL in logs:
   ```json
   {"endpoint":"/v1/status/","ttl":299.5,"message":"Cached response"}
   ```

### 304 Not Modified but still downloading data

**Symptom**: Seeing 304 responses but bandwidth usage is high.

**Cause**: This is normal - 304 means server validated, but client must have cached data.

**How it works**:
```
1. Client has cached data with ETag "abc123"
2. Client sends request with If-None-Match: "abc123"
3. Server checks, data unchanged
4. Server returns 304 Not Modified (small response, ~200 bytes)
5. Client returns cached data to user (from Redis)
```

**Bandwidth saved**: Instead of 100KB response, client receives 200 byte response.

### Cache not expiring

**Symptom**: Seeing stale data even after ESI `Expires` time.

**Cause**: Cache expiration not checked or Redis TTL not set.

**Solution**:

1. Check cache entry expiration:
   ```go
   entry, err := esiClient.GetCache().Get(ctx, cacheKey)
   if entry != nil {
       log.Printf("Expires: %s, IsExpired: %v", 
           entry.Expires, entry.IsExpired())
   }
   ```

2. Verify Redis TTL:
   ```bash
   redis-cli
   > ttl "esi:cache:v1:markets:10000002:orders"
   ```

3. Check if system time is correct:
   ```bash
   date
   # Should match current UTC time
   ```

## Connection Problems

### "Connection refused" to Redis

**Symptom**: Cannot connect to Redis server.

**Solution**:

1. Verify Redis is running:
   ```bash
   # Docker
   docker ps | grep redis
   
   # Service
   systemctl status redis
   ```

2. Test connectivity:
   ```bash
   redis-cli -h localhost -p 6379 ping
   # Should return PONG
   ```

3. Check firewall rules:
   ```bash
   # Allow Redis port
   sudo ufw allow 6379/tcp
   ```

4. Verify Docker network (if using containers):
   ```bash
   docker network inspect bridge
   ```

### "i/o timeout" errors

**Symptom**: Redis operations timing out.

**Cause**: Network latency or Redis overload.

**Solution**:

1. Increase Redis timeouts:
   ```go
   redisClient := redis.NewClient(&redis.Options{
       Addr:         "redis:6379",
       DialTimeout:  10 * time.Second,
       ReadTimeout:  5 * time.Second,
       WriteTimeout: 5 * time.Second,
   })
   ```

2. Check Redis performance:
   ```bash
   redis-cli --latency
   redis-cli --stat
   ```

3. Monitor Redis memory:
   ```bash
   redis-cli info memory
   ```

## Performance Issues

### Slow response times

**Symptom**: Requests taking longer than expected.

**Diagnosis**:

1. Check request duration metrics:
   ```promql
   histogram_quantile(0.95, rate(esi_request_duration_seconds_bucket[5m]))
   ```

2. Breakdown latency sources:
   - Cache lookup: ~1-5ms
   - ESI request: ~100-500ms
   - Retry delays: 1-10s
   - Rate limit throttling: 1s

**Solutions**:

1. Optimize cache usage:
   ```go
   // Batch similar requests
   // Use goroutines for parallel requests (respects MaxConcurrency)
   ```

2. Check if retries are happening:
   ```promql
   rate(esi_retries_total[5m])
   ```

3. Verify network path to ESI:
   ```bash
   traceroute esi.evetech.net
   ping esi.evetech.net
   ```

### High memory usage

**Symptom**: Application memory growing continuously.

**Cause**: Response bodies not being closed or cache growing too large.

**Solution**:

1. Always close response bodies:
   ```go
   resp, err := esiClient.Get(ctx, endpoint)
   if err != nil {
       return err
   }
   defer resp.Body.Close() // IMPORTANT!
   
   // Read body...
   ```

2. Monitor Redis memory:
   ```bash
   redis-cli info memory | grep used_memory_human
   ```

3. Set Redis maxmemory:
   ```bash
   # In redis.conf
   maxmemory 2gb
   maxmemory-policy allkeys-lru
   ```

4. Profile application:
   ```go
   import _ "net/http/pprof"
   
   go http.ListenAndServe("localhost:6060", nil)
   // Visit http://localhost:6060/debug/pprof/heap
   ```

## Error Messages

### Common Error Messages and Solutions

| Error | Cause | Solution |
|-------|-------|----------|
| `redis: nil` | Redis client not initialized | Pass valid Redis client to config |
| `user-agent is required` | Missing UserAgent in config | Set `cfg.UserAgent` |
| `respect_expires must be true` | ESI compliance violation | Set `cfg.RespectExpires = true` |
| `error_threshold must be >= 5` | Invalid threshold | Set `cfg.ErrorThreshold >= 5` |
| `ESI returned 420` | Rate limited by ESI | Slow down requests |
| `ESI returned 520` | Endpoint rate limit | Reduce requests to specific endpoint |

## Debugging

### Enable Debug Logging

```go
import (
    "github.com/Sternrassler/eve-esi-client/pkg/logging"
    "github.com/rs/zerolog"
)

// Set global log level
zerolog.SetGlobalLevel(zerolog.DebugLevel)

// Or configure logger
logger := logging.Setup(logging.Config{
    Level:  logging.LevelDebug,
    Pretty: true, // Human-readable output
})
```

### Check ESI Status

Before debugging client issues, verify ESI is operational:

```bash
curl -v https://esi.evetech.net/v1/status/
```

Look for:
- Status code 200
- `X-ESI-Error-Limit-Remain` header
- `X-ESI-Error-Limit-Reset` header
- Valid JSON response

### Monitor Metrics

Enable Prometheus metrics endpoint:

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

http.Handle("/metrics", promhttp.Handler())
go http.ListenAndServe(":9090", nil)
```

Key metrics to watch:
- `esi_errors_remaining` - Should stay > 20
- `esi_cache_hits_total` - Should increase
- `esi_request_duration_seconds` - P95 should be < 1s
- `esi_rate_limit_blocks_total` - Should be 0 or very low

### Trace Requests

Add request IDs for tracing:

```go
import "github.com/google/uuid"

ctx := context.Background()
requestID := uuid.New().String()
ctx = context.WithValue(ctx, "request_id", requestID)

log.Printf("[%s] Starting request to %s", requestID, endpoint)
resp, err := esiClient.Get(ctx, endpoint)
log.Printf("[%s] Completed: status=%d err=%v", requestID, resp.StatusCode, err)
```

### Test with Mock Server

Use the provided mock ESI server for testing:

```go
import "github.com/Sternrassler/eve-esi-client/internal/testutil"

mockESI := testutil.NewMockESI()
defer mockESI.Close()

// Configure response
mockESI.SetResponse("/v1/status/", testutil.NewHealthyResponse(`{"status": "ok"}`))

// Use mock in tests
cfg := client.DefaultConfig(redisClient, "TestApp/1.0.0")
esiClient, _ := client.New(cfg)

// Override HTTP client to use mock
esiClient.SetHTTPClient(&http.Client{
    Transport: &testTransport{mockServer: mockESI},
})
```

## Getting Help

If you can't resolve the issue:

1. **Check existing issues**: [GitHub Issues](https://github.com/Sternrassler/eve-esi-client/issues)
2. **Ask in discussions**: [GitHub Discussions](https://github.com/Sternrassler/eve-esi-client/discussions)
3. **Read ESI docs**: [ESI Documentation](https://docs.esi.evetech.net/)

When reporting issues, include:
- Error message and full stack trace
- Configuration (sanitize secrets!)
- Logs (with debug level enabled)
- Metrics screenshots if available
- Steps to reproduce

## See Also

- [Getting Started Guide](getting-started.md)
- [Configuration Guide](configuration.md)
- [Monitoring Guide](monitoring.md)
- [Example Code](../examples/library-usage/)

## License

MIT License - See [LICENSE](../LICENSE)
