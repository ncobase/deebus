# Authentication

## Scope

The library supports static API keys, static bearer tokens, extra headers, and
runtime credential injection. It does not implement provider-specific OAuth
browser flows or token exchange endpoints.

This keeps the core package dependency-light while still allowing callers to
inject short-lived access tokens, OAuth bearer tokens, or proxy credentials.

## Provider Matrix

### OpenAI

- Direct API auth: API key
- Extra request headers:
  - `OpenAI-Organization`
  - `OpenAI-Project`
- Recommendation:
  - Use API keys or project-scoped service credentials

Official docs:

- <https://developers.openai.com/api/docs/guides/production-best-practices>
- <https://developers.openai.com/api/docs/api-reference/requesting-organization>

### Anthropic

- Direct Claude API auth: API key
- Recommendation:
  - Keep direct Claude API usage on API keys
  - Use bearer tokens only when routing through a proxy or gateway that expects
    them

Official docs:

- <https://docs.anthropic.com/en/api/overview>

### Gemini

- Direct Gemini API auth:
  - API key
  - OAuth bearer token / Application Default Credentials
- Extra request header:
  - `x-goog-user-project`
- Recommendation:
  - Use API keys for simple server-side access
  - Use bearer tokens when project policy or user-scoped auth requires OAuth

Official docs:

- <https://ai.google.dev/gemini-api/docs/api-key>
- <https://ai.google.dev/gemini-api/docs/oauth>

## Runtime Credentials

Use `CredentialProvider` when credentials must be resolved per request:

- OAuth access tokens
- short-lived gateway tokens
- vault-backed credentials
- rotating bearer tokens

The provider returns a `Credentials` value:

- `APIKey`
- `BearerToken`
- `Headers`
- `Organization`
- `Project`
- `UserProject`

Static config and runtime credentials are merged. Runtime values override static
ones when both are present.

`CredentialProvider` is programmatic only. It is not loaded from YAML.
