package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	// ErrCacheMiss indicates the requested key was not found in cache
	ErrCacheMiss = errors.New("cache miss")

	// ErrInvalidEntry indicates the cache entry is invalid or corrupted
	ErrInvalidEntry = errors.New("invalid cache entry")
)

// Manager handles caching operations with Redis backend.
type Manager struct {
	redis *redis.Client
}

// NewManager creates a new cache manager with Redis backend.
func NewManager(redisClient *redis.Client) *Manager {
	if redisClient == nil {
		panic("redis client cannot be nil")
	}
	return &Manager{
		redis: redisClient,
	}
}

// Get retrieves a cache entry by key.
// Returns ErrCacheMiss if the key doesn't exist or entry is expired.
func (m *Manager) Get(ctx context.Context, key CacheKey) (*CacheEntry, error) {
	cacheKey := key.String()

	// Get data from Redis
	data, err := m.redis.Get(ctx, cacheKey).Bytes()
	if err != nil {
		if err == redis.Nil {
			CacheMisses.Inc()
			return nil, ErrCacheMiss
		}
		CacheErrors.WithLabelValues("get").Inc()
		return nil, fmt.Errorf("redis get: %w", err)
	}

	// Unmarshal entry
	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		CacheErrors.WithLabelValues("get").Inc()
		return nil, fmt.Errorf("%w: %v", ErrInvalidEntry, err)
	}

	// Check if expired
	if entry.IsExpired() {
		// Delete expired entry
		_ = m.Delete(ctx, key)
		CacheMisses.Inc()
		return nil, ErrCacheMiss
	}

	// Cache hit
	CacheHits.WithLabelValues("redis").Inc()
	CacheSize.WithLabelValues("redis").Add(float64(len(data)))

	return &entry, nil
}

// Set stores a cache entry with TTL based on the entry's Expires field.
// The entry will be automatically removed from Redis when it expires.
func (m *Manager) Set(ctx context.Context, key CacheKey, entry *CacheEntry) error {
	if entry == nil {
		return fmt.Errorf("cache entry cannot be nil")
	}

	cacheKey := key.String()

	// Calculate TTL
	ttl := entry.TTL()
	if ttl <= 0 {
		// Already expired, don't cache
		return nil
	}

	// Marshal entry
	data, err := json.Marshal(entry)
	if err != nil {
		CacheErrors.WithLabelValues("set").Inc()
		return fmt.Errorf("marshal cache entry: %w", err)
	}

	// Store in Redis with TTL
	if err := m.redis.Set(ctx, cacheKey, data, ttl).Err(); err != nil {
		CacheErrors.WithLabelValues("set").Inc()
		return fmt.Errorf("redis set: %w", err)
	}

	// Update cache size metric
	CacheSize.WithLabelValues("redis").Add(float64(len(data)))

	return nil
}

// Delete removes a cache entry.
func (m *Manager) Delete(ctx context.Context, key CacheKey) error {
	cacheKey := key.String()

	if err := m.redis.Del(ctx, cacheKey).Err(); err != nil {
		CacheErrors.WithLabelValues("delete").Inc()
		return fmt.Errorf("redis del: %w", err)
	}

	return nil
}

// UpdateTTL updates the TTL of an existing cache entry.
// This is useful when receiving a 304 Not Modified response with a new expires header.
func (m *Manager) UpdateTTL(ctx context.Context, key CacheKey, newExpires time.Time) error {
	// Get existing entry
	entry, err := m.Get(ctx, key)
	if err != nil {
		return err
	}

	// Update expires time
	entry.Expires = newExpires

	// Re-save with new TTL
	return m.Set(ctx, key, entry)
}
