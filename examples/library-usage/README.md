# Library Usage Example

A complete, runnable program showing the integrated ESI client: configuration,
making a request, automatic caching (ETag / 304), and error handling.

The code lives in [`main.go`](main.go) — this README only explains how to run it.

## Prerequisites

- Go 1.25+
- A Redis server (default: `localhost:6379`)

## Run

```bash
# Start Redis if needed
docker run -d -p 6379:6379 redis:7-alpine

# Run the example
cd examples/library-usage
go run main.go
```

Override the Redis address via `REDIS_URL` if needed.

## What it demonstrates

- Client initialization with `client.DefaultConfig` (User-Agent per ESI rules)
- Making a request with automatic rate limiting and caching
- A second request served from cache via a conditional request (304 Not Modified)
- Error handling for an invalid endpoint

## See also

- [Project README](../../README.md)
- [Configuration Guide](../../docs/configuration.md)
- [Troubleshooting Guide](../../docs/troubleshooting.md)
