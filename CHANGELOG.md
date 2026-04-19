# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added

- **`Request.Cache *CacheOptions`** - unified request-level cache controls across providers:
  - **Anthropic**: top-level automatic `cache_control`
  - **OpenAI**: `prompt_cache_key` and `prompt_cache_retention`
  - **Gemini**: explicit `cachedContent` reuse
- **Explicit cache resource management** - `Client.CreateCache`, `GetCache`, `ListCaches`, `UpdateCache`, and `DeleteCache` for providers that support cache lifecycle APIs. Gemini `cachedContents` is implemented in this release.
- **`docs/caching.md`** - multi-provider caching reference and provider matrix.
- **Dynamic credential support** - static bearer tokens, extra headers, runtime `CredentialProvider`, OpenAI org/project headers, and Gemini `x-goog-user-project`.
- **`docs/auth.md`** - authentication matrix and runtime credential guidance.
- **Unit tests** for request serialization and Gemini cache lifecycle operations.

### Changed

- **Anthropic prompt caching** - prompt caching is treated as generally available; the legacy `anthropic-beta: prompt-caching-2024-07-31` header is no longer sent.
- **README caching docs** - updated to describe Anthropic automatic caching, OpenAI request hints, and Gemini explicit caches.

## [1.6.0] - 2026-03-19

### Added

- **`Stats.CacheCreatedTokens` / `Stats.CacheReadTokens`** - aggregate atomic counters for prompt-cache activity across all requests and providers. `RecordRequest` now accepts `cacheCreated, cacheRead int` parameters; `wrapStream` captures cache values from the Done chunk.
- **`Response.ReasoningTokens` / `StreamChunk.ReasoningTokens`** - subset of `OutputTokens` used for internal chain-of-thought reasoning. Populated from `completion_tokens_details.reasoning_tokens` (OpenAI o-series) and `thoughtsTokenCount` (Gemini thinking models).
- **Gemini context caching** - `cachedContentTokenCount` from `usageMetadata` is now parsed in both `Complete` and `Stream` and reported via `CacheUsage.ReadTokens`.
- **Gemini `thoughtsTokenCount`** - thinking-model tokens are now included in `OutputTokens` (billed in addition to `candidatesTokenCount`); also reported as `ReasoningTokens`.
- **Integration test suite** (`integration_test.go`) - real-API regression tests for Anthropic, OpenAI, and Gemini covering complete, stream, embed, prompt caching, UserID attribution, and multi-provider fallback. Tests run only when `DEEBUS_RUN_INTEGRATION=1` and skip when `token.test` is absent.

### Fixed

- **Anthropic `InputTokens` undercount** - the API returns `input_tokens` as only the uncached portion of the input. `InputTokens` now equals the true total: `input_tokens + cache_read_input_tokens + cache_creation_input_tokens`. Affects both `Complete` and `Stream`.
- **Gemini `TokensUsed` unreliable** - `totalTokenCount` from the Gemini API excludes `cachedContentTokenCount`. `TokensUsed` is now computed as `InputTokens + OutputTokens` instead of trusting `totalTokenCount`.

---

## [1.5.0] - 2026-03-19

### Added

- **Prompt caching** - user-controlled `cache_control` markers, zero library-side policy.
  - `CacheControl` struct with `Type` (`"ephemeral"`) and `TTL` (`"5m"` default, `"1h"` for longer-lived content).
  - `CacheControl` field on `TextContent`, `ImageContent`, `DocumentContent`, and `Tool` - set at whichever granularity is appropriate for the use case.
  - **Anthropic**: `anthropicHasCacheControl` detects markers; `anthropic-beta: prompt-caching-2024-07-31` header added automatically when caching is active. `BuildAnthropicSystem` serialises system messages as a content-block array when cache_control is present, or a plain string otherwise (backward-compatible). Cache breakpoint order follows the Anthropic spec: Tools -> System -> Messages.
  - **OpenAI**: automatic server-side caching; `stream_options.include_usage` added to streaming requests so cached-token counts are always available in the final chunk.
  - `CacheUsage` in `Response` and `StreamChunk`: `CreatedTokens` (cache writes, Anthropic) and `ReadTokens` (cache hits, Anthropic + OpenAI).
- **`UserID string`** on `Request` - forwarded as `metadata.user_id` (Anthropic) and `user` (OpenAI) for provider-side abuse detection and per-user rate limiting.
- **`TextMessage`** replaces `SimpleMessage` as the primary text-message constructor. Naming is now consistent with `ImageMessage`, `AudioMessage`, `DocumentMessage`, `AssistantMessage`, `ToolResultMessage`.
- **`examples/06-caching`** - demonstrates all three caching patterns: system prompt, tool-definition boundary, and large retrieved document in the user turn.

### Changed

