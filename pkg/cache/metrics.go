package cache

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// CacheHits tracks cache hits by layer (redis)
	CacheHits = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "esi_cache_hits_total",
			Help: "Total number of ESI cache hits",
		},
		[]string{"layer"}, // "redis"
	)

	// CacheMisses tracks cache misses
	CacheMisses = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "esi_cache_misses_total",
			Help: "Total number of ESI cache misses",
		},
	)

	// CacheSize tracks cache size in bytes by layer
	CacheSize = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "esi_cache_size_bytes",
			Help: "Current size of ESI cache in bytes",
		},
		[]string{"layer"}, // "redis"
	)

	// NotModifiedResponses tracks 304 Not Modified responses
	NotModifiedResponses = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "esi_304_responses_total",
			Help: "Total number of ESI 304 Not Modified responses",
		},
	)

	// ConditionalRequestsSent tracks conditional requests sent with If-None-Match
	ConditionalRequestsSent = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "esi_conditional_requests_total",
			Help: "Total number of conditional requests sent with If-None-Match",
		},
	)

	// CacheErrors tracks cache operation errors
	CacheErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "esi_cache_errors_total",
			Help: "Total number of cache operation errors",
		},
		[]string{"operation"}, // "get", "set", "delete"
	)
)
