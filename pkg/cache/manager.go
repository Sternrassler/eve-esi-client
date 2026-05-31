package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	// ErrCacheMiss indicates the requested key was not found in cache
	ErrCacheMiss = errors.New("cache miss")

	// ErrInvalidEntry indicates the cache entry is invalid or corrupted
	ErrInvalidEntry = errors.New("invalid cache entry")
)

// Manager handles caching operations with Redis backend.
type Manager struct {
	redis  *redis.Client
	logger zerolog.Logger
}

// NewManager creates a new cache manager with Redis backend.
// It logs internal failures (e.g. a stale-entry delete that fails) via the
// package's zerolog logger; use SetLogger to inject a component-scoped logger.
func NewManager(redisClient *redis.Client) *Manager {
	if redisClient == nil {
		panic("redis client cannot be nil")
	}
	return &Manager{
		redis:  redisClient,
		logger: log.With().Str("component", "esi-cache").Logger(),
	}
}

// SetLogger injects a logger so internal warnings (e.g. failed stale-entry
// deletes) are attributed to the caller's logging context instead of being
// silently discarded.
func (m *Manager) SetLogger(logger zerolog.Logger) {
	m.logger = logger
}

// Get retrieves a cache entry by key.
// Returns ErrCacheMiss if the key doesn't exist or entry is expired.
func (m *Manager) Get(ctx context.Context, key CacheKey) (*CacheEntry, error) {
	cacheKey := key.String()

	// Get data from Redis
	data, err := m.redis.Get(ctx, cacheKey).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, ErrCacheMiss
		}
		return nil, fmt.Errorf("redis get: %w", err)
	}

	// Unmarshal entry
	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEntry, err)
	}

	// Check if expired
	if entry.IsExpired() {
		// Delete expired entry. A failed delete leaves a stale entry behind, so
		// log it loudly instead of discarding the error silently. The cache result
		// is still ErrCacheMiss (the entry is expired regardless).
		if derr := m.Delete(ctx, key); derr != nil {
			m.logger.Warn().Err(derr).Str("key", cacheKey).
				Msg("Failed to delete expired cache entry; stale entry may remain")
		}
		return nil, ErrCacheMiss
	}

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
		return fmt.Errorf("marshal cache entry: %w", err)
	}

	// Store in Redis with TTL
	if err := m.redis.Set(ctx, cacheKey, data, ttl).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}

	return nil
}

// Delete removes a cache entry.
func (m *Manager) Delete(ctx context.Context, key CacheKey) error {
	cacheKey := key.String()

	if err := m.redis.Del(ctx, cacheKey).Err(); err != nil {
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