- `SimpleMessage` removed. All internal usages updated to `TextMessage`. No backward-compatibility shim - the library has no published releases.

---

## [1.4.0] - 2026-03-19

### Added

- **`mcp` package** - MCP client (Model Context Protocol, spec 2025-03-26) with zero new dependencies.
  - `NewStdioClient` - launches a subprocess and communicates via stdin/stdout (newline-delimited JSON-RPC 2.0).
  - `NewHTTPClient` - connects to a remote server via the Streamable HTTP transport; handles JSON and SSE responses, `Mcp-Session-Id` session management, and `DELETE` on close.
  - `conn` - shared request/response correlation layer (pending map + atomic ID generation, 4 MB scanner buffer for large payloads).
  - `Client.Tools` - fetches all pages of `tools/list`, converts to `providers.Tool`, caches results, and automatically invalidates the cache when the server sends `notifications/tools/list_changed`.
  - `Client.Execute` - `AgentToolFunc`-compatible; tool-level `IsError` is returned as prefixed text rather than a Go error, so the model can observe and self-correct.
  - `Client.CallTool` - returns the full `CallToolResult` for callers that need the `IsError` flag or non-text content items.
  - `WithNotificationHandler` option for receiving server-initiated notifications.
- **Agent loop enhancements** (`agent.go`, extracted from `client.go`):
  - **Parallel tool execution** - when `DisableParallel` is `false` (the default), all tool calls returned in one model response are dispatched concurrently via goroutines + `sync.WaitGroup`; results are collected in original order.
  - **`AgentConfig.Hook`** - `func(AgentEvent)` callback fired synchronously on every observable action: `llm_request`, `llm_response`, `tool_call`, `tool_result`, `done`, `error`. Includes elapsed `Duration` and `TokensUsed`.
  - **`AgentConfig.MaxHistoryMessages`** - sliding-window context management; preserves system messages and retains the most recent N non-system messages.
  - **`AgentConfig.DisableParallel`** - opt-out flag for sequential tool execution.
  - **`AgentEventType` constants** - `EventLLMRequest`, `EventLLMResponse`, `EventToolCall`, `EventToolResult`, `EventDone`, `EventError`.

### Changed

- Agent code moved from `client.go` to dedicated `agent.go`; `client.go` now contains only client construction and provider dispatch.
- `applyAgentDefaults` replaces `agentDefaults`; `DisableParallel` bool (opt-out) replaces the earlier `Parallel` bool (opt-in) to work correctly with Go's zero value.

---

## [1.3.0] - 2026-03-19

### Added

- **Full tool calling for all five providers** with streaming assembly.
  - **OpenAI** - accumulates `tool_calls[index].function.arguments` fragments by index across stream deltas; emits assembled `ToolCalls` in the Done chunk.
  - **Anthropic** - parses `content_block_start` / `content_block_delta` (`input_json_delta`) / `content_block_stop` events; accumulates per block-index; reports `TokensUsed` from `message_delta`.
  - **Gemini** - `functionCall` parts parsed in both `Complete` and `Stream`; `TotalTokenCount` mapped to `TokensUsed`.
  - **Ollama** - `tool_calls` parsed from the done chunk; object arguments marshalled to JSON string.
  - **Cohere** - `tool-call-start` / `tool-call-delta` / `tool-call-end` accumulation; `message-end` carries `TokensUsed`.
- **Multi-turn tool calling** - providers now understand `role="tool"` and `role="assistant"+ToolCalls` messages:
  - `Message.ToolCallID`, `Message.ToolCalls`, `Message.Name` fields added.
  - `AssistantMessage(content, toolCalls)` and `ToolResultMessage(toolCallID, name, result)` constructors.
  - `ConvertToOpenAIFormat` - `role="tool"` -> flat `{role, tool_call_id, content}`; assistant with tool calls -> `{role, tool_calls, content:null}`.
  - `ConvertToAnthropicFormat` - `role="tool"` -> `role="user"` with `tool_result` content block; assistant with tool calls -> `tool_use` content array (input as JSON object).
  - `ConvertToGeminiFormat` - `role="tool"` -> `functionResponse` part (parsed JSON or `{result:...}` wrapper); assistant with tool calls -> `functionCall` parts.
  - `cohereMessages` helper for Cohere - `role="tool"` -> `{role, tool_call_id, content:[{type:document,...}]}`; assistant with tool calls -> `{role, tool_calls, tool_plan}`.
