package cache

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// setupTestRedis creates a test Redis client for testing.
// For unit tests, we use miniredis (in-memory). For integration tests,
// we would use testcontainers-go with a real Redis instance.
func setupTestRedis(t *testing.T) *redis.Client {
	t.Helper()

	// For now, use a simple Redis client that connects to localhost
	// In production tests, this should use testcontainers-go
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // Use a separate DB for tests
	})

	// Ping to check connection
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available for testing: %v", err)
	}

	// Flush test DB before each test
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("Failed to flush test DB: %v", err)
	}

	t.Cleanup(func() {
		client.FlushDB(context.Background())
		client.Close()
	})

	return client
}

func TestNewManager(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer client.Close()

	manager := NewManager(client)
	if manager == nil {
		t.Fatal("NewManager returned nil")
	}
	if manager.redis != client {
		t.Error("Manager redis client not set correctly")
	}
}

func TestNewManager_Panic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewManager should panic with nil redis client")
		}
	}()
	NewManager(nil)
}

func TestManager_SetAndGet(t *testing.T) {
	client := setupTestRedis(t)
	manager := NewManager(client)
	ctx := context.Background()

	key := CacheKey{
		Endpoint: "/v1/test/endpoint/",
	}

	entry := &CacheEntry{
		Data:         []byte(`{"test": "data"}`),
		ETag:         `"abc123"`,
		Expires:      time.Now().Add(5 * time.Minute),
		LastModified: time.Now().Add(-1 * time.Hour),
		StatusCode:   200,
		Headers:      http.Header{"Content-Type": []string{"application/json"}},
		CachedAt:     time.Now(),
	}

	// Set entry
	if err := manager.Set(ctx, key, entry); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Get entry
	retrieved, err := manager.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	// Verify data
	if string(retrieved.Data) != string(entry.Data) {
		t.Errorf("Data mismatch: got %s, want %s", retrieved.Data, entry.Data)
	}
	if retrieved.ETag != entry.ETag {
		t.Errorf("ETag mismatch: got %s, want %s", retrieved.ETag, entry.ETag)
	}
	if retrieved.StatusCode != entry.StatusCode {
		t.Errorf("StatusCode mismatch: got %d, want %d", retrieved.StatusCode, entry.StatusCode)
	}
}

func TestManager_Get_CacheMiss(t *testing.T) {
	client := setupTestRedis(t)
	manager := NewManager(client)
	ctx := context.Background()

	key := CacheKey{
		Endpoint: "/v1/nonexistent/",
	}

	_, err := manager.Get(ctx, key)
	if err != ErrCacheMiss {
		t.Errorf("Expected ErrCacheMiss, got %v", err)
	}
}

func TestManager_Get_ExpiredEntry(t *testing.T) {
	client := setupTestRedis(t)
	manager := NewManager(client)
	ctx := context.Background()

	key := CacheKey{
		Endpoint: "/v1/test/",
	}

	// Create already expired entry
	entry := &CacheEntry{
		Data:    []byte(`{"test": "data"}`),
		Expires: time.Now().Add(-1 * time.Hour), // Already expired
	}

	// Set should not cache expired entries
	if err := manager.Set(ctx, key, entry); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Get should return cache miss
	_, err := manager.Get(ctx, key)
	if err != ErrCacheMiss {
		t.Errorf("Expected ErrCacheMiss for expired entry, got %v", err)
	}
}

func TestManager_Delete(t *testing.T) {
	client := setupTestRedis(t)
	manager := NewManager(client)
	ctx := context.Background()

	key := CacheKey{
		Endpoint: "/v1/test/",
	}

	entry := &CacheEntry{
		Data:    []byte(`{"test": "data"}`),
		Expires: time.Now().Add(5 * time.Minute),
	}

	// Set entry
	if err := manager.Set(ctx, key, entry); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify it exists
	if _, err := manager.Get(ctx, key); err != nil {
		t.Fatalf("Get after Set failed: %v", err)
	}

	// Delete entry
	if err := manager.Delete(ctx, key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify it's gone
	_, err := manager.Get(ctx, key)
	if err != ErrCacheMiss {
		t.Errorf("Expected ErrCacheMiss after Delete, got %v", err)
	}
}

func TestManager_UpdateTTL(t *testing.T) {
	client := setupTestRedis(t)
	manager := NewManager(client)
	ctx := context.Background()

	key := CacheKey{
		Endpoint: "/v1/test/",
	}

	// Create entry with initial TTL
	entry := &CacheEntry{
		Data:    []byte(`{"test": "data"}`),
		Expires: time.Now().Add(5 * time.Minute),
	}

	if err := manager.Set(ctx, key, entry); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Update TTL to a new expiration time
	newExpires := time.Now().Add(10 * time.Minute)
	if err := manager.UpdateTTL(ctx, key, newExpires); err != nil {
		t.Fatalf("UpdateTTL failed: %v", err)
	}

	// Get entry and verify new expiration
	retrieved, err := manager.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get after UpdateTTL failed: %v", err)
	}

	// Check that the new expires time is close to what we set
	diff := retrieved.Expires.Sub(newExpires)
	if diff < -1*time.Second || diff > 1*time.Second {
		t.Errorf("Expires time not updated correctly: got %v, want %v (diff: %v)",
			retrieved.Expires, newExpires, diff)
	}
}

func TestManager_Set_NilEntry(t *testing.T) {
	client := setupTestRedis(t)
	manager := NewManager(client)
	ctx := context.Background()

	key := CacheKey{
		Endpoint: "/v1/test/",
	}

	err := manager.Set(ctx, key, nil)
	if err == nil {
		t.Error("Set with nil entry should return error")
	}
}
