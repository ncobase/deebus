# deebus

[![Go Reference](https://pkg.go.dev/badge/github.com/ncobase/deebus.svg)](https://pkg.go.dev/github.com/ncobase/deebus)
[![Go Report Card](https://goreportcard.com/badge/github.com/ncobase/deebus)](https://goreportcard.com/report/github.com/ncobase/deebus)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**deebus** is a production-grade AI provider abstraction library for Go. It presents a unified interface over five large-language-model providers, wraps every call in a reliability stack (retry, circuit breaking, rate limiting, fallback), and ships an agentic loop with parallel tool execution and an MCP client for connecting to any Model Context Protocol server.

---

## Features

| Feature | Details |
|---------|---------|
| **Multi-provider** | OpenAI, Anthropic, Google Gemini, Ollama, Cohere |
| **Smart fallback** | Primary → fallbacks in order; HTTP 400 is never retried or fallen back |
| **Retry with jitter** | Equal-jitter exponential backoff; honours `Retry-After` on 429 |
| **Circuit breaker** | Closed → Open → Half-open state machine per provider |
| **Rate limiting** | Continuous token-bucket algorithm per provider |
| **Tool calling** | Function/tool use for all five providers with streaming assembly |
| **Multi-turn tool calling** | `AssistantMessage` / `ToolResultMessage` with per-provider wire format |
| **Agent loop** | `RunAgent` / `RunAgentStream` with parallel tool dispatch and event hooks |
| **MCP client** | Connects to any MCP server via stdio or Streamable HTTP (spec 2025-03-26) |
| **Streaming** | SSE / NDJSON streaming for all five providers |
| **Multimodal** | Text, images (URL / base64), audio, PDF documents |
| **Embeddings** | OpenAI, Gemini, Ollama, Cohere |
| **Structured logging** | Pluggable `Logger` interface; defaults to no-op |
| **Usage statistics** | Atomic request / token counters via `client.Stats` |
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
    apiKey: ${ANTHROPIC_API_KEY}      # expanded from environment at load time
    baseURL: https://api.anthropic.com
  openai:
    type: openai
    apiKey: ${OPENAI_API_KEY}
    baseURL: https://api.openai.com

primary:   anthropic/claude-opus-4-6
fallbacks:
  - openai/gpt-4o

timeout:   30   # seconds per request
retry:     2    # additional attempts on transient errors (429, 5xx, network)
rateLimit: 10   # max requests/second per provider (0 = disabled)
circuitBreaker:
  maxFailures:  5   # consecutive failures that open the circuit (0 = disabled)
  resetTimeout: 60  # seconds before a half-open probe
```

`${ENV_VAR}` and `$ENV_VAR` placeholders are expanded before YAML parsing; API keys are never written in plaintext.

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
| `fallbacks` | []string | `[]` | Ordered fallback models tried when the primary fails |
| `timeout` | int | `30` | HTTP request timeout in seconds |
| `retry` | int | `2` | Additional attempts per provider on transient errors |
| `rateLimit` | int | `0` | Max requests per second per provider (0 = disabled) |
| `circuitBreaker.maxFailures` | int | `0` | Consecutive failures before opening circuit (0 = disabled) |
| `circuitBreaker.resetTimeout` | int | `60` | Seconds before half-open probe after circuit opens |

### Provider fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | ✓ | One of `openai`, `anthropic`, `gemini`, `ollama`, `cohere` |
| `apiKey` | string | ✓ * | API key. *Not required for `ollama`. |
| `baseURL` | string | ✓ | Must use `https://` or `http://localhost` / `http://127.0.0.1` / `http://0.0.0.0` |

### Programmatic configuration

```go
client, err := deebus.NewClient(deebus.Config{
    Providers: map[string]deebus.ProviderConfig{
        "anthropic": {
            Type:    "anthropic",
            APIKey:  os.Getenv("ANTHROPIC_API_KEY"),
            BaseURL: "https://api.anthropic.com",
        },
    },
    Primary:   "anthropic/claude-opus-4-6",
    Fallbacks: []string{"openai/gpt-4o"},
    Timeout:   30,
    Retry:     2,
})
```

---

## Provider Compatibility

| Provider | Complete | Stream | Embed | Tool Calling | Multimodal |
|----------|:--------:|:------:|:-----:|:------------:|:----------:|
| **OpenAI** | ✓ | ✓ SSE | ✓ | ✓ | Image, Audio |
| **Anthropic** | ✓ | ✓ SSE | — | ✓ | Image, PDF |
| **Gemini** | ✓ | ✓ SSE | ✓ | ✓ | Image |
| **Ollama** | ✓ | ✓ NDJSON | ✓ | ✓ | — |
| **Cohere** | ✓ | ✓ SSE | ✓ | ✓ | — |

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
        fmt.Printf("\n[finish: %s, tokens: %d]\n", chunk.FinishReason, chunk.TokensUsed)
        break
    }
}
```

---

## Multimodal Input

```go
// Image from URL
msg := deebus.ImageMessage("user",
    deebus.ImageSource{Type: "url", URL: "https://example.com/photo.jpg"},
    "Describe this image.",
)

// Image from base64
msg := deebus.ImageMessage("user",
    deebus.ImageSource{Type: "base64", MediaType: "image/jpeg", Data: base64Bytes},
    "What is shown here?",
)

// Audio
msg := deebus.AudioMessage("user", base64AudioData, "mp3")

// PDF document
msg := deebus.DocumentMessage("user", base64PDFData, "application/pdf")
```

---

## Tool Calling

### Single-turn

```go
tools := []deebus.Tool{{
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
}}

resp, err := client.Complete(ctx, &deebus.Request{
    Messages:   []deebus.Message{deebus.SimpleMessage("user", "Weather in Tokyo?")},
    Tools:      tools,
    ToolChoice: "auto", // "auto" | "none" | "required" | specific function name
})

for _, call := range resp.ToolCalls {
    fmt.Printf("tool=%s args=%s\n", call.Function.Name, call.Function.Arguments)
}
```

### Multi-turn (manual)

```go
// Build conversation history manually when executing tools yourself.
messages := []deebus.Message{
    deebus.SimpleMessage("user", "What is the weather in Tokyo and London?"),
}

resp, _ := client.Complete(ctx, &deebus.Request{Messages: messages, Tools: tools})

// Append the assistant's tool-call turn.
messages = append(messages, deebus.AssistantMessage(resp.Content, resp.ToolCalls))

// Execute each tool and append results.
for _, tc := range resp.ToolCalls {
    result := executeWeatherTool(tc.Function.Arguments) // your implementation
    messages = append(messages, deebus.ToolResultMessage(tc.ID, tc.Function.Name, result))
}

// Continue the conversation.
final, _ := client.Complete(ctx, &deebus.Request{Messages: messages, Tools: tools})
fmt.Println(final.Content)
```

---

## Agent Loop

`RunAgent` and `RunAgentStream` automate the tool-call loop: call the model, execute any tool calls (in parallel by default), feed results back, and repeat until the model returns a final text response or `MaxIterations` is reached.

### Non-streaming

```go
answer, history, err := client.RunAgent(ctx,
    &deebus.Request{
        Messages: []deebus.Message{
            deebus.SimpleMessage("user", "List the files in /tmp and summarise them."),
        },
        Tools: tools,
    },
    func(ctx context.Context, name, argsJSON string) (string, error) {
        // Execute the named tool and return its result as a string.
        return dispatchTool(name, argsJSON)
    },
    deebus.AgentConfig{
        MaxIterations:      10,    // default: 10
        DisableParallel:    false, // execute independent tool calls concurrently
        MaxHistoryMessages: 50,    // trim oldest turns when conversation grows
        Hook: func(ev deebus.AgentEvent) {
            // Observe every action in the loop.
            slog.Info("agent event", "type", ev.Type, "tool", ev.ToolName,
                "tokens", ev.TokensUsed, "duration", ev.Duration)
        },
    },
)
```

### Streaming

```go
histCh := make(chan []deebus.Message, 1)

stream, err := client.RunAgentStream(ctx, req, toolFn, histCh,
    deebus.AgentConfig{MaxIterations: 10})
if err != nil {
    log.Fatal(err)
}

for chunk := range stream {
    if chunk.Error != nil {
        log.Printf("agent error: %v", chunk.Error)
        break
    }
    fmt.Print(chunk.Content)
}

history := <-histCh // full conversation including all tool turns
```

### AgentConfig fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `MaxIterations` | int | `10` | Maximum model → tool round-trips |
| `DisableParallel` | bool | `false` | When `false`, independent tool calls in one turn run concurrently |
| `Hook` | `func(AgentEvent)` | `nil` | Called synchronously on each observable action |
| `MaxHistoryMessages` | int | `0` | Trim conversation to at most N messages, preserving system messages |

### AgentEvent types

| Type | When |
|------|------|
| `"llm_request"` | Before each model call |
| `"llm_response"` | After the model responds |
| `"tool_call"` | Before each tool execution |
| `"tool_result"` | After a tool returns |
| `"done"` | Agent produced a final answer |
| `"error"` | Agent loop terminated with an error |

---

## MCP Client

The `mcp` sub-package implements a client for the [Model Context Protocol](https://modelcontextprotocol.io/) (spec 2025-03-26), enabling agents to use tools exposed by any MCP-compatible server with zero additional dependencies.

### stdio transport (most common)

```go
import "github.com/ncobase/deebus/mcp"

// Launch a local MCP server as a subprocess.
mcpClient, err := mcp.NewStdioClient(ctx,
    "npx", []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}, nil)
if err != nil {
    log.Fatal(err)
}
defer mcpClient.Close()

// Fetch tool schemas (paginated internally, cached, auto-refreshed on list_changed).
tools, err := mcpClient.Tools(ctx)
if err != nil {
    log.Fatal(err)
}

// Run an agent that calls MCP tools automatically.
answer, _, err := deebusClient.RunAgent(ctx,
    &deebus.Request{
        Messages: []deebus.Message{deebus.SimpleMessage("user", "List files in /tmp")},
        Tools:    tools,
    },
    mcpClient.Execute, // AgentToolFunc-compatible
)
fmt.Println(answer)
```

### HTTP transport (Streamable HTTP, spec 2025-03-26)

```go
mcpClient, err := mcp.NewHTTPClient(ctx,
    "https://mcp.example.com/mcp",
    30*time.Second,
    mcp.WithNotificationHandler(func(method string, params json.RawMessage) {
        slog.Info("mcp notification", "method", method)
    }),
)
```

### Calling tools directly

```go
// Full result with IsError flag.
result, err := mcpClient.CallTool(ctx, "read_file", `{"path":"/tmp/notes.txt"}`)
fmt.Printf("isError=%v text=%s\n", result.IsError, result.Text())

// AgentToolFunc-compatible shorthand; IsError is returned as prefixed text,
// not a Go error, so the model can observe and recover from tool-level failures.
out, err := mcpClient.Execute(ctx, "read_file", `{"path":"/tmp/notes.txt"}`)
```

---

## Middleware Pipeline

Every provider is wrapped with the following middleware layers, constructed automatically by `NewClient` / `LoadConfig`.

```
Client.Complete(req)
    │
    └─ LoggingMiddleware           ← records duration, tokens, errors
        └─ CircuitBreakerMiddleware ← rejects immediately when provider is down
            └─ RetryMiddleware      ← equal-jitter exponential backoff
                └─ RateLimitMiddleware ← continuous token-bucket throttle
                    └─ BaseProvider    ← HTTP call to the LLM API
```

### Retry and Fallback Strategy

| Status | Retry same provider | Try next fallback |
|--------|--------------------:|------------------:|
| 400 Bad Request | No | **No** — request is malformed |
| 401 / 403 Auth error | No | Yes |
| 408 / 504 Timeout | Yes | Yes |
| 429 Rate limited | Yes (honours `Retry-After`) | Yes after retries exhausted |
| 5xx Server error | Yes | Yes |
| Network failure | Yes | Yes |

Auth errors (401/403) and bad requests (400) do **not** count as failures toward the circuit breaker.

---

## Embeddings

```go
resp, err := client.Embed(ctx, &deebus.EmbedRequest{
    Input:     []string{"The quick brown fox", "Go is fast"},
    InputType: "search_document", // hint for retrieval-optimised embeddings
})
fmt.Printf("%d vectors of dim %d\n", len(resp.Embeddings), len(resp.Embeddings[0]))
```

Supported by OpenAI, Gemini, Ollama, and Cohere.

---

## Custom Logger

Implement the four-method `Logger` interface to plug in any logging backend:

```go
type SlogAdapter struct{}

func (SlogAdapter) Debug(msg string, fields ...any) { slog.Debug(msg, fields...) }
func (SlogAdapter) Info(msg string, fields ...any)  { slog.Info(msg, fields...) }
func (SlogAdapter) Warn(msg string, fields ...any)  { slog.Warn(msg, fields...) }
func (SlogAdapter) Error(msg string, fields ...any) { slog.Error(msg, fields...) }

client.SetLogger(SlogAdapter{})
```

`SetLogger` is safe to call concurrently at any time; the change propagates immediately to every middleware layer.

---

## Usage Statistics

```go
total, tokens, success, failed := client.Stats.Get()
fmt.Printf("requests=%d tokens=%d success=%d failed=%d\n",
    total, tokens, success, failed)
```

---

## Error Handling

```go
resp, err := client.Complete(ctx, req)
if err != nil {
    if deebus.IsRetryable(err) {
        // Transient failure on the last attempt — safe to retry from the caller
    }
    if !deebus.IsFallback(err) {
        // HTTP 400 — the request itself is malformed; fix it before retrying
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
├── client.go          Client, Config, LoadConfig, NewClient, Health
├── agent.go           RunAgent, RunAgentStream, AgentConfig, AgentEvent
├── types.go           Type aliases (re-exports from providers sub-package)
├── errors.go          IsRetryable, IsFallback
├── logger.go          Logger interface, NoopLogger, sharedLogger
├── stats.go           Stats (atomic counters)
│
├── providers/
│   ├── types.go       Provider interface, Request, Response, StreamChunk, …
│   ├── helpers.go     Message constructors, per-provider format converters
│   ├── error_types.go ProviderError{Retryable, Fallback, RetryAfter}
│   ├── errors.go      parseError, networkError
│   ├── anthropic.go   Anthropic Messages API
│   ├── openai.go      OpenAI Chat Completions (and compatible endpoints)
│   ├── gemini.go      Google Gemini generateContent / streamGenerateContent
│   ├── ollama.go      Ollama /api/chat (local models)
│   └── cohere.go      Cohere /v2/chat
│
├── mcp/
│   ├── client.go      MCPClient, NewStdioClient, NewHTTPClient, Tools, Execute
│   ├── conn.go        JSON-RPC 2.0 request/response correlation
│   ├── stdio.go       stdio subprocess transport
│   ├── http.go        Streamable HTTP transport (spec 2025-03-26)
│   └── types.go       MCP protocol types, tool conversion
│
├── middleware/
│   ├── logging.go     LoggingMiddleware
│   ├── retry.go       RetryMiddleware (equal-jitter exponential backoff)
│   ├── ratelimit.go   RateLimitMiddleware (continuous token bucket)
│   └── circuit.go     CircuitBreakerMiddleware
│
└── internal/
    ├── circuit/       Circuit breaker state machine (Closed → Open → Half-open)
    └── log/           Shared Logger interface (avoids circular imports)
```

---

## License

MIT — see [LICENSE](LICENSE).
