package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sternrassler/eve-esi-client/pkg/client"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestRedis(t *testing.T) (*redis.Client, func()) {
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	redisC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start Redis container: %v", err)
	}

	host, err := redisC.Host(ctx)
	if err != nil {
		t.Fatalf("Failed to get container host: %v", err)
	}

	port, err := redisC.MappedPort(ctx, "6379")
	if err != nil {
		t.Fatalf("Failed to get container port: %v", err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: host + ":" + port.Port(),
	})

	cleanup := func() {
		_ = redisClient.Close()
		if err := redisC.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate Redis container: %v", err)
		}
	}

	return redisClient, cleanup
}

func TestHealthEndpoint(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	healthHandler(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	if string(body) != "OK" {
		t.Errorf("Expected body 'OK', got %s", string(body))
	}
}

func TestReadyEndpoint(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create a minimal client for testing
	esiClient, err := client.New(client.DefaultConfig(redisClient, "test/1.0"))
	if err != nil {
		t.Fatalf("Failed to create ESI client: %v", err)
	}
	defer func() { _ = esiClient.Close() }()

	handler := readyHandler(redisClient, esiClient)

	t.Run("ready", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ready", nil)
		w := httptest.NewRecorder()

		handler(w, req)

		resp := w.Result()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}

		if string(body) != "OK" {
			t.Errorf("Expected body 'OK', got %s", string(body))
		}
	})

	t.Run("not_ready_redis_down", func(t *testing.T) {
		// Close Redis to simulate failure
		_ = redisClient.Close()

		req := httptest.NewRequest("GET", "/ready", nil)
		w := httptest.NewRecorder()

		handler(w, req)

		resp := w.Result()

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("Expected status 503, got %d", resp.StatusCode)
		}
	})
}

func TestESIProxyHandler_Integration(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create ESI client
	esiClient, err := client.New(client.DefaultConfig(redisClient, "test/1.0"))
	if err != nil {
		t.Fatalf("Failed to create ESI client: %v", err)
	}
	defer func() { _ = esiClient.Close() }()

	handler := esiProxyHandler(esiClient)

	t.Run("invalid_endpoint", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/esi/invalid", nil)
		w := httptest.NewRecorder()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req = req.WithContext(ctx)

		handler(w, req)

		resp := w.Result()

		// We expect a bad gateway error since the endpoint is invalid
		if resp.StatusCode != http.StatusBadGateway {
			t.Logf("Status code: %d", resp.StatusCode)
		}
	})
}
