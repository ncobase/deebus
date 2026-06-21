package deebus

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ncobase/deebus/internal/circuit"
	"github.com/ncobase/deebus/middleware"
	"github.com/ncobase/deebus/providers"
)

// Config is the top-level client configuration.
type Config struct {
	// Providers maps logical provider names to their connection settings.
	Providers map[string]ProviderConfig `yaml:"providers"`

	// Primary is the preferred model in "provider/model" format.
	// Example: "anthropic/claude-opus-4-6"
	Primary string `yaml:"primary"`

	// Fallbacks is an ordered list of "provider/model" strings tried when
	// the primary fails with a fallback-eligible error (anything except 400).
	Fallbacks []string `yaml:"fallbacks"`

	// Timeout is the per-request HTTP timeout in seconds. Default: 30.
	Timeout int `yaml:"timeout"`

	// Retry is the maximum number of additional attempts per provider for
	// transient errors (429, 5xx, network). Default: 2.
	Retry int `yaml:"retry"`

	// RateLimit caps outgoing requests per second per provider.
	// 0 disables rate limiting.
	RateLimit int `yaml:"rateLimit"`

	// CircuitBreaker configures the per-provider circuit breaker.
	// Zero MaxFailures disables the circuit breaker entirely.
	CircuitBreaker CircuitBreakerConfig `yaml:"circuitBreaker"`

	// RequestPolicy optionally validates, normalizes, and snapshots requests
	// before each provider attempt. The zero value is disabled.
	RequestPolicy RequestPolicy `yaml:"requestPolicy"`
}

// CircuitBreakerConfig holds circuit breaker tunables.
type CircuitBreakerConfig struct {
	// MaxFailures is the consecutive-failure count that opens the circuit.
	// Default: 5.  0 = disabled.
	MaxFailures int `yaml:"maxFailures"`

	// ResetTimeout is seconds to wait before allowing a probe (half-open).
	// Default: 60.
	ResetTimeout int `yaml:"resetTimeout"`
}

// ProviderConfig holds the connection parameters for one AI provider.
type ProviderConfig struct {
	Type               string             `yaml:"type"`
	APIKey             string             `yaml:"apiKey"`
	BearerToken        string             `yaml:"bearerToken"`
	BaseURL            string             `yaml:"baseURL"`
	APIMode            string             `yaml:"apiMode"`
	Headers            map[string]string  `yaml:"headers"`
	Organization       string             `yaml:"organization"`
	Project            string             `yaml:"project"`
	UserProject        string             `yaml:"userProject"`
	CredentialProvider CredentialProvider `yaml:"-"`
}

// Validate returns an error if the configuration is incomplete or invalid.
func (c *Config) Validate() error {
	if c.Primary == "" {
		return fmt.Errorf("primary model required")
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider required")
	}
	for name, cfg := range c.Providers {
		if cfg.Type == "" {
			return fmt.Errorf("provider %q: type required", name)
		}
		if cfg.BaseURL == "" {
			return fmt.Errorf("provider %q: baseURL required", name)
		}
		if !isAllowedURL(cfg.BaseURL) {
			return fmt.Errorf("provider %q: baseURL must use https or localhost/127.0.0.1", name)
		}
		// Ollama is a local service and does not require authentication.
		if cfg.Type != "ollama" &&
			cfg.APIKey == "" &&
			cfg.BearerToken == "" &&
			cfg.CredentialProvider == nil &&
			len(cfg.Headers) == 0 {
			return fmt.Errorf("provider %q: apiKey, bearerToken, credentialProvider, or headers required", name)
		}
	}
	// Validate that every model in the fallback chain references a configured provider.
	for _, fb := range c.Fallbacks {
		providerName, _, err := parseModel(fb)
		if err != nil {
			return fmt.Errorf("fallback %q: %w", fb, err)
		}
		if _, ok := c.Providers[providerName]; !ok {
			return fmt.Errorf("fallback %q: provider %q not configured", fb, providerName)
		}
	}
	return nil
}

func isAllowedURL(u string) bool {
	return strings.HasPrefix(u, "https://") ||
		strings.HasPrefix(u, "http://localhost") ||
		strings.HasPrefix(u, "http://127.0.0.1") ||
		strings.HasPrefix(u, "http://0.0.0.0") // Docker / container environments
}

// Client dispatches AI requests across a pool of providers with automatic
// fallback, retry, rate limiting, circuit breaking, and logging.
//
// Client is safe for concurrent use.
type Client struct {
	config    Config
	providers map[string]providers.Provider
	log       *sharedLogger

	// Stats tracks aggregate request/token counts. Read it directly or call
	// Stats.Get() to retrieve all counters atomically.
	Stats *Stats

	mu sync.RWMutex
}

// LoadConfig is an optional convenience helper that reads Config from YAML,
// expands ${ENV_VAR} and $ENV_VAR references, and returns a ready-to-use
// Client. Applications can call NewClient directly when they already own
// configuration loading.
func LoadConfig(path string) (*Client, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	// Expand $VAR / ${VAR} before YAML parsing so secrets are never stored
	// in plaintext configuration files.
	expanded := os.ExpandEnv(string(raw))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return NewClient(cfg)
}

