# Gateway Governance

This document covers the gateway-oriented helpers in `deebus`. They are
designed for applications that expose one or more upstream model providers to
users, teams, spaces, or internal products.

The library still stays below the application boundary. User management,
balances, quotas, database records, permission checks, and API-key storage
belong in the gateway application. `deebus` provides reusable request
preparation, cache-affinity helpers, prompt-safe observability primitives, and
usage/cost calculations based on provider-reported tokens.

## Request Policy

`RequestPolicy` can be configured on `deebus.Config`:

```go
store := false

client, err := deebus.NewClient(deebus.Config{
    Primary: "openai/gpt-4o",
    Providers: map[string]deebus.ProviderConfig{
        "openai": {
            Type:    "openai",
            APIKey:  os.Getenv("OPENAI_API_KEY"),
            BaseURL: "https://api.openai.com",
        },
    },
    RequestPolicy: deebus.RequestPolicy{
        Limits: deebus.RequestLimits{
            MaxMessages:        64,
            MaxTextBytes:       256 * 1024,
            MaxMediaBytes:      8 * 1024 * 1024,
            MaxTools:           64,
            MaxToolSchemaBytes: 128 * 1024,
            MaxMetadataKeys:    32,
            MaxOptionKeys:      32,
        },
        Defaults: deebus.RequestDefaults{
            Store: &store,
        },
        PromptCache: deebus.PromptCachePolicy{
            Enabled:         true,
            Scope:           "ncobase",
            Client:          "console",
            IncludeProvider: true,
            IncludeModel:    true,
            IncludeUser:     true,
            MetadataKeys:    []string{"space_id", "action"},
            Retention:       "24h",
        },
        CacheBreaker: deebus.CacheBreakerPolicy{
            Enabled:                   true,
            AnthropicBillingHeaderCCH: true,
            Replacement:               "stable",
        },
        Fingerprint: deebus.FingerprintOptions{
            Salt:        os.Getenv("REQUEST_FINGERPRINT_SALT"),
            IncludeText: true,
        },
        Reporter: deebus.RequestPolicyReporterFunc(func(ctx context.Context, report deebus.RequestPolicyReport) error {
            return auditStore.SaveAIRequestPolicyReport(ctx, report)
        }),
        FailOnReporterError: false,
    },
})
```

`Client.Complete` and `Client.Stream` apply the policy before each provider
attempt. Each fallback attempt starts from a fresh deep clone of the caller's
request, so provider-specific mutations never leak back to the original
request or across fallback providers.

## Reports

Every policy application produces a `RequestPolicyReport` with:

- provider and model attempted
- prompt-safe request snapshot and fingerprint
- policy changes applied
- rejection status and rejection error when limits fail
- reporter error when the optional reporter fails

Configure `RequestPolicy.Reporter` to receive reports from `Client.Complete`,
`Client.Stream`, or `ApplyContext`:

```go
policy.Reporter = deebus.RequestPolicyReporterFunc(func(ctx context.Context, report deebus.RequestPolicyReport) error {
    return auditStore.SaveAIRequestPolicyReport(ctx, report)
})
```

Reporter errors are recorded on the returned report. They do not fail the model
call unless `FailOnReporterError` is true. Use strict mode only when persisted
audit is a hard requirement.

To use the policy outside `Client`, call it directly:

```go
req := deebus.CloneRequest(originalReq)
report, err := policy.ApplyContext(ctx, "openai", "gpt-4o", &req)
if err != nil {
    return err
}

auditFingerprint := report.Fingerprint
auditChanges := report.Changes
```

The report does not include prompt text. It records change types, hashed cache
keys, request counters, and a prompt-safe `RequestSnapshot`.

## Limits

`RequestLimits` validates:

- message count
- total text bytes
- inline media bytes
- tool count
- approximate tool schema bytes
- metadata key count
- provider option key count

