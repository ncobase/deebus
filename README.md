# deebus

[![Go Reference](https://pkg.go.dev/badge/github.com/ncobase/deebus.svg)](https://pkg.go.dev/github.com/ncobase/deebus)
[![Go Report Card](https://goreportcard.com/badge/github.com/ncobase/deebus)](https://goreportcard.com/report/github.com/ncobase/deebus)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**deebus** is a production-grade AI provider abstraction library for Go. It presents a unified interface over five large-language-model providers, wraps every call in a reliability stack (retry, circuit breaking, rate limiting, fallback), and ships an agentic loop with parallel tool execution and an MCP client for connecting to any Model Context Protocol server.

---

## Features

| Feature                     | Details                                                                                                                                  |
| --------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| **Multi-provider**          | OpenAI, Anthropic, Google Gemini, Ollama, Cohere                                                                                         |
| **Smart fallback**          | Primary -> fallbacks in order; HTTP 400 is never retried or fallen back                                                                  |
| **Retry with jitter**       | Equal-jitter exponential backoff; honours `Retry-After` on 429                                                                           |
| **Circuit breaker**         | Closed -> Open -> Half-open state machine per provider                                                                                   |
| **Rate limiting**           | Continuous token-bucket algorithm per provider                                                                                           |
| **Tool calling**            | Function/tool use for all five providers with streaming assembly                                                                         |
| **Multi-turn tool calling** | `AssistantMessage` / `ToolResultMessage` with per-provider wire format                                                                   |
| **Agent loop**              | `RunAgent` / `RunAgentStream` with parallel tool dispatch and event hooks                                                                |
| **MCP client**              | Connects to any MCP server via stdio or Streamable HTTP (spec 2025-11-25)                                                                |
| **Prompt caching**          | Anthropic block/request caching, OpenAI request hints, Gemini explicit caches; `CacheUsage` in response                                  |
| **Streaming**               | SSE / NDJSON streaming for all five providers, including tool-call assembly and reasoning deltas                              |
| **Multimodal**              | Text, images (URL / base64), audio, PDF documents                                                                                        |
| **Embeddings**              | OpenAI, Gemini, Ollama, Cohere                                                                                                           |
| **Structured outputs**      | JSON object / JSON Schema response formats mapped across OpenAI, Gemini, Ollama, and Cohere                                  |
| **Structured logging**      | Pluggable `Logger` interface; defaults to no-op                                                                                          |
| **Usage statistics**        | Per-request input/output/cache token counters; ReasoningTokens for o-series/thinking models; aggregate Stats with cache hit/write totals |
| **Zero dependencies**       | Only `gopkg.in/yaml.v3` for optional YAML loading                                                                                        |
| **Thread-safe**             | All public methods are safe for concurrent use                                                                                           |

---

## Installation

```bash
go get github.com/ncobase/deebus@latest
```

Requires **Go 1.21** or later.

---

## Quick Start

`NewClient` is the primary entry point. The library does not require a
dedicated config file; applications can pass `deebus.Config` directly and read
environment variables however they prefer.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    "github.com/ncobase/deebus"
)

func main() {
    client, err := deebus.NewClient(deebus.Config{
        Providers: map[string]deebus.ProviderConfig{
            "anthropic": {
                Type:    "anthropic",
                APIKey:  os.Getenv("ANTHROPIC_API_KEY"),
                BaseURL: "https://api.anthropic.com",
            },
        },
        Primary: "anthropic/claude-opus-4-6",
        Timeout: 30,
        Retry:   2,
    })
    if err != nil {
        log.Fatal(err)
    }

    resp, err := client.Complete(context.Background(), &deebus.Request{
        Messages: []deebus.Message{
            deebus.TextMessage("user", "Explain the circuit breaker pattern."),
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

| Field                         | Type     | Default | Description                                                |
| ----------------------------- | -------- | ------- | ---------------------------------------------------------- |
| `providers`                   | map      | -       | Named provider configurations (see below)                  |
| `primary`                     | string   | -       | **Required.** Model to try first (`provider/model`)        |
| `fallbacks`                   | []string | `[]`    | Ordered fallback models tried when the primary fails       |
| `timeout`                     | int      | `30`    | HTTP request timeout in seconds                            |
| `retry`                       | int      | `2`     | Additional attempts per provider on transient errors       |
| `rateLimit`                   | int      | `0`     | Max requests per second per provider (0 = disabled)        |
| `circuitBreaker.maxFailures`  | int      | `0`     | Consecutive failures before opening circuit (0 = disabled) |
| `circuitBreaker.resetTimeout` | int      | `60`    | Seconds before half-open probe after circuit opens         |

### Provider fields

| Field          | Type   | Required | Description                                                                                    |
| -------------- | ------ | -------- | ---------------------------------------------------------------------------------------------- |
| `type`         | string | Yes      | One of `openai`, `anthropic`, `gemini`, `ollama`, `cohere`                                     |
| `apiKey`       | string | Yes\*    | API key. Optional when `bearerToken`, `headers`, or `CredentialProvider` supplies credentials. |
| `bearerToken`  | string | -        | Static bearer token for OAuth-style or proxy auth                                              |
| `baseURL`      | string | Yes      | Must use `https://` or `http://localhost` / `http://127.0.0.1` / `http://0.0.0.0`              |
| `apiMode`      | string | -        | OpenAI only: `chat_completions` default or `responses` for the modern Responses API            |
| `headers`      | map    | -        | Extra static headers sent on every request                                                     |
| `organization` | string | -        | OpenAI `OpenAI-Organization` header                                                            |
| `project`      | string | -        | OpenAI `OpenAI-Project` header                                                                 |
| `userProject`  | string | -        | Gemini `x-goog-user-project` header                                                            |

See [docs/auth.md](docs/auth.md) for the authentication matrix and runtime credential guidance.
`CredentialProvider` is available for programmatic configuration only.

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


### Modern request controls

`Request` supports provider-neutral controls for current model APIs:

- `MaxOutputTokens` for modern output limits while keeping `MaxTokens` backward compatible.
- `ResponseFormat` for JSON object / JSON Schema structured outputs.
- `Reasoning` for effort, thinking budgets, and provider-visible thought summaries.
- `TopP`, `Stop`, `Seed`, `Metadata`, `Store`, and `ParallelToolCalls` for providers that support them.

OpenAI can use the Responses API by setting `apiMode: responses` on the provider config; OpenAI-compatible gateways can keep the default Chat Completions mode.

### Optional YAML loading

If your application already stores settings in YAML, `LoadConfig` can read a
file into `Config`. The file name and location belong to the application.

```yaml
providers:
  anthropic:
    type: anthropic
    apiKey: ${ANTHROPIC_API_KEY} # expanded from environment at load time
    baseURL: https://api.anthropic.com
  openai:
    type: openai
    apiKey: ${OPENAI_API_KEY}
    baseURL: https://api.openai.com

primary: anthropic/claude-opus-4-6
fallbacks:
  - openai/gpt-4o

timeout: 30 # seconds per request
retry: 2 # additional attempts on transient errors (429, 5xx, network)
rateLimit: 10 # max requests/second per provider (0 = disabled)
circuitBreaker:
  maxFailures: 5 # consecutive failures that open the circuit (0 = disabled)
  resetTimeout: 60 # seconds before a half-open probe
```

```go
client, err := deebus.LoadConfig("./config.yaml")
```

`${ENV_VAR}` and `$ENV_VAR` placeholders are expanded before YAML parsing.

---

## Authentication

Direct API auth support is provider-specific:

| Provider  | Direct API auth               | Extra fields              |
| --------- | ----------------------------- | ------------------------- |
| OpenAI    | API key                       | `organization`, `project` |
| Anthropic | API key                       | -                         |
| Gemini    | API key or OAuth bearer token | `userProject`             |
| Ollama    | none                          | -                         |
| Cohere    | API key                       | -                         |

For short-lived bearer tokens, OAuth access tokens, or gateway credentials, use a runtime `CredentialProvider`:

```go
type tokenSource struct{}

func (tokenSource) Credentials(ctx context.Context) (deebus.Credentials, error) {
    token, err := currentAccessToken(ctx)
    if err != nil {
        return deebus.Credentials{}, err
    }
    return deebus.Credentials{
        BearerToken: token,
        UserProject: "gcp-project-id", // Gemini only
    }, nil
}

client, err := deebus.NewClient(deebus.Config{
    Providers: map[string]deebus.ProviderConfig{
        "gemini": {
            Type:               "gemini",
            BaseURL:            "https://generativelanguage.googleapis.com",
            CredentialProvider: tokenSource{},
        },
    },
    Primary: "gemini/gemini-2.5-flash",
})
```

The library resolves credentials per request. It does not implement provider-specific OAuth browser flows or token exchange endpoints.

---

## Provider Compatibility

| Provider      | Complete |    Stream    | Embed | Tool Calling |  Multimodal  |                       Caching                        |
| ------------- | :------: | :----------: | :---: | :----------: | :----------: | :--------------------------------------------------: |
| **OpenAI**    |   Yes    |  Yes (SSE)   |  Yes  |     Yes      | Image, Audio | Auto (>=1024 tokens) + `Request.Cache.Key/Retention` |
| **Anthropic** |   Yes    |  Yes (SSE)   |  No   |     Yes      |  Image, PDF  |        Automatic or explicit `cache_control`         |
| **Gemini**    |   Yes    |  Yes (SSE)   |  Yes  |     Yes      |    Image     |         Implicit + explicit `cachedContents`         |
| **Ollama**    |   Yes    | Yes (NDJSON) |  Yes  |     Yes      |      No      |                          No                          |
| **Cohere**    |   Yes    |  Yes (SSE)   |  Yes  |     Yes      |      No      |                          No                          |

---

## Streaming

```go
stream, err := client.Stream(ctx, &deebus.Request{
    Messages: []deebus.Message{
        deebus.TextMessage("user", "Write a haiku about Go."),
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
    Messages:   []deebus.Message{deebus.TextMessage("user", "Weather in Tokyo?")},
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
    deebus.TextMessage("user", "What is the weather in Tokyo and London?"),
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
            deebus.TextMessage("user", "List the files in /tmp and summarise them."),
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

| Field                | Type               | Default | Description                                                         |
| -------------------- | ------------------ | ------- | ------------------------------------------------------------------- |
| `MaxIterations`      | int                | `10`    | Maximum model -> tool round-trips                                   |
| `DisableParallel`    | bool               | `false` | When `false`, independent tool calls in one turn run concurrently   |
| `Hook`               | `func(AgentEvent)` | `nil`   | Called synchronously on each observable action                      |
| `MaxHistoryMessages` | int                | `0`     | Trim conversation to at most N messages, preserving system messages |

### AgentEvent types

| Type             | When                                |
| ---------------- | ----------------------------------- |
| `"llm_request"`  | Before each model call              |
| `"llm_response"` | After the model responds            |
| `"tool_call"`    | Before each tool execution          |
| `"tool_result"`  | After a tool returns                |
| `"done"`         | Agent produced a final answer       |
| `"error"`        | Agent loop terminated with an error |

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
        Messages: []deebus.Message{deebus.TextMessage("user", "List files in /tmp")},
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

1. `LoggingMiddleware`: records duration, tokens, and errors.
2. `CircuitBreakerMiddleware`: rejects immediately when the provider is unavailable.
3. `RetryMiddleware`: retries transient failures with equal-jitter backoff.
4. `RateLimitMiddleware`: enforces a continuous token-bucket limit.
5. `BaseProvider`: performs the HTTP call to the upstream API.

### Retry and Fallback Strategy

| Status               |         Retry same provider |             Try next fallback |
| -------------------- | --------------------------: | ----------------------------: |
| 400 Bad Request      |                          No | **No** - request is malformed |
| 401 / 403 Auth error |                          No |                           Yes |
| 408 / 504 Timeout    |                         Yes |                           Yes |
| 429 Rate limited     | Yes (honours `Retry-After`) |   Yes after retries exhausted |
| 5xx Server error     |                         Yes |                           Yes |
| Network failure      |                         Yes |                           Yes |

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

## Prompt Caching

Caching reduces token costs on repeated static context (system prompts, tool schemas, retrieved documents). The library exposes provider-native controls; **policy decisions belong to the caller**.

See [docs/caching.md](docs/caching.md) for the design and lifecycle model.

### Anthropic - automatic or explicit `cache_control`

Anthropic supports both top-level automatic caching and explicit cache breakpoints. Cache prefixes are evaluated in the order **Tools -> System -> Messages**.

Minimum cacheable block size varies by Anthropic model. Check Anthropic's
current prompt-caching documentation when sizing reusable prefixes. Anthropic
currently allows up to 4 explicit cache breakpoints per request.

```go
// Automatic request-level caching.
req := &deebus.Request{
    Messages: []deebus.Message{
        deebus.TextMessage("system", longSystemPrompt),
        deebus.TextMessage("user", "Summarise your role."),
    },
    Cache: &deebus.CacheOptions{
        Control: &deebus.CacheControl{Type: "ephemeral"},
    },
}
```

```go
// Explicit block-level caching.
req := &deebus.Request{
    Messages: []deebus.Message{
        {
            Role: "system",
            Content: []deebus.ContentBlock{
                deebus.TextContent{
                    Type: "text",
                    Text: longSystemPrompt,
                    CacheControl: &deebus.CacheControl{Type: "ephemeral"},
                },
            },
        },
        deebus.TextMessage("user", "Summarise your role."),
    },
}
```

```go
// Cache the tools array at the last tool boundary.
tools := []deebus.Tool{
    {Type: "function", Function: schema1},
    {
        Type:         "function",
        Function:     schema2,
        CacheControl: &deebus.CacheControl{Type: "ephemeral"},
    },
}
```

### OpenAI - automatic plus cache hints

OpenAI caches the longest cacheable prefix automatically for prompts of 1024 tokens or more. To supply cache key and retention hints, set `Request.Cache`:

```go
resp, _ := client.Complete(ctx, &deebus.Request{
    Messages: messages,
    Cache: &deebus.CacheOptions{
        Key:       "tenant:123:repo:deebus",
        Retention: "24h", // or "in_memory"
    },
})
if resp.CacheUsage.ReadTokens > 0 {
    fmt.Printf("openai served %d tokens from cache\n", resp.CacheUsage.ReadTokens)
}
```

### Gemini - implicit or explicit cached contents

Gemini 2.5+ models use implicit caching automatically. For explicit cached prefixes, create a cache resource once and then reference it from later requests:

```go
cache, err := client.CreateCache(ctx, "gemini", &deebus.CreateCacheRequest{
    Model:       "gemini-2.5-flash",
    DisplayName: "kb:customer-support",
    Messages: []deebus.Message{
        deebus.TextMessage("system", "You are a support assistant."),
        deebus.TextMessage("user", largeDocument),
    },
    TTL: 30 * time.Minute,
})

resp, err := client.Complete(ctx, &deebus.Request{
    Messages: []deebus.Message{
        deebus.TextMessage("user", "Summarise the document."),
    },
    Cache: &deebus.CacheOptions{
        CachedContent: cache.Name,
    },
})
```

`Client.GetCache`, `ListCaches`, `UpdateCache`, and `DeleteCache` are also available for providers that support explicit cache resources.

### CacheUsage fields

| Field           | Anthropic                     | OpenAI                                | Gemini                    |
| --------------- | ----------------------------- | ------------------------------------- | ------------------------- |
| `CreatedTokens` | `cache_creation_input_tokens` | -                                     | -                         |
| `ReadTokens`    | `cache_read_input_tokens`     | `prompt_tokens_details.cached_tokens` | `cachedContentTokenCount` |

---

## Token Breakdown

All token counts are real server-reported values - never client-side estimates.

| Field                      | Description                                                                                                                                                                                                      |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `InputTokens`              | True total input tokens. For Anthropic this is `input_tokens + cache_read + cache_creation`; for other providers it matches the API total.                                                                       |
| `OutputTokens`             | Total output tokens, including reasoning/thinking tokens where applicable.                                                                                                                                       |
| `TokensUsed`               | `InputTokens + OutputTokens`.                                                                                                                                                                                    |
| `ReasoningTokens`          | Subset of `OutputTokens` used for internal reasoning. Populated for OpenAI o-series (`completion_tokens_details.reasoning_tokens`) and Gemini thinking models (`thoughtsTokenCount`). Zero for all other models. |
| `CacheUsage.CreatedTokens` | Tokens written to the prompt cache this request (Anthropic only).                                                                                                                                                |
| `CacheUsage.ReadTokens`    | Tokens served from cache: Anthropic `cache_read_input_tokens`, OpenAI `prompt_tokens_details.cached_tokens`, Gemini `cachedContentTokenCount`.                                                                   |

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
total, input, output, success, failed := client.Stats.Get()
fmt.Printf("requests=%d  input=%d  output=%d  success=%d  failed=%d\n",
    total, input, output, success, failed)

// Cache activity totals (all providers, all requests)
fmt.Printf("cache_writes=%d  cache_reads=%d\n",
    client.Stats.CacheCreatedTokens.Load(),
    client.Stats.CacheReadTokens.Load())
```

---

## Error Handling

```go
resp, err := client.Complete(ctx, req)
if err != nil {
    if deebus.IsRetryable(err) {
        // Transient failure on the last attempt - safe to retry from the caller
    }
    if !deebus.IsFallback(err) {
        // HTTP 400 - the request itself is malformed; fix it before retrying
    }
    log.Printf("provider error: %v", err)
}
```

Both helpers walk the error chain, so they work correctly with wrapped errors.

---

## Architecture

- `client.go`: `Client`, `Config`, `LoadConfig`, `NewClient`, `Health`
- `agent.go`: `RunAgent`, `RunAgentStream`, `AgentConfig`, `AgentEvent`
- `cache.go`: explicit cache resource management on `Client`
- `types.go`: type aliases re-exported from `providers`
- `errors.go`: `IsRetryable`, `IsFallback`
- `logger.go`: `Logger`, `NoopLogger`, `sharedLogger`
- `stats.go`: atomic request and token counters
- `providers/`: provider implementations, wire-format helpers, cache types, and auth/error handling
- `mcp/`: MCP client, transports, protocol types, and tool conversion
- `middleware/`: logging, retry, rate limit, and circuit breaker layers
- `internal/`: shared circuit breaker and logger internals
- `examples/`: completion, tools, agent, embeddings, MCP, and caching demos

---

## License

MIT - see [LICENSE](LICENSE).
