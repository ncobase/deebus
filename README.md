# deebus

[![Go Reference](https://pkg.go.dev/badge/github.com/ncobase/deebus.svg)](https://pkg.go.dev/github.com/ncobase/deebus)
[![Go Report Card](https://goreportcard.com/badge/github.com/ncobase/deebus)](https://goreportcard.com/report/github.com/ncobase/deebus)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**deebus** is a zero-dependency AI provider abstraction library for Go. It presents a single, unified interface over multiple large-language-model providers and wraps every call in a production-grade reliability stack: automatic fallback, exponential backoff with jitter, per-provider circuit breaking, token-bucket rate limiting, and structured logging.

---

## Features

| Feature | Details |
|---------|---------|
| **Multi-provider** | OpenAI, Anthropic (Claude), Google Gemini, Ollama, Cohere |
| **Smart fallback** | Tries primary → fallbacks in order; skips 400 errors (bad request) |
| **Retry with jitter** | Equal-jitter exponential backoff; honours `Retry-After` on 429 |
| **Circuit breaker** | Closed → Open → Half-open state machine per provider |
| **Rate limiting** | Continuous token-bucket algorithm per provider |
| **Structured logging** | Pluggable `Logger` interface; defaults to no-op |
| **Usage statistics** | Atomic request/token counters via `client.Stats` |
| **Streaming** | SSE / NDJSON streaming for all five providers |
| **Multimodal** | Text, images (URL/base64), audio, PDF documents |
| **Tool calling** | Function/tool use for OpenAI and Anthropic |
| **Zero dependencies** | Only `gopkg.in/yaml.v3` for config parsing |
| **Thread-safe** | All public methods are safe for concurrent use |

---

## Installation

```bash
go get github.com/ncobase/deebus@latest
```

Requires **Go 1.21** or later.

---

## Quick Start

### 1. Write a configuration file (`deebus.yaml`)

```yaml
providers:
  anthropic:
    type: anthropic
    apiKey: ${ANTHROPIC_API_KEY}      # expanded from environment
    baseURL: https://api.anthropic.com
  openai:
    type: openai
    apiKey: ${OPENAI_API_KEY}
    baseURL: https://api.openai.com

primary: anthropic/claude-opus-4-6
fallbacks:
  - openai/gpt-4o

timeout: 30        # seconds per request
retry: 2           # additional attempts on transient errors
rateLimit: 10      # max requests/second per provider (0 = disabled)
circuitBreaker:
  maxFailures: 5   # open circuit after N consecutive failures
  resetTimeout: 60 # seconds before half-open probe
```

`${ENV_VAR}` and `$ENV_VAR` placeholders are expanded before YAML parsing, so API keys are never written in plaintext.

### 2. Use the client

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/ncobase/deebus"
)

func main() {
    client, err := deebus.LoadConfig("deebus.yaml")
    if err != nil {
        log.Fatal(err)
    }

    resp, err := client.Complete(context.Background(), &deebus.Request{
        Messages: []deebus.Message{
            deebus.SimpleMessage("user", "Explain the circuit breaker pattern."),
        },
        MaxTokens:   512,
        Temperature: 0.7,
    })
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(resp.Content)
    fmt.Printf("provider=%s model=%s tokens=%d\n",
        resp.Provider, resp.Model, resp.TokensUsed)
}
```

---

## Configuration Reference

### Top-level fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `providers` | map | — | Named provider configurations (see below) |
| `primary` | string | — | **Required.** Model to try first (`provider/model`) |
| `fallbacks` | []string | `[]` | Ordered list of fallback models |
| `timeout` | int | `30` | HTTP request timeout in seconds |
| `retry` | int | `2` | Additional attempts per provider on transient errors |
| `rateLimit` | int | `0` | Maximum requests per second per provider (0 = disabled) |
| `circuitBreaker.maxFailures` | int | `0` | Consecutive failures before opening circuit (0 = disabled) |
| `circuitBreaker.resetTimeout` | int | `60` | Seconds before half-open probe after circuit opens |

### Provider fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | ✓ | One of `openai`, `anthropic`, `gemini`, `ollama`, `cohere` |
| `apiKey` | string | ✓ * | API key. *Not required for `ollama`. |
| `baseURL` | string | ✓ | Base URL. Must use `https://` or `http://localhost`/`http://127.0.0.1` |

### Programmatic configuration

```go
client, err := deebus.NewClient(deebus.Config{
    Providers: map[string]deebus.ProviderConfig{
        "anthropic": {
            Type:    "anthropic",
            APIKey:  os.Getenv("ANTHROPIC_API_KEY"),
            BaseURL: "https://api.anthropic.com",
        },
        "openai": {
            Type:    "openai",
            APIKey:  os.Getenv("OPENAI_API_KEY"),
            BaseURL: "https://api.openai.com",
        },
    },
    Primary:   "anthropic/claude-opus-4-6",
    Fallbacks: []string{"openai/gpt-4o"},
    Timeout:   30,
    Retry:     2,
    RateLimit: 10,
    CircuitBreaker: deebus.CircuitBreakerConfig{
        MaxFailures:  5,
        ResetTimeout: 60,
    },
})
```

---

## Provider Compatibility

| Provider | Complete | Stream | Embed | Tool Use | Multimodal |
|----------|----------|--------|-------|----------|------------|
| **OpenAI** | ✓ | ✓ SSE | ✓ | ✓ | Image, Audio |
| **Anthropic** | ✓ | ✓ SSE | — | ✓ | Image, PDF |
| **Gemini** | ✓ | ✓ SSE | — | — | Image |
| **Ollama** | ✓ | ✓ NDJSON | ✓ | — | — |
| **Cohere** | ✓ | ✓ SSE | ✓ | — | — |

---

## Middleware Pipeline

Every provider is wrapped with the following middleware layers. The stack is constructed automatically by `NewClient`/`LoadConfig`.

```
Client.Complete(req)
    │
    └─ LoggingMiddleware          ← records duration, tokens, errors
        └─ CircuitBreakerMiddleware ← rejects immediately if provider is down
            └─ RetryMiddleware     ← retries with exponential back-off + jitter
                └─ RateLimitMiddleware ← token-bucket throttle
                    └─ BaseProvider    ← HTTP call to the LLM API
```

### Retry and Fallback Strategy

The handling of each HTTP status code is deterministic:

| Status | Meaning | Retry same provider | Try next provider |
|--------|---------|---------------------|-------------------|
| 400 | Bad Request | No | **No** — request is malformed |
| 401 / 403 | Auth error | No | Yes — another provider may have valid credentials |
| 408 / 504 | Timeout | Yes | Yes |
| 429 | Rate limited | Yes (honours `Retry-After`) | Yes after retries exhausted |
| 5xx | Server error | Yes | Yes |
| Network | Connection failure | Yes | Yes |

**Circuit breaker notes:**
- Auth errors (401/403) and bad requests (400) do **not** count as failures toward the circuit — they are configuration or request issues, not provider health indicators.
- When the circuit opens, the error returned has `Fallback: true`, so the client immediately moves to the next provider without waiting for a retry cycle.

---

## Streaming

```go
stream, err := client.Stream(ctx, &deebus.Request{
    Messages: []deebus.Message{
        deebus.SimpleMessage("user", "Write a haiku about Go."),
    },
})
if err != nil {
    log.Fatal(err)
}

for chunk := range stream {
    if chunk.Error != nil {
        log.Printf("stream error: %v", chunk.Error)
        break
    }
    fmt.Print(chunk.Content)
    if chunk.Done {
        fmt.Printf("\n[finish: %s]\n", chunk.FinishReason)
        break
    }
}
```

---

## Multimodal Input

```go
// Image (URL)
msg := deebus.ImageMessage("user",
    deebus.ImageSource{Type: "url", URL: "https://example.com/photo.jpg"},
    "Describe this image.",
)

// Image (base64)
msg := deebus.ImageMessage("user",
    deebus.ImageSource{
        Type:      "base64",
        MediaType: "image/jpeg",
        Data:      base64EncodedBytes,
    },
    "What is shown here?",
)

// Audio
msg := deebus.AudioMessage("user", base64AudioData, "mp3")

// PDF document
msg := deebus.DocumentMessage("user", base64PDFData, "application/pdf")
```

---

## Tool Calling

```go
resp, err := client.Complete(ctx, &deebus.Request{
    Messages: []deebus.Message{
        deebus.SimpleMessage("user", "What is the weather in Tokyo?"),
    },
    Tools: []deebus.Tool{
        {
            Type: "function",
            Function: deebus.FunctionSchema{
                Name:        "get_weather",
                Description: "Return current weather for a city",
                Parameters: map[string]any{
                    "type": "object",
                    "properties": map[string]any{
                        "city": map[string]any{"type": "string"},
                    },
                    "required": []string{"city"},
                },
            },
        },
    },
    ToolChoice: "auto",
})

for _, call := range resp.ToolCalls {
    fmt.Printf("tool=%s args=%s\n", call.Function.Name, call.Function.Arguments)
}
```

---

## Custom Logger

Implement the four-method `Logger` interface to plug in any logging backend:

```go
// Example: bridge to Go's standard slog (Go 1.21+)
type SlogAdapter struct{}

func (SlogAdapter) Debug(msg string, fields ...any) { slog.Debug(msg, fields...) }
func (SlogAdapter) Info(msg string, fields ...any)  { slog.Info(msg, fields...) }
func (SlogAdapter) Warn(msg string, fields ...any)  { slog.Warn(msg, fields...) }
func (SlogAdapter) Error(msg string, fields ...any) { slog.Error(msg, fields...) }

client.SetLogger(SlogAdapter{})
```

`SetLogger` is safe to call concurrently at any time. The change propagates immediately to every middleware layer.

---

## Usage Statistics

`client.Stats` is an `*Stats` value that tracks request counts and token usage atomically.

```go
total, tokens, success, failed := client.Stats.Get()
fmt.Printf("requests=%d tokens=%d success=%d failed=%d\n",
    total, tokens, success, failed)
```

---

## Embeddings

```go
resp, err := client.Embed(ctx, &deebus.EmbedRequest{
    Model: "openai/text-embedding-3-small",
    Input: []string{"The quick brown fox", "Go is fast"},
})
if err != nil {
    log.Fatal(err)
}
fmt.Printf("embeddings: %d vectors of dim %d\n",
    len(resp.Embeddings), len(resp.Embeddings[0]))
```

Embedding is supported by OpenAI, Ollama, and Cohere. Calling `Embed` on a provider that does not support it returns an error with `Fallback: true`, causing the client to try the next provider automatically.

---

## Error Handling

```go
resp, err := client.Complete(ctx, req)
if err != nil {
    if deebus.IsRetryable(err) {
        // A transient error on the last attempt — safe to retry from your side
    }
    if !deebus.IsFallback(err) {
        // 400 Bad Request — the request itself is malformed; fix it before retrying
    }
    log.Printf("provider error: %v", err)
}
```

Both helpers walk the error chain, so they work correctly with wrapped errors.

---

## Architecture

```
github.com/ncobase/deebus
│
├── client.go          Client, Config, LoadConfig, NewClient, SetLogger
├── types.go           Type aliases  (re-exports from providers sub-package)
├── errors.go          IsRetryable, IsFallback
├── logger.go          Logger interface, NoopLogger, sharedLogger
├── stats.go           Stats (atomic int64 counters)
│
├── providers/
│   ├── types.go       Provider interface, Request, Response, StreamChunk, …
│   ├── error_types.go ProviderError{Retryable, Fallback, RetryAfter}
│   ├── errors.go      parseError, parseRetryAfter, networkError
│   ├── helpers.go     SimpleMessage, ImageMessage, format converters
│   ├── anthropic.go   Anthropic Messages API
│   ├── openai.go      OpenAI Chat Completions (and compatible endpoints)
│   ├── gemini.go      Google Gemini generateContent / streamGenerateContent
│   ├── ollama.go      Ollama /api/chat  (local models)
│   └── cohere.go      Cohere /v2/chat
│
├── middleware/
│   ├── logging.go     LoggingMiddleware
│   ├── retry.go       RetryMiddleware   (equal-jitter exponential backoff)
│   ├── ratelimit.go   RateLimitMiddleware (continuous token bucket)
│   └── circuit.go     CircuitBreakerMiddleware
│
└── internal/
    ├── log/           Shared Logger interface (breaks circular imports)
    └── circuit/       Circuit breaker state machine (Closed → Open → Half-open)
```

---

## License

MIT — see [LICENSE](LICENSE).
