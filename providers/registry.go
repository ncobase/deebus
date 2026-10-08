package providers

import (
	"fmt"
	"strings"
	"sync"
)

// Factory builds a provider from a low-level connection config.
// Register a factory to add a provider type without changing the client.
type Factory func(Config) Provider

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds or replaces a provider type. The name is matched
// case-insensitively. Call it from an init function before NewClient.
func Register(typeName string, factory Factory) {
	typeName = strings.ToLower(strings.TrimSpace(typeName))
	if typeName == "" || factory == nil {
		panic("providers: registration requires a type name and factory")
	}
	registryMu.Lock()
	registry[typeName] = factory
	registryMu.Unlock()
}

// New builds a registered provider. Unknown types return an error.
func New(typeName string, cfg Config) (Provider, error) {
	key := strings.ToLower(strings.TrimSpace(typeName))
	registryMu.RLock()
	factory := registry[key]
	registryMu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("unknown provider type %q", typeName)
	}
	return factory(cfg), nil
}

// AllowsAPIMode reports whether a provider type accepts OpenAI apiMode.
func AllowsAPIMode(typeName string) bool {
	switch strings.ToLower(strings.TrimSpace(typeName)) {
	case "openai", "azure", "groq", "deepseek", "mistral", "xai", "together", "openrouter", "fireworks", "perplexity", "qwen", "qwen-intl":
		return true
	default:
		return false
	}
}

func init() {
	Register("openai", func(cfg Config) Provider { return NewOpenAI(cfg) })
	Register("azure", func(cfg Config) Provider { return NewAzure(cfg) })
	Register("anthropic", func(cfg Config) Provider { return NewAnthropic(cfg) })
	Register("gemini", func(cfg Config) Provider { return NewGemini(cfg) })
	Register("ollama", func(cfg Config) Provider { return NewOllama(cfg) })
	Register("cohere", func(cfg Config) Provider { return NewCohere(cfg) })
	for _, name := range []string{"groq", "deepseek", "mistral", "xai", "together", "openrouter", "fireworks", "perplexity", "qwen", "qwen-intl"} {
		name := name
		Register(name, func(cfg Config) Provider { return NewOpenAICompatible(name, cfg) })
	}
}
