# Monitoring & Observability Guide

Complete guide to monitoring the EVE ESI Client with Prometheus metrics, structured logging, and health checks.

## Table of Contents

- [Prometheus Metrics](#prometheus-metrics)
- [Structured Logging](#structured-logging)
- [Health Checks](#health-checks)
- [Alerting](#alerting)
- [Dashboards](#dashboards)
- [Performance Monitoring](#performance-monitoring)

## Prometheus Metrics

### Exposing Metrics

The client automatically registers Prometheus metrics. Expose them via HTTP:

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

// Expose metrics endpoint
http.Handle("/metrics", promhttp.Handler())
go http.ListenAndServe(":9090", nil)
```

Access metrics at `http://localhost:9090/metrics`.

### Available Metrics

#### Rate Limit Metrics

**`esi_errors_remaining` (Gauge)**
- Current number of errors remaining in ESI rate limit window
- **Labels**: None
- **Alert on**: < 20 (warning), < 5 (critical)

**`esi_rate_limit_blocks_total` (Counter)**
- Total requests blocked due to critical rate limit state
- **Labels**: None
- **Should be**: 0 or very low
- **Alert on**: Increasing trend

**`esi_rate_limit_throttles_total` (Counter)**
- Total requests throttled due to warning rate limit state
- **Labels**: None
- **Info**: Occasional throttles are normal

**`esi_rate_limit_resets_total` (Counter)**
- Total number of rate limit window resets
- **Labels**: None
- **Expected**: ~60 per hour (ESI resets every 60s)

#### Cache Metrics

**`esi_cache_hits_total` (Counter)**
- Total cache hits by layer
- **Labels**: `layer` (redis)
- **Target**: High hit rate (>60%)

**`esi_cache_misses_total` (Counter)**
- Total cache misses
- **Labels**: None
- **Info**: First request to endpoint always misses

**`esi_cache_size_bytes` (Gauge)**
- Current cache size in bytes
- **Labels**: `layer` (redis)
- **Monitor**: Should grow then stabilize

**`esi_304_responses_total` (Counter)**
- Total 304 Not Modified responses (cache validation)
- **Labels**: None
- **Info**: Indicates efficient caching

**`esi_conditional_requests_total` (Counter)**
- Total conditional requests sent with If-None-Match
- **Labels**: None
- **Expected**: ≈ (requests - cache_misses)

**`esi_cache_errors_total` (Counter)**
- Cache operation errors
- **Labels**: `operation` (get, set, delete)
- **Alert on**: Increasing trend (indicates Redis issues)

#### Request Metrics

**`esi_requests_total` (Counter)**
- Total ESI requests by endpoint and status
- **Labels**: `endpoint`, `status`
- **Use**: Track which endpoints are used most

**`esi_request_duration_seconds` (Histogram)**
- Request duration distribution
- **Labels**: `endpoint`
- **Buckets**: 0.1, 0.5, 1, 2, 5, 10 seconds
- **Target**: P95 < 1s

**`esi_errors_total` (Counter)**
- Total errors by classification
- **Labels**: `class` (client, server, rate_limit, network)
- **Alert on**: High `client` errors (bad requests)

#### Retry Metrics

**`esi_retries_total` (Counter)**
- Total retry attempts by error class
- **Labels**: `error_class`
- **Info**: Server/network errors should retry

**`esi_retry_backoff_seconds` (Histogram)**
- Backoff duration distribution
- **Labels**: `error_class`
- **Buckets**: 0.5, 1, 2, 5, 10, 30, 60 seconds

**`esi_retry_exhausted_total` (Counter)**
- Times max retries were exhausted
- **Labels**: `error_class`
- **Alert on**: High rate (tune retry config)

### Example Prometheus Queries

#### Cache Hit Rate

```promql
sum(rate(esi_cache_hits_total[5m])) / 
(sum(rate(esi_cache_hits_total[5m])) + sum(rate(esi_cache_misses_total[5m])))
```

#### Error Limit Status

```promql
esi_errors_remaining
```

**Alert when < 20:**
```promql
esi_errors_remaining < 20
```

#### Request Error Rate

```promql
sum(rate(esi_errors_total[5m])) by (class)
```

#### P95 Request Latency

```promql
histogram_quantile(0.95, rate(esi_request_duration_seconds_bucket[5m]))
```

#### P99 Request Latency by Endpoint

```promql
histogram_quantile(0.99, 
  rate(esi_request_duration_seconds_bucket[5m])) by (endpoint)
```

#### 304 Not Modified Rate

```promql
rate(esi_304_responses_total[5m]) / rate(esi_conditional_requests_total[5m])
```

#### Request Success Rate

```promql
sum(rate(esi_requests_total{status=~"2.."}[5m])) /
sum(rate(esi_requests_total[5m]))
```

#### Top Endpoints by Request Count

```promql
topk(10, sum(rate(esi_requests_total[5m])) by (endpoint))
```

#### Rate Limit Block Rate

```promql
rate(esi_rate_limit_blocks_total[5m])
```

## Structured Logging

The client uses [zerolog](https://github.com/rs/zerolog) for structured JSON logging.

### Log Levels

| Level | Usage | Example |
|-------|-------|---------|
| **Debug** | Cache operations, request flow | Cache hit, conditional request sent |
| **Info** | Successful operations | Request completed, rate limit updated |
| **Warn** | Non-critical issues | Rate limit warning, retry attempt |
| **Error** | Critical failures | Rate limit block, request failed |

### Log Format

All logs are JSON with consistent fields:

```json
{
  "level": "info",
  "component": "esi-client",
  "endpoint": "/v1/markets/10000002/orders/",
  "status_code": 200,
  "duration": 123.45,
  "time": "2025-01-01T12:00:00Z",
  "message": "Request completed"
}
```

### Common Log Fields

| Field | Type | Description |
|-------|------|-------------|
| `level` | string | Log level (debug, info, warn, error) |
| `component` | string | Component name (esi-client, rate-limiter, cache) |
| `endpoint` | string | ESI endpoint path |
| `status_code` | int | HTTP status code |
| `duration` | float | Request duration in milliseconds |
| `error_class` | string | Error classification |
| `cache_hit` | bool | Whether cache was hit |
| `errors_remaining` | int | Current ESI error limit |
| `etag` | string | ETag for conditional requests |
| `time` | string | ISO 8601 timestamp |
| `message` | string | Human-readable message |

### Configuring Logging

```go
import (
    "github.com/Sternrassler/eve-esi-client/pkg/logging"
    "github.com/rs/zerolog"
)

// Production: JSON logging
logger := logging.Setup(logging.Config{
    Level:  logging.LevelInfo,
    Pretty: false,
})

// Development: Pretty console output
logger := logging.Setup(logging.Config{
    Level:  logging.LevelDebug,
    Pretty: true,
})
```

### Parsing Logs

**Count errors by endpoint:**
```bash
cat app.log | jq -r 'select(.level=="error") | .endpoint' | sort | uniq -c
```

**Average request duration:**
```bash
cat app.log | jq -r 'select(.duration) | .duration' | awk '{sum+=$1; count++} END {print sum/count}'
```

**Cache hit rate from logs:**
```bash
grep "cache_hit" app.log | jq -r '.cache_hit' | 
  awk '{hits+=$1; total++} END {print hits/total*100 "%"}'
```

**Rate limit over time:**
```bash
cat app.log | jq -r 'select(.errors_remaining) | 
  "\(.time) \(.errors_remaining)"'
```

## Health Checks

### Readiness Check

Implement a readiness check for Kubernetes/load balancers:

```go
func readinessHandler(esiClient *client.Client) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
        defer cancel()

        // Check Redis connection
        if err := esiClient.GetRedis().Ping(ctx).Err(); err != nil {
            w.WriteHeader(http.StatusServiceUnavailable)
            json.NewEncoder(w).Encode(map[string]string{
                "status": "unhealthy",
                "reason": "redis_down",
            })
            return
        }

        // Check rate limit state
        state, err := esiClient.GetRateLimiter().GetState(ctx)
        if err != nil {
            w.WriteHeader(http.StatusServiceUnavailable)
            json.NewEncoder(w).Encode(map[string]string{
                "status": "unhealthy",
                "reason": "rate_limit_check_failed",
            })
            return
        }

        // Service is healthy
        w.WriteHeader(http.StatusOK)
        json.NewEncoder(w).Encode(map[string]interface{}{
            "status":           "healthy",
            "errors_remaining": state.ErrorsRemaining,
            "is_healthy":       state.IsHealthy,
        })
    }
}

// Register handler
http.HandleFunc("/ready", readinessHandler(esiClient))
```

### Liveness Check

Simple liveness check:

```go
func livenessHandler(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("OK"))
}

http.HandleFunc("/health", livenessHandler)
```

## Alerting

### Recommended Alerts

#### Critical Alerts

**1. Rate Limit Critical**
```yaml
- alert: ESIRateLimitCritical
  expr: esi_errors_remaining < 5
  for: 1m
  labels:
    severity: critical
  annotations:
    summary: "ESI error limit critically low"
    description: "Only {{ $value }} errors remaining. IP ban risk!"
```

**2. High Error Rate**
```yaml
- alert: ESIHighErrorRate
  expr: rate(esi_errors_total[5m]) > 0.5
  for: 5m
  labels:
    severity: critical
  annotations:
    summary: "High ESI error rate"
    description: "Error rate: {{ $value }} errors/sec"
```

**3. Cache System Down**
```yaml
- alert: ESICacheDown
  expr: rate(esi_cache_errors_total[5m]) > 0.1
  for: 2m
  labels:
    severity: critical
  annotations:
    summary: "ESI cache system failing"
    description: "Cache error rate: {{ $value }}"
```

#### Warning Alerts

**1. Rate Limit Warning**
```yaml
- alert: ESIRateLimitWarning
  expr: esi_errors_remaining < 20
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "ESI error limit warning"
    description: "Only {{ $value }} errors remaining"
```

**2. Low Cache Hit Rate**
```yaml
- alert: ESILowCacheHitRate
  expr: |
    sum(rate(esi_cache_hits_total[5m])) / 
    (sum(rate(esi_cache_hits_total[5m])) + sum(rate(esi_cache_misses_total[5m]))) < 0.4
  for: 10m
  labels:
    severity: warning
  annotations:
    summary: "Low cache hit rate"
    description: "Hit rate: {{ $value | humanizePercentage }}"
```

**3. High Latency**
```yaml
- alert: ESIHighLatency
  expr: histogram_quantile(0.95, rate(esi_request_duration_seconds_bucket[5m])) > 2
  for: 10m
  labels:
    severity: warning
  annotations:
    summary: "High ESI request latency"
    description: "P95 latency: {{ $value }}s"
```

**4. Retry Exhaustion**
```yaml
- alert: ESIRetryExhaustion
  expr: rate(esi_retry_exhausted_total[5m]) > 0.1
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "ESI requests exhausting retries"
    description: "Exhaustion rate: {{ $value }}/sec"
```

### Alert Configuration Example

```yaml
# prometheus-alerts.yml
groups:
  - name: esi_client
    interval: 30s
    rules:
      - alert: ESIRateLimitCritical
        expr: esi_errors_remaining < 5
        for: 1m
        labels:
          severity: critical
          component: esi-client
        annotations:
          summary: "ESI error limit critically low"
          description: "ESI errors remaining: {{ $value }}. Immediate action required to prevent IP ban!"
          runbook_url: "https://docs.example.com/runbooks/esi-rate-limit"

      - alert: ESIRateLimitWarning
        expr: esi_errors_remaining < 20
        for: 5m
        labels:
          severity: warning
          component: esi-client
        annotations:
          summary: "ESI error limit warning"
          description: "ESI errors remaining: {{ $value }}. Review error sources."

      # Add more alerts...
```

## Dashboards

### Grafana Dashboard

See [docs/monitoring/grafana-dashboard.json](monitoring/grafana-dashboard.json) for a complete Grafana dashboard.

**Key Panels:**

1. **Rate Limit Status** - Gauge showing errors remaining
2. **Request Rate** - Requests per second by endpoint
3. **Cache Hit Rate** - Percentage over time
4. **Request Latency** - P50, P95, P99 percentiles
5. **Error Rate** - Errors by classification
6. **Retry Activity** - Retry rate by error class

### Quick Dashboard Setup

```bash
# Import dashboard to Grafana
curl -X POST http://grafana:3000/api/dashboards/db \
  -H "Content-Type: application/json" \
  -d @docs/monitoring/grafana-dashboard.json
```

## Performance Monitoring

### Key Performance Indicators

| Metric | Target | Alert Threshold |
|--------|--------|-----------------|
| Cache Hit Rate | > 60% | < 40% |
| P95 Latency | < 1s | > 2s |
| Error Rate | < 1% | > 5% |
| Errors Remaining | > 50 | < 20 |
| Rate Limit Blocks | 0 | > 0 |

### Performance Checklist

- [ ] Cache hit rate > 60%
- [ ] P95 latency < 1 second
- [ ] Error rate < 1%
- [ ] ESI errors remaining > 50
- [ ] No rate limit blocks
- [ ] Retry exhaustion rate near 0

### Optimization Tips

1. **Improve Cache Hit Rate**
   - Increase cache TTL if appropriate
   - Ensure requests use identical parameters
   - Monitor cache size and eviction

2. **Reduce Latency**
   - Use concurrent requests (respects MaxConcurrency)
   - Optimize Redis connection (use pooling)
   - Reduce retry backoff if safe

3. **Lower Error Rate**
   - Fix invalid endpoints (404 errors)
   - Handle expected errors gracefully
   - Improve request validation

4. **Protect Rate Limit**
   - Increase ErrorThreshold for safety margin
   - Implement request prioritization
   - Add circuit breaker for failing endpoints

## See Also

- [Getting Started Guide](getting-started.md)
- [Configuration Guide](configuration.md)
- [Troubleshooting Guide](troubleshooting.md)
- [Prometheus Alerts](monitoring/prometheus-alerts.md)
- [Grafana Dashboard](monitoring/grafana-dashboard.json)

## License

MIT License - See [LICENSE](../LICENSE)
