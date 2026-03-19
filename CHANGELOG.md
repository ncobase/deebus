# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.2.0] — 2026-03-19

### Added

- **Environment variable expansion** — `LoadConfig` now calls `os.ExpandEnv` before
  YAML parsing, so `apiKey: ${ANTHROPIC_API_KEY}` works out of the box.
- **`client.Stats`** — public `*Stats` field on `Client`; updated atomically after
  every `Complete`, `Stream`, and `Embed` call. `Stats.Get()` returns total
  requests, total tokens, successes, and failures in one call.
- **`client.SetLogger`** — replaces the active logger at runtime. Uses a
  `sharedLogger` wrapper so the change propagates instantly to every middleware
  layer without rebuilding the provider stack.
- **`rateLimit` config field** — top-level integer (requests per second per
  provider). Set to `0` (the default) to disable.
- **`circuitBreaker` config block** — `maxFailures` and `resetTimeout` fields.
  Set `maxFailures` to `0` (the default) to disable the circuit breaker.
- **`internal/circuit` package** — standalone, zero-dependency circuit breaker
  with a proper Closed → Open → Half-open state machine, configurable
  `MaxFailures`, `ResetTimeout`, and `HalfOpenRequests`.
- **`middleware/circuit.go`** — `CircuitBreakerMiddleware` that wraps any
  `Provider`. Auth and bad-request errors (401/403/400) do not trip the circuit.
- **`internal/log` package** — shared `Logger` interface that breaks the
  circular import between `middleware` and the root `deebus` package.
- **`providers.IsFallback`** — counterpart to `IsRetryable`; reports whether an
  error should trigger trying the next provider in the fallback chain.
- **`providers.ProviderError.Fallback`** — new boolean field; `false` only for
  HTTP 400 (malformed request — no point trying another provider).
- **`providers.ProviderError.RetryAfter`** — populated from the `Retry-After`
  response header on 429 responses (both integer-seconds and HTTP-date formats).
- **`providers.networkError`** — helper that wraps transport-layer errors as a
  retryable, fallback-eligible `ProviderError`.
- **Streaming for Gemini** — `streamGenerateContent?alt=sse` SSE endpoint.
- **Streaming for Ollama** — `/api/chat` NDJSON streaming.
- **Streaming for Cohere** — `/v2/chat` SSE streaming.
- **`deebus.AudioMessage` / `deebus.DocumentMessage`** exported from root
  package (previously only in `providers`).
- Full `go.sum` file — project now builds without additional steps.

### Changed

- **Middleware stack fully wired** — `NewClient` now constructs a
  `Logging → CircuitBreaker → Retry → RateLimit → BaseProvider` chain for every
  provider. Previously the middleware package was dead code.
- **Retry strategy** — switched from fixed linear sleep to equal-jitter
  exponential backoff (`base=500ms`, multiplier `2×`, `cap=30s`). The
  `Retry-After` header on 429 responses overrides the computed delay.
- **Rate limiter** — replaced the discrete burst-refill implementation with a
  continuous token-bucket that refills proportionally to elapsed time. Fixed a
  mutex deadlock in the `acquire` path.
- **`providers.Config`** — removed unused `Model`, `MaxRetries`, and `CacheTTL`
  fields. Providers now read the model exclusively from `req.Model`.
- **`Config.Validate`** — Ollama providers are exempt from the `apiKey`
  requirement (local service; no authentication needed).
- **All providers** — use `req.Model` consistently (Ollama and Cohere previously
  used the stale `config.Model`).
- **`parseError`** — accepts `http.Header` to extract `Retry-After`; always
  passes the provider name (was empty string in Anthropic, Gemini, and Ollama).
- **Root `errors.go`** — removed the unused `deebus.Error` type and duplicate
  `ErrorType` constants; delegated `IsRetryable` / `IsFallback` to
  `providers.IsRetryable` / `providers.IsFallback`.
- **`types.go`** — exports `AudioContent`, `AudioSource`, `DocumentContent`,
  `DocumentSource`; adds `AudioMessage` and `DocumentMessage` constructors.
- **`internal/reliability`** — replaced by `internal/circuit` with the correct
  package declaration (`package circuit`, not `package deebus`).

### Fixed

- **`req.Model` data race** — all public `Client` methods now copy the caller's
  `*Request` before writing `req.Model`, preventing concurrent-use races.
- **Gemini panic** — `Complete` now guards against an empty `Candidates` slice
  before indexing; returns a structured error instead of panicking.
- **Ollama `Embed` context loss** — was calling `http.NewRequestWithContext`
  with `context.Background()`; now uses the caller's `ctx`.
- **Stream fallback** — `Client.Stream` now propagates the last error from all
  providers instead of returning a generic "all providers failed" string.
- **`internal/reliability` package declaration** — files declared `package
  deebus` but lived under `internal/reliability/`, causing a misleading
  import path. Replaced entirely.
- **Circular import** — `middleware/logging.go` imported the root `deebus`
  package for its `Logger` type, making it impossible to import `middleware`
  from `client.go`. Resolved via `internal/log`.

---

## [1.1.0] — 2026-03-18

### Added

- Structured `ProviderError` type with `Retryable` field.
- Per-provider HTTP error parsing with `parseError`.
- Exponential backoff retry (1 s → 2 s → 4 s → 8 s).
- `RateLimitMiddleware` using a token-bucket algorithm.
- `LoggingMiddleware` with a pluggable `Logger` interface.
- `Stats` struct for tracking request and token counts.

### Changed

- Retry strategy upgraded from fixed sleep to exponential backoff.
- Error messages now include provider name and HTTP status code.

---

## [1.0.0] — 2026-03-17

### Added

- Unified `Provider` interface: `Complete`, `Stream`, `Embed`, `Name`, `Health`.
- Five provider implementations: OpenAI, Anthropic, Gemini, Ollama, Cohere.
- YAML configuration via `LoadConfig`.
- Smart primary-plus-fallbacks routing.
- Multimodal message constructors: `SimpleMessage`, `ImageMessage`,
  `AudioMessage`, `DocumentMessage`.
- Streaming (OpenAI SSE, Anthropic SSE).
- Function/tool calling support (OpenAI, Anthropic).
- Structural circuit breaker (state machine without middleware integration).