// NewClient creates a Client from cfg and builds the provider middleware stack
// in this order: Logging -> CircuitBreaker -> Retry -> RateLimit -> BaseProvider.
func NewClient(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	setDefaults(&cfg)

	log := newSharedLogger(NoopLogger{})

	c := &Client{
		config:    cfg,
		providers: make(map[string]providers.Provider, len(cfg.Providers)),
		Stats:     &Stats{},
		log:       log,
	}

	timeout := time.Duration(cfg.Timeout) * time.Second

	for name, pcfg := range cfg.Providers {
		p, err := buildProvider(pcfg, timeout, cfg, log)
		if err != nil {
			return nil, fmt.Errorf("build provider %q: %w", name, err)
		}
		c.providers[name] = p
	}

	return c, nil
}

// SetLogger replaces the client's logger. The change propagates immediately to
// every middleware layer. Safe to call concurrently at any time.
func (c *Client) SetLogger(l Logger) {
	c.log.set(l)
}

// Complete sends a completion request. It tries the primary model first, then
// falls back through the configured fallback list on eligible errors.
//
// HTTP 400 (bad request) is never retried or fallen back. It indicates a
// problem with the request itself, not the provider.
func (c *Client) Complete(ctx context.Context, req *Request) (*Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request required")
	}
	base := CloneRequest(req)

	var lastErr error
	for _, modelStr := range c.modelChain() {
		providerName, modelName, err := parseModel(modelStr)
		if err != nil {
			lastErr = err
			continue
		}

		p, ok := c.getProvider(providerName)
		if !ok {
			lastErr = fmt.Errorf("provider %q not configured", providerName)
			continue
		}

		r := CloneRequest(&base)
		r.Model = modelName
		if err := c.applyRequestPolicy(ctx, providerName, modelName, &r); err != nil {
			c.Stats.RecordRequest(false, 0, 0, 0, 0)
			return nil, err
		}
		resp, err := p.Complete(ctx, &r)
		if err == nil {
			c.Stats.RecordRequest(true, resp.InputTokens, resp.OutputTokens,
				resp.CacheUsage.CreatedTokens, resp.CacheUsage.ReadTokens)
			return resp, nil
		}

		lastErr = err
		if !providers.IsFallback(err) {
			break
		}
	}

	c.Stats.RecordRequest(false, 0, 0, 0, 0)
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}

// Stream initiates a streaming completion. Fallback semantics are identical
// to Complete. Stats are recorded when the returned channel is fully consumed
// (closed or done chunk received).
func (c *Client) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	if req == nil {
		return nil, fmt.Errorf("request required")
	}
	base := CloneRequest(req)

	var lastErr error
	for _, modelStr := range c.modelChain() {
		providerName, modelName, err := parseModel(modelStr)
		if err != nil {
			lastErr = err
			continue
		}

		p, ok := c.getProvider(providerName)
		if !ok {
			lastErr = fmt.Errorf("provider %q not configured", providerName)
			continue
		}

		r := CloneRequest(&base)
		r.Model = modelName
		if err := c.applyRequestPolicy(ctx, providerName, modelName, &r); err != nil {
			c.Stats.RecordRequest(false, 0, 0, 0, 0)
			return nil, err
		}
		ch, err := p.Stream(ctx, &r)
		if err == nil {
			return c.wrapStream(ctx, ch), nil
		}

		lastErr = err
		if !providers.IsFallback(err) {
			break
		}
	}

	c.Stats.RecordRequest(false, 0, 0, 0, 0)
	return nil, fmt.Errorf("all providers failed for streaming: %w", lastErr)
}

// Embed generates vector embeddings. Fallback semantics are identical to
// Complete.
func (c *Client) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("embed request required")
	}
	r := *req

	var lastErr error
	for _, modelStr := range c.modelChain() {
		providerName, modelName, err := parseModel(modelStr)
		if err != nil {
			lastErr = err
			continue
		}

		p, ok := c.getProvider(providerName)
		if !ok {
			lastErr = fmt.Errorf("provider %q not configured", providerName)
			continue
		}

		r.Model = modelName
		resp, err := p.Embed(ctx, &r)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		if !providers.IsFallback(err) {
			break
		}
	}

	return nil, fmt.Errorf("all providers failed for embedding: %w", lastErr)
}

func (c *Client) applyRequestPolicy(ctx context.Context, providerName, modelName string, req *Request) error {
	if !c.config.RequestPolicy.Enabled() {
		return nil
	}
	if _, err := c.config.RequestPolicy.ApplyContext(ctx, providerName, modelName, req); err != nil {
		return fmt.Errorf("request policy failed for %s/%s: %w", providerName, modelName, err)
	}
	return nil
}

