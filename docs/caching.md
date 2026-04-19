# Multi-Provider Caching

## Scope

The library exposes provider-native caching controls for repeated static
context, such as system prompts, tool schemas, and retrieved documents. The
implementation covers both request-time cache hints and explicit provider cache
resources where the upstream API supports them.

The library does not add a generic Redis-backed prompt cache. Token-billing
savings come from provider-side prefix or context caches, not from storing raw
prompts locally. A local cache may still be useful for response reuse or
metadata indexing, but that is a separate concern and should remain optional.

## Provider Matrix

### OpenAI

- Automatic prompt caching on supported models.
- Request-level controls:
  - `prompt_cache_key`
  - `prompt_cache_retention`
- Reported usage:
  - `usage.prompt_tokens_details.cached_tokens`
- Explicit cache lifecycle API: not supported.

### Anthropic

- Explicit cache breakpoints on tools and content blocks.
- Top-level automatic caching via `cache_control`.
- Minimum cacheable block size varies by model.
- Reported usage:
  - `cache_creation_input_tokens`
  - `cache_read_input_tokens`
- Explicit cache lifecycle API: not supported.

### Gemini

- Implicit caching on supported models.
- Explicit cache reuse via `cachedContent`.
- Explicit cache resources via `cachedContents`:
  - create
  - get
  - list
  - update TTL or expiration
  - delete
- Reported usage:
  - `cachedContentTokenCount`

## Public API

### Request-Time Controls

`Request.Cache` exposes provider-native request controls through a single
additive struct:

- `Control *CacheControl`
  - Anthropic top-level `cache_control`.
- `Key string`
  - OpenAI `prompt_cache_key`.
- `Retention string`
  - OpenAI `prompt_cache_retention`.
- `CachedContent string`
  - Gemini `cachedContent`.

These fields are optional and ignored by providers that do not support them.

### Explicit Cache Resources

Providers that expose cache-resource lifecycle APIs implement `CacheProvider`.
The current implementation is available for Gemini cached contents.

Types:

- `Cache`
- `CacheUsageMetadata`
- `CreateCacheRequest`
- `UpdateCacheRequest`
- `ListCachesRequest`
- `ListCachesResponse`

Client methods:

- `CreateCache(ctx, provider, req)`
- `GetCache(ctx, provider, name)`
- `ListCaches(ctx, provider, req)`
- `UpdateCache(ctx, provider, req)`
- `DeleteCache(ctx, provider, name)`

If a provider does not support explicit cache resources, the client returns a
clear error. Cache lifecycle operations are routed directly to the named
provider and do not participate in fallback chains.

## Observability

Request and stream responses expose cache usage through:

- `Response.CacheUsage`
- `StreamChunk.CacheUsage`

Aggregate counters remain available on:

- `Stats.CacheCreatedTokens`
- `Stats.CacheReadTokens`

Explicit cache resources expose metadata through `Cache`, including timestamps,
model, display name, and provider-reported usage totals.

The library does not compute estimated cost savings. Pricing varies by provider,
model, and retention policy and should be read from current provider pricing
documentation.

## Compatibility

- Existing Anthropic block-level `CacheControl` usage remains valid.
- `CacheUsage` remains backward-compatible.
- New request fields and client methods are additive.
- Middleware transparently forwards cache lifecycle methods when the wrapped
  provider supports them.

## Official References

- OpenAI prompt caching:
  <https://developers.openai.com/api/docs/guides/prompt-caching>
- Anthropic prompt caching:
  <https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching>
- Gemini caching:
  <https://ai.google.dev/api/caching>
