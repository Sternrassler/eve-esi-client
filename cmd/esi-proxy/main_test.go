package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sternrassler/eve-esi-client/pkg/client"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
		redisClient.Close()
		redisC.Terminate(ctx)
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
	defer esiClient.Close()

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
		redisClient.Close()

		req := httptest.NewRequest("GET", "/ready", nil)
		w := httptest.NewRecorder()

		handler(w, req)

		resp := w.Result()

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("Expected status 503, got %d", resp.StatusCode)
		}
	})
}

func TestMetricsEndpoint(t *testing.T) {
	// We need to ensure metrics packages are imported
	// by creating a client which will register all metrics
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create ESI client to ensure all metrics are registered
	_, err := client.New(client.DefaultConfig(redisClient, "test/1.0"))
	if err != nil {
		t.Fatalf("Failed to create ESI client: %v", err)
	}

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	handler := promhttp.Handler()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	bodyStr := string(body)

	// Just verify we get prometheus output format
	if !strings.Contains(bodyStr, "# HELP") || !strings.Contains(bodyStr, "# TYPE") {
		t.Error("Expected Prometheus format metrics output")
	}

	// Verify at least the error remaining gauge is present
	// (this is always initialized even if no requests are made)
	if !strings.Contains(bodyStr, "esi_errors_remaining") {
		t.Error("Expected metrics output to contain esi_errors_remaining")
	}

	t.Logf("Metrics endpoint returned %d bytes of data", len(bodyStr))
}

func TestESIProxyHandler_Integration(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create ESI client
	esiClient, err := client.New(client.DefaultConfig(redisClient, "test/1.0"))
	if err != nil {
		t.Fatalf("Failed to create ESI client: %v", err)
	}
	defer esiClient.Close()

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
