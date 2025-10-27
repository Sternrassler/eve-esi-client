# Prometheus Alert Rules

Example alert rules for EVE ESI Client monitoring.

## alerts.yml

```yaml
groups:
  - name: esi_client_alerts
    interval: 30s
    rules:
      # Critical: ESI Error Limit
      - alert: ESIErrorLimitCritical
        expr: esi_errors_remaining < 10
        for: 1m
        labels:
          severity: critical
          component: esi-client
        annotations:
          summary: "ESI error limit critical"
          description: "ESI error limit is at {{ $value }}, requests are being blocked to prevent ban"

      # Warning: ESI Error Limit  
      - alert: ESIErrorLimitWarning
        expr: esi_errors_remaining < 30
        for: 5m
        labels:
          severity: warning
          component: esi-client
        annotations:
          summary: "ESI error limit warning"
          description: "ESI error limit is at {{ $value }}, requests are being throttled"

      # Critical: High Error Rate
      - alert: ESIHighErrorRate
        expr: rate(esi_errors_total[5m]) > 0.1
        for: 5m
        labels:
          severity: warning
          component: esi-client
        annotations:
          summary: "High ESI error rate"
          description: "ESI error rate is {{ $value | humanize }} errors/sec"

      # Warning: Low Cache Hit Rate
      - alert: ESICacheLowHitRate
        expr: |
          sum(rate(esi_cache_hits_total[10m])) / 
          (sum(rate(esi_cache_hits_total[10m])) + sum(rate(esi_cache_misses_total[10m]))) < 0.5
        for: 10m
        labels:
          severity: warning
          component: esi-client
        annotations:
          summary: "Low ESI cache hit rate"
          description: "Cache hit rate is {{ $value | humanizePercentage }}"

      # Critical: Redis Connection Lost
      - alert: ESIRedisDown
        expr: up{job="esi-proxy"} == 0
        for: 1m
        labels:
          severity: critical
          component: esi-client
        annotations:
          summary: "ESI client Redis connection lost"
          description: "Unable to connect to Redis backend"

      # Warning: High Request Latency
      - alert: ESIHighLatency
        expr: histogram_quantile(0.95, rate(esi_request_duration_seconds_bucket[5m])) > 5
        for: 5m
        labels:
          severity: warning
          component: esi-client
        annotations:
          summary: "High ESI request latency"
          description: "P95 latency is {{ $value | humanizeDuration }}"

      # Info: Rate Limit Blocks Occurring
      - alert: ESIRateLimitBlocks
        expr: rate(esi_rate_limit_blocks_total[5m]) > 0
        for: 2m
        labels:
          severity: info
          component: esi-client
        annotations:
          summary: "ESI rate limit blocks occurring"
          description: "{{ $value | humanize }} requests/sec being blocked due to rate limit"

      # Warning: Cache Errors
      - alert: ESICacheErrors
        expr: rate(esi_cache_errors_total[5m]) > 0.01
        for: 5m
        labels:
          severity: warning
          component: esi-client
        annotations:
          summary: "ESI cache errors occurring"
          description: "{{ $value | humanize }} cache errors/sec"
```

## Usage

Add these rules to your Prometheus configuration:

```yaml
# prometheus.yml
rule_files:
  - "alerts.yml"

alerting:
  alertmanagers:
    - static_configs:
        - targets:
            - alertmanager:9093
```

## Alert Severity Levels

- **Critical**: Immediate action required (error limit critical, Redis down)
- **Warning**: Investigation needed (low cache hit rate, high latency)
- **Info**: Informational (rate limit blocks occurring)

## Recommended Actions

### ESIErrorLimitCritical
1. Check if there's an error spike in application logs
2. Verify ESI API status: https://esi.evetech.net/
3. Wait for error limit to reset (tracked in `esi_errors_remaining`)
4. Investigate root cause of errors

### ESIHighErrorRate
1. Check `esi_errors_total{class}` to identify error class
2. Review application logs for error details
3. Verify network connectivity to ESI
4. Check if ESI is experiencing issues

### ESICacheLowHitRate
1. Check if cache TTLs are too short
2. Verify Redis is healthy and not evicting keys
3. Check if request patterns have changed
4. Consider increasing Redis memory limit

### ESIRedisDown
1. Check Redis service status
2. Verify network connectivity
3. Check Redis logs for errors
4. Restart Redis service if needed

### ESIHighLatency
1. Check ESI API status for degraded performance
2. Verify network latency to ESI
3. Review request patterns for inefficiencies
4. Consider increasing timeout values
