package logging

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Level != LevelInfo {
		t.Errorf("Expected default level to be Info, got %s", cfg.Level)
	}

	if cfg.Pretty != false {
		t.Error("Expected default pretty to be false")
	}
}

func TestSetup(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		testMsg  string
		contains string
	}{
		{
			name: "info_level",
			config: Config{
				Level:  LevelInfo,
				Pretty: false,
				Output: &bytes.Buffer{},
			},
			testMsg:  "test info message",
			contains: "test info message",
		},
		{
			name: "debug_level",
			config: Config{
				Level:  LevelDebug,
				Pretty: false,
				Output: &bytes.Buffer{},
			},
			testMsg:  "test debug message",
			contains: "test debug message",
		},
		{
			name: "warn_level",
			config: Config{
				Level:  LevelWarn,
				Pretty: false,
				Output: &bytes.Buffer{},
			},
			testMsg:  "test warn message",
			contains: "test warn message",
		},
		{
			name: "error_level",
			config: Config{
				Level:  LevelError,
				Pretty: false,
				Output: &bytes.Buffer{},
			},
			testMsg:  "test error message",
			contains: "test error message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			tt.config.Output = buf

			logger := Setup(tt.config)

			// Test that logger writes to the configured output
			switch tt.config.Level {
			case LevelDebug:
				logger.Debug().Msg(tt.testMsg)
			case LevelInfo:
				logger.Info().Msg(tt.testMsg)
			case LevelWarn:
				logger.Warn().Msg(tt.testMsg)
			case LevelError:
				logger.Error().Msg(tt.testMsg)
			}

			output := buf.String()
			if !strings.Contains(output, tt.contains) {
				t.Errorf("Expected output to contain %q, got %q", tt.contains, output)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    LogLevel
		expected zerolog.Level
	}{
		{LevelDebug, zerolog.DebugLevel},
		{LevelInfo, zerolog.InfoLevel},
		{LevelWarn, zerolog.WarnLevel},
		{LevelError, zerolog.ErrorLevel},
		{"invalid", zerolog.InfoLevel}, // Should default to Info
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			result := parseLevel(tt.input)
			if result != tt.expected {
				t.Errorf("parseLevel(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNewLogger(t *testing.T) {
	buf := &bytes.Buffer{}
	Setup(Config{
		Level:  LevelInfo,
		Pretty: false,
		Output: buf,
	})

	logger := NewLogger("test-component")
	logger.Info().Msg("test message")

	output := buf.String()
	if !strings.Contains(output, "test-component") {
		t.Errorf("Expected output to contain 'test-component', got %q", output)
	}
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected output to contain 'test message', got %q", output)
	}
}

func TestLogLevelFiltering(t *testing.T) {
	buf := &bytes.Buffer{}
	Setup(Config{
		Level:  LevelWarn,
		Pretty: false,
		Output: buf,
	})

	logger := NewLogger("test")

	// These should NOT appear (below warn level)
	logger.Debug().Msg("debug message")
	logger.Info().Msg("info message")

	// These SHOULD appear (warn level and above)
	logger.Warn().Msg("warn message")
	logger.Error().Msg("error message")

	output := buf.String()

	if strings.Contains(output, "debug message") {
		t.Error("Debug message should be filtered out at Warn level")
	}
	if strings.Contains(output, "info message") {
		t.Error("Info message should be filtered out at Warn level")
	}
	if !strings.Contains(output, "warn message") {
		t.Error("Warn message should be included at Warn level")
	}
	if !strings.Contains(output, "error message") {
		t.Error("Error message should be included at Warn level")
	}
}