// Health calls Health on every configured provider and returns a map of
// provider name to error. A nil error means the provider is reachable.
// Providers that return a non-nil error are reported but do not prevent
// other providers from being checked.
func (c *Client) Health(ctx context.Context) map[string]error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	results := make(map[string]error, len(c.providers))
	for name, p := range c.providers {
		results[name] = p.Health(ctx)
	}
	return results
}

// ListModels returns the model catalog for a configured provider name.
func (c *Client) ListModels(ctx context.Context, providerName string) ([]string, error) {
	p, ok := c.getProvider(providerName)
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", providerName)
	}
	return p.ListModels(ctx)
}

// wrapStream proxies chunks from in to a new channel, recording Stats when
// the stream ends (success or failure). It also guards against the consumer
// stopping mid-stream by honouring ctx cancellation on every send.
func (c *Client) wrapStream(ctx context.Context, in <-chan *StreamChunk) <-chan *StreamChunk {
	out := make(chan *StreamChunk, 16)
	go func() {
		defer close(out)
		success := true
		var inputTokens, outputTokens, cacheCreated, cacheRead int
		for {
			select {
			case chunk, ok := <-in:
				if !ok {
					c.Stats.RecordRequest(success, inputTokens, outputTokens, cacheCreated, cacheRead)
					return
				}
				if chunk.Error != nil {
					success = false
				}
				if chunk.Done {
					inputTokens = chunk.InputTokens
					outputTokens = chunk.OutputTokens
					cacheCreated = chunk.CacheUsage.CreatedTokens
					cacheRead = chunk.CacheUsage.ReadTokens
				}
				select {
				case out <- chunk:
				case <-ctx.Done():
					c.Stats.RecordRequest(false, 0, 0, 0, 0)
					return
				}
			case <-ctx.Done():
				c.Stats.RecordRequest(false, 0, 0, 0, 0)
				return
			}
		}
	}()
	return out
}

// modelChain returns [primary, fallback1, fallback2, ...].
func (c *Client) modelChain() []string {
	chain := make([]string, 1, 1+len(c.config.Fallbacks))
	chain[0] = c.config.Primary
	return append(chain, c.config.Fallbacks...)
}

func (c *Client) getProvider(name string) (providers.Provider, bool) {
	c.mu.RLock()
	p, ok := c.providers[name]
	c.mu.RUnlock()
	return p, ok
}

// parseModel validates and splits "provider/model" into its components.
func parseModel(s string) (provider, model string, err error) {
	if strings.Contains(s, "..") {
		return "", "", fmt.Errorf("invalid model %q: path traversal detected", s)
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid model %q: expected provider/model", s)
	}
	return parts[0], parts[1], nil
}

// buildProvider constructs a base provider and wraps it with the configured
// middleware stack.
//
// Wrapping order, from innermost in code to outermost at runtime:
//
//	RateLimit -> Retry -> CircuitBreaker -> Logging
func buildProvider(
	cfg ProviderConfig,
	timeout time.Duration,
	clientCfg Config,
	log *sharedLogger,
) (providers.Provider, error) {
	pcfg := providers.Config{
		APIKey:             cfg.APIKey,
		BearerToken:        cfg.BearerToken,
		BaseURL:            cfg.BaseURL,
		APIMode:            cfg.APIMode,
		Timeout:            timeout,
		Headers:            cloneStringMap(cfg.Headers),
		Organization:       cfg.Organization,
		Project:            cfg.Project,
		UserProject:        cfg.UserProject,
		CredentialProvider: cfg.CredentialProvider,
	}

	var p providers.Provider
	switch cfg.Type {
	case "openai":
		p = providers.NewOpenAI(pcfg)
	case "anthropic":
		p = providers.NewAnthropic(pcfg)
	case "gemini":
		p = providers.NewGemini(pcfg)
	case "ollama":
		p = providers.NewOllama(pcfg)
	case "cohere":
		p = providers.NewCohere(pcfg)
	default:
		return nil, fmt.Errorf("unknown provider type %q", cfg.Type)
	}

	// Layer 1: rate limiter.
	if clientCfg.RateLimit > 0 {
		p = middleware.NewRateLimit(p, clientCfg.RateLimit)
	}

	// Layer 2: retry.
	if clientCfg.Retry > 0 {
		p = middleware.NewRetry(p, clientCfg.Retry)
	}

	// Layer 3: circuit breaker.
	if clientCfg.CircuitBreaker.MaxFailures > 0 {
		p = middleware.NewCircuitBreaker(p, circuit.Config{
			MaxFailures:  clientCfg.CircuitBreaker.MaxFailures,
			ResetTimeout: time.Duration(clientCfg.CircuitBreaker.ResetTimeout) * time.Second,
		})
	}

	// Layer 4: logging.
	p = middleware.NewLogging(p, log)

	return p, nil
}

// setDefaults fills in zero-value config fields with production-safe defaults.
func setDefaults(cfg *Config) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30
	}
	if cfg.Retry == 0 {
		cfg.Retry = 2
	}
	if cfg.CircuitBreaker.MaxFailures > 0 && cfg.CircuitBreaker.ResetTimeout == 0 {
		cfg.CircuitBreaker.ResetTimeout = 60
	}
}
