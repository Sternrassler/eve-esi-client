// Package metrics provides centralized Prometheus metrics registry for ESI client.
// All metrics are defined in their respective packages (client, cache, ratelimit)
// to maintain modularity and avoid circular dependencies.
//
// This package provides documentation and reference for all available metrics.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Registry is the default Prometheus registry used by the ESI client.
// All metrics are automatically registered via promauto in their respective packages.
var Registry = prometheus.DefaultRegisterer

// Metrics Documentation
//
// Rate Limit Metrics (pkg/ratelimit):
//   - esi_errors_remaining (Gauge): Current errors remaining in ESI rate limit window
//   - esi_rate_limit_blocks_total (Counter): Requests blocked due to critical error limit
//   - esi_rate_limit_throttles_total (Counter): Requests throttled due to warning error limit
//   - esi_rate_limit_resets_total (Counter): Number of error limit resets detected
//
// Cache Metrics (pkg/cache):
//   - esi_cache_hits_total{layer="redis"} (Counter): Cache hits by layer
//   - esi_cache_misses_total (Counter): Cache misses
//   - esi_cache_size_bytes{layer="redis"} (Gauge): Current cache size in bytes
//   - esi_304_responses_total (Counter): 304 Not Modified responses
//   - esi_conditional_requests_total (Counter): Conditional requests sent with If-None-Match
//   - esi_cache_errors_total{operation} (Counter): Cache operation errors
//
// Request Metrics (pkg/client):
//   - esi_requests_total{endpoint, status} (Counter): Total requests by endpoint and HTTP status
//   - esi_request_duration_seconds{endpoint} (Histogram): Request duration by endpoint
//   - esi_errors_total{class} (Counter): Errors by class (client, server, rate_limit, network)
//
// Retry Metrics (pkg/client):
//   - esi_retries_total{error_class} (Counter): Retry attempts by error class
//   - esi_retry_backoff_seconds{error_class} (Histogram): Backoff duration by error class
//   - esi_retry_exhausted_total{error_class} (Counter): Requests that exhausted max retries
//
// Example Prometheus Queries:
//
//   # Cache Hit Rate
//   sum(rate(esi_cache_hits_total[5m])) /
//   (sum(rate(esi_cache_hits_total[5m])) + sum(rate(esi_cache_misses_total[5m])))
//
//   # Error Limit Status
//   esi_errors_remaining < 20
//
//   # Request Error Rate
//   rate(esi_errors_total[5m])
//
//   # P95 Request Latency
//   histogram_quantile(0.95, rate(esi_request_duration_seconds_bucket[5m]))
//
//   # 304 Response Rate
//   rate(esi_304_responses_total[5m]) / rate(esi_requests_total[5m])