Limits are intentionally provider-neutral. Provider-specific model context
windows, image constraints, and organization quota rules should still be
enforced by the application using its own policy tables.

## Prompt Cache Affinity

`PromptCachePolicy` writes `Request.Cache.Key` and `Request.Cache.Retention`
when enabled. This maps to OpenAI prompt-cache hints in the OpenAI adapters.
The key can be explicit or derived from:

- gateway scope, such as tenant, product, or deployment
- client name
- provider name
- model name
- `Request.UserID`
- selected `Request.Metadata` keys

The builder sanitizes whitespace and separators and shortens long keys with a
SHA-256 suffix:

```go
key := deebus.BuildPromptCacheKey(256, "space-1", "console", "openai", "gpt-4o")
```

Use stable, low-cardinality keys for repeated static prefixes. Do not include
raw prompt text, secrets, request IDs, random UUIDs, or high-cardinality trace
IDs in cache keys.

## Cache-Breaker Rewrites

Some clients inject cache-busting markers into otherwise stable provider
prefixes. `CacheBreakerPolicy` currently supports the Anthropic billing-header
`cch=...` pattern when it appears at the beginning of a system text block:

```go
changed := deebus.NormalizeAnthropicBillingHeaderCCH(req, "stable")
```

The rewrite is opt-in because it mutates prompt text. Enable it only for
trusted client patterns where the marker is known to be semantically inert.

## Prompt-Safe Snapshots

`SnapshotRequest` produces a safe audit object:

```go
snapshot := deebus.SnapshotRequest("anthropic", "claude-opus-4-6", req, deebus.FingerprintOptions{
    Salt:        "deployment-salt",
    IncludeText: true,
})
```

The snapshot includes:

- provider, model, timestamp, and fingerprint
- message count and roles
- content block types and byte counts
- optional text hashes, never text
- tool-call names and argument byte counts, with optional argument hashes and
  never raw arguments
- tool names and approximate schema size
- metadata and option keys, never values
- cache key/resource hashes
- user ID hash

Use `Salt` when fingerprints or hashes leave the process boundary. This reduces
cross-tenant correlation risk.

## Stream Aggregation

Providers report final usage on different stream events. `CollectStream`
consumes a `Client.Stream` result and returns a `StreamResult`:

```go
stream, err := client.Stream(ctx, req)
if err != nil {
    return err
}

result, err := deebus.CollectStream(ctx, stream)
if err != nil {
    log.Printf("partial stream content=%q error=%v", result.Content, err)
}

fmt.Println(result.Content)
fmt.Println(result.InputTokens, result.OutputTokens, result.CacheUsage.ReadTokens)
```

For custom relays, use `StreamAccumulator` and call `Add` for every forwarded
chunk. It preserves partial content when a later chunk contains an error.

## Cost Estimation

`deebus` never hard-codes model prices. Providers change pricing and billing
rules independently, so applications must provide a current pricing table from
configuration, `/sys/options`, or another trusted runtime source.

```go
cost := deebus.EstimateResponseCost(resp, deebus.TokenPricing{
    Currency:        "USD",
    InputPer1K:      0.005,
    OutputPer1K:     0.015,
    CacheWritePer1K: 0.00625,
    CacheReadPer1K:  0.0005,
})
```

The estimator separates:

- regular input tokens
- output tokens
- cache write tokens
- cache read tokens
- reasoning tokens when a separate reasoning price is supplied

Use provider-reported `InputTokens`, `OutputTokens`, `ReasoningTokens`, and
`CacheUsage`. Do not estimate tokens locally for billing-grade records.

## Application Boundary

Keep these responsibilities in the gateway application:

- end-user authentication and authorization
- upstream secret storage and rotation
- quota and balance checks
- persisted request/run records
- per-space or per-user rate limits
- billing ledger writes
- audit event publication
- notification and alert workflows
- admin UI and provider option management

`deebus` should remain a dependency-light provider abstraction and reusable
governance toolkit, not a full gateway server.
