// Package logging provides structured logging configuration using zerolog.
package logging

import (
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// LogLevel represents the logging level.
type LogLevel string

const (
	// LevelDebug logs debug messages and above.
	LevelDebug LogLevel = "debug"

	// LevelInfo logs info messages and above.
	LevelInfo LogLevel = "info"

	// LevelWarn logs warning messages and above.
	LevelWarn LogLevel = "warn"

	// LevelError logs error messages only.
	LevelError LogLevel = "error"
)

// Config holds logger configuration.
type Config struct {
	// Level is the minimum log level to output.
	Level LogLevel

	// Pretty enables human-readable console output (default: false for JSON).
	Pretty bool

	// Output is the writer to output logs to (default: os.Stderr).
	Output io.Writer
}

// DefaultConfig returns a default logger configuration.
func DefaultConfig() Config {
	return Config{
		Level:  LevelInfo,
		Pretty: false,
		Output: os.Stderr,
	}
}

// Setup configures the global zerolog logger.
func Setup(cfg Config) zerolog.Logger {
	// Set global log level
	level := parseLevel(cfg.Level)
	zerolog.SetGlobalLevel(level)

	// Configure output
	var output io.Writer = cfg.Output
	if cfg.Pretty {
		output = zerolog.ConsoleWriter{Out: cfg.Output}
	}

	// Create logger with timestamp
	logger := zerolog.New(output).With().Timestamp().Logger()

	// Set as global logger
	log.Logger = logger

	return logger
}

// parseLevel converts LogLevel to zerolog.Level.
func parseLevel(level LogLevel) zerolog.Level {
	switch strings.ToLower(string(level)) {
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}

// NewLogger creates a new logger with the given component name.
func NewLogger(component string) zerolog.Logger {
	return log.With().Str("component", component).Logger()
}

// Log Level Guidelines:
//
// Debug: Detailed information for debugging
//   - Cache operations (hit/miss, key, TTL)
//   - Request flow (conditional requests, ETags)
//   - Internal state changes
//
// Info: Normal operation events
//   - Successful requests
//   - 304 Not Modified responses
//   - Rate limit state updates (healthy)
//   - Server startup/shutdown
//
// Warn: Warning conditions that don't prevent operation
//   - Rate limit warnings (throttling active)
//   - Retry attempts
//   - Cache errors (fallback to direct request)
//   - Non-critical errors
//
// Error: Error conditions requiring attention
//   - Failed requests (after retries)
//   - Critical rate limit blocks
//   - Service unavailability
//   - Configuration errors
//
// Context Fields:
//   - endpoint: ESI endpoint path
//   - status_code: HTTP status code
//   - duration: Request duration
//   - error_class: Error classification (client, server, rate_limit, network)
//   - cache_hit: Boolean indicating cache hit
//   - errors_remaining: Current ESI error limit
//   - etag: ETag value for conditional requests
//   - ttl: Cache entry TTL
