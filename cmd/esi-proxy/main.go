package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Sternrassler/eve-esi-client/pkg/client"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Configuration from environment
	redisURL := getEnv("REDIS_URL", "localhost:6379")
	port := getEnv("PORT", "8080")
	userAgent := getEnv("USER_AGENT", "eve-esi-client/0.1.0")

	// Setup Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisURL,
	})

	// Ping Redis
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Printf("Connected to Redis at %s", redisURL)

	// Create ESI client
	esiClient, err := client.New(client.DefaultConfig(redisClient, userAgent))
	if err != nil {
		log.Fatalf("Failed to create ESI client: %v", err)
	}
	defer esiClient.Close()

	// HTTP Server
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/esi/", esiProxyHandler(esiClient))

	addr := ":" + port
	log.Printf("Starting ESI proxy server on %s", addr)
	log.Printf("User-Agent: %s", userAgent)
	
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK")
}

func esiProxyHandler(esiClient *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract ESI endpoint from request path
		// Example: /esi/v4/markets/10000002/orders/ -> /v4/markets/10000002/orders/
		endpoint := r.URL.Path[4:] // Remove "/esi" prefix

		// Proxy request to ESI
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		resp, err := esiClient.Get(ctx, endpoint)
		if err != nil {
			http.Error(w, fmt.Sprintf("ESI request failed: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Copy response headers
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		// Copy status code
		w.WriteHeader(resp.StatusCode)

		// Copy body
		if _, err := w.Write([]byte("TODO: Copy response body")); err != nil {
			log.Printf("Failed to write response: %v", err)
		}
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
