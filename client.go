package deebus

import (
	"context"
	"fmt"
	"net/url"
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

	// RetryConfigured preserves an explicit Retry value, including zero.
	// Leave false when constructing Config manually to use the default retry
	// count. LoadConfig sets this automatically when the YAML contains retry.
	RetryConfigured bool `yaml:"-"`

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
	// 0 disables the circuit breaker. There is no implicit default of 5.
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

// UnmarshalYAML records whether retry was explicitly set so retry: 0 disables
// retry instead of being treated as an omitted default.
func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	type rawConfig Config
	var decoded rawConfig
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		if value.Content[i].Value == "retry" {
			decoded.RetryConfigured = true
			break
		}
	}
	*c = Config(decoded)
	return nil
}

// Validate returns an error if the configuration is incomplete or invalid.
func (c *Config) Validate() error {
	if c.Primary == "" {
		return fmt.Errorf("primary model required")
	}
	if c.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}
	if c.Retry < 0 {
		return fmt.Errorf("retry must be non-negative")
	}
	if c.RateLimit < 0 {
		return fmt.Errorf("rateLimit must be non-negative")
	}
	if c.CircuitBreaker.MaxFailures < 0 {
		return fmt.Errorf("circuitBreaker.maxFailures must be non-negative")
	}
	if c.CircuitBreaker.ResetTimeout < 0 {
		return fmt.Errorf("circuitBreaker.resetTimeout must be non-negative")
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider required")
	}
	for name, cfg := range c.Providers {
		if cfg.Type == "" {
			return fmt.Errorf("provider %q: type required", name)
		}
		if err := validateProviderAPIMode(name, cfg); err != nil {
			return err
		}
		if cfg.BaseURL == "" {
			return fmt.Errorf("provider %q: baseURL required", name)
		}
		if !isAllowedURL(cfg.BaseURL) {
			return fmt.Errorf("provider %q: baseURL must use https, or http on localhost, 127.0.0.1, ::1, or 0.0.0.0", name)
		}
		if err := providers.ValidateHeaderSafety(cfg.Headers, map[string]string{
			"apiKey":       cfg.APIKey,
			"bearerToken":  cfg.BearerToken,
			"organization": cfg.Organization,
			"project":      cfg.Project,
			"userProject":  cfg.UserProject,
		}); err != nil {
			return fmt.Errorf("provider %q: %w", name, err)
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
	if err := c.validateModelRef("primary", c.Primary); err != nil {
		return err
	}
	for _, fb := range c.Fallbacks {
		if err := c.validateModelRef("fallback", fb); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) validateModelRef(kind, ref string) error {
	providerName, _, err := parseModel(ref)
	if err != nil {
		return fmt.Errorf("%s %q: %w", kind, ref, err)
	}
	if _, ok := c.Providers[providerName]; !ok {
		return fmt.Errorf("%s %q: provider %q not configured", kind, ref, providerName)
	}
	return nil
}

func validateProviderAPIMode(name string, cfg ProviderConfig) error {
	mode := strings.ToLower(strings.TrimSpace(cfg.APIMode))
	if mode == "" {
		return nil
	}
	if cfg.Type != "openai" {
		return fmt.Errorf("provider %q: apiMode is supported only for openai", name)
	}
	switch mode {
	case "chat_completions", "responses":
		return nil
	default:
		return fmt.Errorf("provider %q: apiMode must be chat_completions or responses", name)
	}
}

// isAllowedURL accepts HTTPS endpoints and plaintext HTTP only for exact
// loopback hosts. Prefix checks are intentionally not used: "http://localhost.evil.com"
// and "http://127.0.0.1@evil.com" must be rejected.
func isAllowedURL(raw string) bool {
	if strings.ContainsAny(raw, "\r\n\t") {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		switch strings.ToLower(u.Hostname()) {
		case "localhost", "127.0.0.1", "0.0.0.0", "::1":
			return true
		default:
			return false
		}
	default:
		return false
	}
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
	if err := c.config.RequestPolicy.Limits.ValidateEmbed(req); err != nil {
		c.Stats.RecordRequest(false, 0, 0, 0, 0)
		return nil, fmt.Errorf("request policy failed: %w", err)
	}

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

		attempt := *req
		attempt.Model = modelName
		resp, err := p.Embed(ctx, &attempt)
		if err == nil {
			c.Stats.RecordRequest(true, resp.TokensUsed, 0, 0, 0)
			return resp, nil
		}

		lastErr = err
		if !providers.IsFallback(err) {
			break
		}
	}

	c.Stats.RecordRequest(false, 0, 0, 0, 0)
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
	names := make([]string, 0, len(c.providers))
	provs := make([]providers.Provider, 0, len(c.providers))
	for name, p := range c.providers {
		names = append(names, name)
		provs = append(provs, p)
	}
	c.mu.RUnlock()

	results := make(map[string]error, len(names))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range names {
		wg.Add(1)
		go func(name string, p providers.Provider) {
			defer wg.Done()
			err := p.Health(ctx)
			mu.Lock()
			results[name] = err
			mu.Unlock()
		}(names[i], provs[i])
	}
	wg.Wait()
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
	if cfg.Retry == 0 && !cfg.RetryConfigured {
		cfg.Retry = 2
	}
	if cfg.CircuitBreaker.MaxFailures > 0 && cfg.CircuitBreaker.ResetTimeout == 0 {
		cfg.CircuitBreaker.ResetTimeout = 60
	}
}
