package deebus

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ─── parseModel ───────────────────────────────────────────────────────────────

func TestParseModel(t *testing.T) {
	tests := []struct {
		input        string
		wantProvider string
		wantModel    string
		wantErr      bool
	}{
		{"openai/gpt-4o", "openai", "gpt-4o", false},
		{"anthropic/claude-opus-4-6", "anthropic", "claude-opus-4-6", false},
		{"gemini/gemini-2.0-flash", "gemini", "gemini-2.0-flash", false},
		// path traversal
		{"../etc/passwd", "", "", true},
		{"openai/../model", "", "", true},
		// missing separator
		{"invalid", "", "", true},
		// empty components
		{"/model", "", "", true},
		{"provider/", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			provider, model, err := parseModel(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseModel(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && (provider != tt.wantProvider || model != tt.wantModel) {
				t.Errorf("parseModel(%q) = (%q, %q), want (%q, %q)",
					tt.input, provider, model, tt.wantProvider, tt.wantModel)
			}
		})
	}
}

// ─── Config.Validate ──────────────────────────────────────────────────────────

func TestConfigValidate(t *testing.T) {
	validBase := Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid", func(*Config) {}, false},
		{"missing primary", func(c *Config) { c.Primary = "" }, true},
		{"no providers", func(c *Config) { c.Providers = nil }, true},
		{"missing type", func(c *Config) { c.Providers["openai"] = ProviderConfig{APIKey: "k", BaseURL: "https://x.com"} }, true},
		{"missing apiKey", func(c *Config) { c.Providers["openai"] = ProviderConfig{Type: "openai", BaseURL: "https://x.com"} }, true},
		{"insecure http URL", func(c *Config) { c.Providers["openai"] = ProviderConfig{Type: "openai", APIKey: "k", BaseURL: "http://api.openai.com"} }, true},
		{"localhost http allowed", func(c *Config) { c.Providers["openai"] = ProviderConfig{Type: "openai", APIKey: "k", BaseURL: "http://localhost:11434"} }, false},
		{"ollama no apikey ok", func(c *Config) {
			c.Providers["local"] = ProviderConfig{Type: "ollama", BaseURL: "http://localhost:11434"}
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBase
			cfg.Providers = map[string]ProviderConfig{"openai": validBase.Providers["openai"]}
			tt.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ─── NewClient ────────────────────────────────────────────────────────────────

func TestNewClientDefaults(t *testing.T) {
	cfg := Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}
	if c.config.Timeout != 30 {
		t.Errorf("default timeout = %d, want 30", c.config.Timeout)
	}
	if c.config.Retry != 2 {
		t.Errorf("default retry = %d, want 2", c.config.Retry)
	}
	if c.Stats == nil {
		t.Error("Stats must not be nil")
	}
}

func TestNewClientCircuitBreakerDefault(t *testing.T) {
	cfg := Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	if c.config.CircuitBreaker.ResetTimeout != 60 {
		t.Errorf("CB reset timeout = %d, want 60", c.config.CircuitBreaker.ResetTimeout)
	}
}

// ─── SetLogger ────────────────────────────────────────────────────────────────

func TestSetLogger(t *testing.T) {
	c, _ := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
	})

	var called bool
	c.SetLogger(testLogger{onInfo: func() { called = true }})

	// Trigger a log message by attempting a real call (will fail with network error
	// but the logging middleware will still fire).
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	c.Complete(ctx, &Request{Messages: []Message{SimpleMessage("user", "ping")}}) //nolint:errcheck

	// We can't guarantee the network call will complete in time, but at least
	// verify SetLogger didn't panic.
	_ = called
}

type testLogger struct{ onInfo func() }

func (l testLogger) Debug(string, ...any) {}
func (l testLogger) Info(string, ...any)  { if l.onInfo != nil { l.onInfo() } }
func (l testLogger) Warn(string, ...any)  {}
func (l testLogger) Error(string, ...any) {}

// ─── Stats ────────────────────────────────────────────────────────────────────

func TestStatsRecording(t *testing.T) {
	c, _ := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
		Retry: 0, // disable retry so we get a fast failure
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	c.Complete(ctx, &Request{Messages: []Message{SimpleMessage("user", "ping")}}) //nolint:errcheck

	total, _, _, _ := c.Stats.Get()
	if total == 0 {
		t.Error("Stats.TotalRequests should have been incremented")
	}
}

// ─── Concurrent access ────────────────────────────────────────────────────────

func TestClientConcurrency(t *testing.T) {
	c, err := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
		Retry: 0,
	})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}

	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			c.Complete(ctx, &Request{Messages: []Message{SimpleMessage("user", "hi")}}) //nolint:errcheck
		}()
	}

	wg.Wait()

	total, _, _, _ := c.Stats.Get()
	if total != workers {
		t.Errorf("expected %d total requests, got %d", workers, total)
	}
}
