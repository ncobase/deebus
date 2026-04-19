package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNormalizeOpenAICacheRetention(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"in_memory", "in_memory", false},
		{"in-memory", "in_memory", false},
		{"24h", "24h", false},
		{"30m", "", true},
	}

	for _, tt := range tests {
		got, err := normalizeOpenAICacheRetention(tt.input)
		if (err != nil) != tt.wantErr {
			t.Fatalf("normalizeOpenAICacheRetention(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
		}
		if got != tt.want {
			t.Fatalf("normalizeOpenAICacheRetention(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateCacheExpiry(t *testing.T) {
	if err := validateCacheExpiry(time.Minute, time.Now()); err == nil {
		t.Fatal("validateCacheExpiry should reject ttl and expiresAt together")
	}
	if err := validateCacheExpiry(-time.Second, time.Time{}); err == nil {
		t.Fatal("validateCacheExpiry should reject negative ttl")
	}
	if err := validateCacheExpiry(time.Minute, time.Time{}); err != nil {
		t.Fatalf("validateCacheExpiry unexpected error: %v", err)
	}
}

func TestOpenAICompleteIncludesPromptCacheFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s, want /v1/chat/completions", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if got := body["prompt_cache_key"]; got != "tenant:123" {
			t.Fatalf("prompt_cache_key = %#v, want %q", got, "tenant:123")
		}
		if got := body["prompt_cache_retention"]; got != "in_memory" {
			t.Fatalf("prompt_cache_retention = %#v, want %q", got, "in_memory")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     1200,
				"completion_tokens": 10,
				"total_tokens":      1210,
				"prompt_tokens_details": map[string]any{
					"cached_tokens": 1000,
				},
				"completion_tokens_details": map[string]any{
					"reasoning_tokens": 0,
				},
			},
			"model": "gpt-5.4",
		})
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "sk-test", BaseURL: srv.URL, Timeout: time.Second})
	resp, err := p.Complete(context.Background(), &Request{
		Model:    "gpt-5.4",
		Messages: []Message{TextMessage("user", "hello")},
		Cache: &CacheOptions{
			Key:       "tenant:123",
			Retention: "in-memory",
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.CacheUsage.ReadTokens != 1000 {
		t.Fatalf("CacheUsage.ReadTokens = %d, want 1000", resp.CacheUsage.ReadTokens)
	}
}

func TestAnthropicCompleteIncludesTopLevelCacheControl(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("anthropic-beta"); got != "" {
			t.Fatalf("anthropic-beta header = %q, want empty", got)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		cc, ok := body["cache_control"].(map[string]any)
		if !ok {
			t.Fatalf("cache_control missing or wrong type: %#v", body["cache_control"])
		}
		if got := cc["type"]; got != "ephemeral" {
			t.Fatalf("cache_control.type = %#v, want %q", got, "ephemeral")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "ok"},
			},
			"usage": map[string]any{
				"input_tokens":                5,
				"output_tokens":               2,
				"cache_creation_input_tokens": 100,
				"cache_read_input_tokens":     25,
			},
			"model":       "claude-sonnet-4-6",
			"stop_reason": "end_turn",
		})
	}))
	defer srv.Close()

	p := NewAnthropic(Config{APIKey: "sk-test", BaseURL: srv.URL, Timeout: time.Second})
	resp, err := p.Complete(context.Background(), &Request{
		Model:    "claude-sonnet-4-6",
		Messages: []Message{TextMessage("user", "hello")},
		Cache: &CacheOptions{
			Control: &CacheControl{Type: "ephemeral"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.CacheUsage.CreatedTokens != 100 || resp.CacheUsage.ReadTokens != 25 {
		t.Fatalf("cache usage = %+v, want created=100 read=25", resp.CacheUsage)
	}
}

func TestGeminiCompleteIncludesCachedContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-2.5-flash:generateContent" {
			t.Fatalf("path = %s, want gemini generateContent", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["cachedContent"]; got != "cachedContents/demo" {
			t.Fatalf("cachedContent = %#v, want %q", got, "cachedContents/demo")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{{"text": "ok"}},
					},
					"finishReason": "STOP",
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":        10,
				"candidatesTokenCount":    2,
				"cachedContentTokenCount": 50,
			},
		})
	}))
	defer srv.Close()

	p := NewGemini(Config{APIKey: "sk-test", BaseURL: srv.URL, Timeout: time.Second})
	resp, err := p.Complete(context.Background(), &Request{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "hello")},
		Cache: &CacheOptions{
			CachedContent: "cachedContents/demo",
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.CacheUsage.ReadTokens != 50 {
		t.Fatalf("CacheUsage.ReadTokens = %d, want 50", resp.CacheUsage.ReadTokens)
	}
}

