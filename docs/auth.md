# Authentication

## Scope

The library supports static API keys, static bearer tokens, extra headers,
runtime credential injection, and provider-specific access-key signatures.
It does not implement provider-specific OAuth browser flows or token exchange
endpoints.

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

### Access key and secret

Use `accessKey` and `secret` together, and do not also set `apiKey`.

- `kling` signs an HS256 JWT. The request sends `Authorization: Bearer`.
- `hunyuan` signs Tencent Cloud TC3-HMAC-SHA256. `accessKey` is SecretId and `secret` is SecretKey. `region` defaults to `ap-guangzhou`.
- `jimeng` signs Volcengine HMAC-SHA256 for `visual.volcengineapi.com`. `region` defaults to `cn-north-1` and the service is `cv`.

`doubao` stays on an Ark API key. Other provider types reject an access key.

## Runtime Credentials

Use `CredentialProvider` when credentials must be resolved per request:

- OAuth access tokens
- short-lived gateway tokens
- vault-backed credentials
- rotating bearer tokens

The provider returns a `Credentials` value:

- `APIKey`
- `BearerToken`
- `AccessKey`
- `Secret`
- `Headers`
- `Organization`
- `Project`
- `UserProject`

Static config and runtime credentials are merged. Runtime values override static
ones when both are present.

Header names and credential values are rejected when they contain CR, LF, or
NUL. Provider errors redact bearer tokens, `sk-` keys, and query secrets such
as Gemini `key=` before those errors are returned to the caller.

`CredentialProvider` is programmatic only. It is not loaded from YAML.