- **`RunAgent` / `RunAgentStream`** - initial agent loop implementation.
- **`AssistantMessage` / `ToolResultMessage`** re-exported from root `deebus` package.
- **`FunctionSchema.Strict`** - OpenAI structured outputs flag.
- **`StreamChunk.ToolCalls []ToolCall`** - replaces the former `*ToolCall` singular field; populated in the Done chunk when the model called tools.
- **`StreamChunk.TokensUsed`** - populated in the Done chunk from provider usage data.
- **`EmbedRequest.InputType`** - passed through to Cohere as `input_type`; mapped to Gemini `taskType`.
- **Gemini embeddings** - `batchEmbedContents` endpoint; `InputType` mapped to `RETRIEVAL_QUERY`, `RETRIEVAL_DOCUMENT`, `CLASSIFICATION`, `CLUSTERING`.
- **Anthropic `defaultMaxTokens = 4096`** - replaces the previous 1024 default.
- **`isAllowedURL`** - added `http://0.0.0.0` for Docker / container environments.
- **`Config.Validate`** - verifies that every fallback references a configured provider at `NewClient` time.
- **`Client.Health`** - calls `Health` on all providers; returns `map[string]error`.
- **`wrapStream`** - captures `TokensUsed` from the Done chunk; records `Stats` on stream end (success or failure).

### Fixed

- `Client.Stream` now records `Stats.RecordRequest(false, 0)` when `Stream` itself errors (not just when a chunk carries an error).

---

## [1.2.0] - 2026-03-19

### Added

- **Environment variable expansion** - `LoadConfig` calls `os.ExpandEnv` before YAML parsing.
- **`client.Stats`** - `*Stats` field updated atomically after every `Complete`, `Stream`, and `Embed`. `Stats.Get()` returns totals, tokens, successes, and failures.
- **`client.SetLogger`** - replaces the active logger at runtime via `sharedLogger`; propagates instantly to every middleware layer.
- **`rateLimit` config field** - requests per second per provider (0 = disabled).
- **`circuitBreaker` config block** - `maxFailures` and `resetTimeout`.
- **`internal/circuit`** - standalone circuit breaker: Closed -> Open -> Half-open state machine.
- **`middleware/circuit.go`** - `CircuitBreakerMiddleware`; auth and bad-request errors do not trip the circuit.
- **`internal/log`** - shared `Logger` interface that breaks the circular import between `middleware` and the root package.
- **`providers.IsFallback`** - reports whether an error should trigger the next provider.
- **`providers.ProviderError.Fallback`** - `false` only for HTTP 400.
- **`providers.ProviderError.RetryAfter`** - populated from `Retry-After` on 429 (integer-seconds and HTTP-date formats).
- **`providers.networkError`** - wraps transport-layer errors as retryable, fallback-eligible.
- **Streaming for Gemini** - `streamGenerateContent?alt=sse` SSE endpoint.
- **Streaming for Ollama** - `/api/chat` NDJSON streaming.
- **Streaming for Cohere** - `/v2/chat` SSE streaming.
- **`AudioMessage` / `DocumentMessage`** exported from root package.

### Changed

- **Middleware stack fully wired** - `NewClient` now builds `Logging -> CircuitBreaker -> Retry -> RateLimit -> BaseProvider` for every provider.
- **Retry strategy** - equal-jitter exponential backoff (`base=500ms`, `cap=30s`); `Retry-After` overrides the computed delay.
- **Rate limiter** - continuous token-bucket replacing discrete burst-refill; fixed mutex deadlock in `acquire`.
- **`providers.Config`** - removed unused `Model`, `MaxRetries`, `CacheTTL` fields.
- **`Config.Validate`** - Ollama exempt from `apiKey` requirement.

### Fixed

- `req.Model` data race - all public methods copy the caller's `*Request` before writing `req.Model`.
- Gemini panic on empty `Candidates` slice.
- Ollama `Embed` context loss (`context.Background()` -> caller's `ctx`).
- Stream fallback propagated last error correctly.
- Circular import between `middleware` and root package via `internal/log`.

---

## [1.1.0] - 2026-03-18

### Added

- Structured `ProviderError` with `Retryable` field.
- Per-provider HTTP error parsing via `parseError`.
- Exponential backoff retry (1 s -> 2 s -> 4 s -> 8 s).
- `RateLimitMiddleware` using a token-bucket algorithm.
- `LoggingMiddleware` with pluggable `Logger` interface.
- `Stats` struct for tracking request and token counts.

### Changed

- Retry strategy upgraded from fixed sleep to exponential backoff.
- Error messages include provider name and HTTP status code.

---

## [1.0.0] - 2026-03-17

### Added

- Unified `Provider` interface: `Complete`, `Stream`, `Embed`, `Name`, `Health`.
- Five provider implementations: OpenAI, Anthropic, Gemini, Ollama, Cohere.
- YAML configuration via `LoadConfig`.
- Primary-plus-fallbacks routing.
- Multimodal message constructors: `TextMessage`, `ImageMessage`, `AudioMessage`, `DocumentMessage`.
- Streaming (OpenAI SSE, Anthropic SSE).
- Function/tool calling for OpenAI and Anthropic.
- Structural circuit breaker (state machine without middleware integration).