func TestGeminiCacheLifecycle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/cachedContents":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if got := body["model"]; got != "models/gemini-2.5-flash" {
				t.Fatalf("create model = %#v, want %q", got, "models/gemini-2.5-flash")
			}
			if got := body["ttl"]; got != "300s" {
				t.Fatalf("create ttl = %#v, want %q", got, "300s")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":        "cachedContents/demo",
				"model":       "models/gemini-2.5-flash",
				"displayName": "demo",
				"createTime":  "2026-04-19T12:00:00Z",
				"updateTime":  "2026-04-19T12:00:00Z",
				"expireTime":  "2026-04-19T12:05:00Z",
				"usageMetadata": map[string]any{
					"totalTokenCount": 2048,
				},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/v1beta/cachedContents/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":        "cachedContents/demo",
				"model":       "models/gemini-2.5-flash",
				"displayName": "demo",
				"createTime":  "2026-04-19T12:00:00Z",
				"updateTime":  "2026-04-19T12:00:00Z",
				"expireTime":  "2026-04-19T12:05:00Z",
				"usageMetadata": map[string]any{
					"totalTokenCount": 2048,
				},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/v1beta/cachedContents":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cachedContents": []map[string]any{
					{
						"name":        "cachedContents/demo",
						"model":       "models/gemini-2.5-flash",
						"displayName": "demo",
						"createTime":  "2026-04-19T12:00:00Z",
						"updateTime":  "2026-04-19T12:00:00Z",
						"expireTime":  "2026-04-19T12:05:00Z",
						"usageMetadata": map[string]any{
							"totalTokenCount": 2048,
						},
					},
				},
				"nextPageToken": "next-token",
			})

		case r.Method == http.MethodPatch && r.URL.Path == "/v1beta/cachedContents/demo":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode update request: %v", err)
			}
			if got := body["ttl"]; got != "600s" {
				t.Fatalf("update ttl = %#v, want %q", got, "600s")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":        "cachedContents/demo",
				"model":       "models/gemini-2.5-flash",
				"displayName": "demo",
				"createTime":  "2026-04-19T12:00:00Z",
				"updateTime":  "2026-04-19T12:01:00Z",
				"expireTime":  "2026-04-19T12:10:00Z",
				"usageMetadata": map[string]any{
					"totalTokenCount": 2048,
				},
			})

		case r.Method == http.MethodDelete && r.URL.Path == "/v1beta/cachedContents/demo":
			w.WriteHeader(http.StatusOK)

		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	p := NewGemini(Config{APIKey: "sk-test", BaseURL: srv.URL, Timeout: time.Second})

	cache, err := p.CreateCache(context.Background(), &CreateCacheRequest{
		Model:       "gemini-2.5-flash",
		DisplayName: "demo",
		Messages:    []Message{TextMessage("user", "hello")},
		TTL:         5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("CreateCache: %v", err)
	}
	if cache.Name != "cachedContents/demo" || cache.Usage.TotalTokenCount != 2048 {
		t.Fatalf("CreateCache = %+v", cache)
	}

	got, err := p.GetCache(context.Background(), "cachedContents/demo")
	if err != nil {
		t.Fatalf("GetCache: %v", err)
	}
	if got.Name != "cachedContents/demo" {
		t.Fatalf("GetCache.Name = %q, want cachedContents/demo", got.Name)
	}

	list, err := p.ListCaches(context.Background(), &ListCachesRequest{PageSize: 10})
	if err != nil {
		t.Fatalf("ListCaches: %v", err)
	}
	if len(list.Items) != 1 || list.NextPageToken != "next-token" {
		t.Fatalf("ListCaches = %+v", list)
	}

	updated, err := p.UpdateCache(context.Background(), &UpdateCacheRequest{
		Name: "cachedContents/demo",
		TTL:  10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("UpdateCache: %v", err)
	}
	if updated.UpdatedAt.IsZero() {
		t.Fatal("UpdateCache.UpdatedAt should be populated")
	}

	if err := p.DeleteCache(context.Background(), "cachedContents/demo"); err != nil {
		t.Fatalf("DeleteCache: %v", err)
	}
}
