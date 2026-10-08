package providers

import "strings"

// defaultBaseURLs are the official public endpoints. Azure stays unset because
// each resource has its own host. Perplexity's chat route has no /v1 prefix;
// the provider rewrites that path.
var defaultBaseURLs = map[string]string{
	"openai":     "https://api.openai.com",
	"anthropic":  "https://api.anthropic.com",
	"gemini":     "https://generativelanguage.googleapis.com",
	"cohere":     "https://api.cohere.com",
	"ollama":     "http://localhost:11434",
	"groq":       "https://api.groq.com/openai/v1",
	"deepseek":   "https://api.deepseek.com",
	"mistral":    "https://api.mistral.ai",
	"xai":        "https://api.x.ai",
	"together":   "https://api.together.xyz/v1",
	"openrouter": "https://openrouter.ai/api/v1",
	"fireworks":  "https://api.fireworks.ai/inference/v1",
	"qwen":       "https://dashscope.aliyuncs.com/compatible-mode/v1",
	"qwen-intl":  "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
	"perplexity": "https://api.perplexity.ai",
}

// DefaultBaseURL returns the official base URL for a built-in provider type.
// An empty result means the caller must set baseURL.
func DefaultBaseURL(providerType string) string {
	return defaultBaseURLs[strings.ToLower(strings.TrimSpace(providerType))]
}

// ResolveBaseURL returns the configured base URL, or the official default.
func ResolveBaseURL(providerType, configured string) string {
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured
	}
	return DefaultBaseURL(providerType)
}

func applyDefaultBaseURL(providerType string, cfg Config) Config {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = DefaultBaseURL(providerType)
	}
	return cfg
}
