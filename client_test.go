package deebus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type testCredentialProvider struct {
	creds Credentials
	err   error
}

func (p testCredentialProvider) Credentials(context.Context) (Credentials, error) {
	return p.creds, p.err
}

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
		{"bearer token accepted", func(c *Config) {
			c.Providers["openai"] = ProviderConfig{Type: "openai", BearerToken: "tok", BaseURL: "https://x.com"}
		}, false},
		{"headers accepted", func(c *Config) {
			c.Providers["openai"] = ProviderConfig{
				Type:    "openai",
				BaseURL: "https://x.com",
				Headers: map[string]string{"Authorization": "Bearer tok"},
			}
		}, false},
		{"credential provider accepted", func(c *Config) {
			c.Providers["openai"] = ProviderConfig{
				Type:               "openai",
				BaseURL:            "https://x.com",
				CredentialProvider: testCredentialProvider{creds: Credentials{BearerToken: "tok"}},
			}
		}, false},
		{"insecure http URL", func(c *Config) {
			c.Providers["openai"] = ProviderConfig{Type: "openai", APIKey: "k", BaseURL: "http://api.openai.com"}
		}, true},
		{"localhost http allowed", func(c *Config) {
			c.Providers["openai"] = ProviderConfig{Type: "openai", APIKey: "k", BaseURL: "http://localhost:11434"}
		}, false},
		{"0.0.0.0 allowed for Docker", func(c *Config) {
			c.Providers["openai"] = ProviderConfig{Type: "openai", APIKey: "k", BaseURL: "http://0.0.0.0:11434"}
		}, false},
		{"ollama no apikey ok", func(c *Config) {
			c.Providers["local"] = ProviderConfig{Type: "ollama", BaseURL: "http://localhost:11434"}
		}, false},
		{"fallback references unconfigured provider", func(c *Config) {
			c.Fallbacks = []string{"anthropic/claude-opus-4-6"}
		}, true},
		{"fallback references configured provider", func(c *Config) {
			c.Providers["anthropic"] = ProviderConfig{Type: "anthropic", APIKey: "k", BaseURL: "https://api.anthropic.com"}
			c.Fallbacks = []string{"anthropic/claude-opus-4-6"}
		}, false},
		{"fallback invalid format", func(c *Config) {
			c.Fallbacks = []string{"invalid"}
		}, true},
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

func TestNewClientExplicitZeroRetry(t *testing.T) {
	c, err := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
		Retry:           0,
		RetryConfigured: true,
	})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	if c.config.Retry != 0 {
		t.Fatalf("explicit zero retry = %d, want 0", c.config.Retry)
	}
}

func TestLoadConfigExplicitZeroRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deebus.yaml")
	err := os.WriteFile(path, []byte(`
providers:
  openai:
    type: openai
    apiKey: sk-test
    baseURL: https://api.openai.com
primary: openai/gpt-4o
retry: 0
`), 0o600)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if c.config.Retry != 0 {
		t.Fatalf("yaml retry = %d, want 0", c.config.Retry)
	}
	if !c.config.RetryConfigured {
		t.Fatal("RetryConfigured was not preserved from yaml")
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
	c.Complete(ctx, &Request{Messages: []Message{TextMessage("user", "ping")}}) //nolint:errcheck

	// We can't guarantee the network call will complete in time, but at least
	// verify SetLogger didn't panic.
	_ = called
}

type testLogger struct{ onInfo func() }

func (l testLogger) Debug(string, ...any) {}
func (l testLogger) Info(string, ...any) {
	if l.onInfo != nil {
		l.onInfo()
	}
}
func (l testLogger) Warn(string, ...any)  {}
func (l testLogger) Error(string, ...any) {}

func TestStatsRecording(t *testing.T) {
	c, _ := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
		Retry:           0, // disable retry so we get a fast failure
		RetryConfigured: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	c.Complete(ctx, &Request{Messages: []Message{TextMessage("user", "ping")}}) //nolint:errcheck

	total, _, _, _, _ := c.Stats.Get()
	if total == 0 {
		t.Error("Stats.TotalRequests should have been incremented")
	}
}

func TestClientConcurrency(t *testing.T) {
	c, err := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
		Retry:           0,
		RetryConfigured: true,
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
			c.Complete(ctx, &Request{Messages: []Message{TextMessage("user", "hi")}}) //nolint:errcheck
		}()
	}

	wg.Wait()

	total, _, _, _, _ := c.Stats.Get()
	if total != workers {
		t.Errorf("expected %d total requests, got %d", workers, total)
	}
}

func TestClientHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "gpt-5.4-mini"}},
		})
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: srv.URL},
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}

	results := c.Health(context.Background())
	if _, ok := results["openai"]; !ok {
		t.Error("Health() must return an entry for every configured provider")
	}
	if results["openai"] != nil {
		t.Fatalf("Health() error = %v", results["openai"])
	}
}

func TestClientListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "gpt-5.4-mini"},
				{"id": "gpt-5.4"},
				{"id": "gpt-5.4-mini"},
			},
		})
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: srv.URL},
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}

	models, err := c.ListModels(context.Background(), "openai")
	if err != nil {
		t.Fatalf("ListModels() error: %v", err)
	}
	if len(models) != 2 || models[0] != "gpt-5.4" || models[1] != "gpt-5.4-mini" {
		t.Fatalf("models = %#v", models)
	}
}

func TestFallbackValidation(t *testing.T) {
	// Fallback referencing a provider not in the providers map.
	_, err := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
		Fallbacks: []string{"anthropic/claude-opus-4-6"}, // anthropic not configured
	})
	if err == nil {
		t.Error("expected error for fallback referencing unconfigured provider")
	}
}

func TestStreamStatsRecordedOnFailure(t *testing.T) {
	c, _ := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
		Retry:           0,
		RetryConfigured: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	c.Stream(ctx, &Request{Messages: []Message{TextMessage("user", "hi")}}) //nolint:errcheck

	total, _, _, _, _ := c.Stats.Get()
	if total == 0 {
		t.Error("Stats must be incremented even when Stream returns an error")
	}
}

func TestClientCacheManagementGemini(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/cachedContents":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":       "cachedContents/demo",
				"model":      "models/gemini-2.5-flash",
				"createTime": "2026-04-19T12:00:00Z",
				"updateTime": "2026-04-19T12:00:00Z",
				"expireTime": "2026-04-19T12:05:00Z",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1beta/cachedContents/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":       "cachedContents/demo",
				"model":      "models/gemini-2.5-flash",
				"createTime": "2026-04-19T12:00:00Z",
				"updateTime": "2026-04-19T12:00:00Z",
				"expireTime": "2026-04-19T12:05:00Z",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1beta/cachedContents":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cachedContents": []map[string]any{
					{
						"name":       "cachedContents/demo",
						"model":      "models/gemini-2.5-flash",
						"createTime": "2026-04-19T12:00:00Z",
						"updateTime": "2026-04-19T12:00:00Z",
						"expireTime": "2026-04-19T12:05:00Z",
					},
				},
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1beta/cachedContents/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":       "cachedContents/demo",
				"model":      "models/gemini-2.5-flash",
				"createTime": "2026-04-19T12:00:00Z",
				"updateTime": "2026-04-19T12:01:00Z",
				"expireTime": "2026-04-19T12:10:00Z",
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1beta/cachedContents/demo":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Primary: "gemini/gemini-2.5-flash",
		Providers: map[string]ProviderConfig{
			"gemini": {Type: "gemini", APIKey: "sk-test", BaseURL: srv.URL},
		},
		Retry:           0,
		RetryConfigured: true,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx := context.Background()

	created, err := c.CreateCache(ctx, "gemini", &CreateCacheRequest{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "hello")},
	})
	if err != nil {
		t.Fatalf("CreateCache: %v", err)
	}
	if created.Name != "cachedContents/demo" {
		t.Fatalf("CreateCache.Name = %q, want cachedContents/demo", created.Name)
	}

	if _, err := c.GetCache(ctx, "gemini", "cachedContents/demo"); err != nil {
		t.Fatalf("GetCache: %v", err)
	}
	if _, err := c.ListCaches(ctx, "gemini", nil); err != nil {
		t.Fatalf("ListCaches: %v", err)
	}
	if _, err := c.UpdateCache(ctx, "gemini", &UpdateCacheRequest{Name: "cachedContents/demo", TTL: time.Minute}); err != nil {
		t.Fatalf("UpdateCache: %v", err)
	}
	if err := c.DeleteCache(ctx, "gemini", "cachedContents/demo"); err != nil {
		t.Fatalf("DeleteCache: %v", err)
	}
}

func TestClientCacheManagementUnsupportedProvider(t *testing.T) {
	c, err := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: "https://api.openai.com"},
		},
		Retry: 0,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = c.CreateCache(context.Background(), "openai", &CreateCacheRequest{
		Model:    "gpt-4o",
		Messages: []Message{TextMessage("user", "hello")},
	})
	if err == nil {
		t.Fatal("CreateCache should fail for unsupported provider")
	}
}
