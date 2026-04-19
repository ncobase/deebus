package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type stubCredentialProvider struct {
	creds Credentials
	err   error
	calls int
}

func (p *stubCredentialProvider) Credentials(context.Context) (Credentials, error) {
	p.calls++
	return p.creds, p.err
}

func TestOpenAICompleteUsesBearerTokenAndProjectHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer oauth-token")
		}
		if got := r.Header.Get("OpenAI-Organization"); got != "org_123" {
			t.Fatalf("OpenAI-Organization = %q, want %q", got, "org_123")
		}
		if got := r.Header.Get("OpenAI-Project"); got != "proj_123" {
			t.Fatalf("OpenAI-Project = %q, want %q", got, "proj_123")
		}
		if got := r.Header.Get("X-Test"); got != "ok" {
			t.Fatalf("X-Test = %q, want %q", got, "ok")
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
				"prompt_tokens":     5,
				"completion_tokens": 2,
				"total_tokens":      7,
			},
			"model": "gpt-5.4",
		})
	}))
	defer srv.Close()

	p := NewOpenAI(Config{
		APIKey:       "sk-static",
		BearerToken:  "oauth-token",
		BaseURL:      srv.URL,
		Timeout:      time.Second,
		Headers:      map[string]string{"X-Test": "ok"},
		Organization: "org_123",
		Project:      "proj_123",
	})
	if _, err := p.Complete(context.Background(), &Request{
		Model:    "gpt-5.4",
		Messages: []Message{TextMessage("user", "hello")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestOpenAICompleteUsesCredentialProvider(t *testing.T) {
	cp := &stubCredentialProvider{
		creds: Credentials{
			BearerToken:  "dynamic-token",
			Organization: "org_dynamic",
			Project:      "proj_dynamic",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer dynamic-token" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer dynamic-token")
		}
		if got := r.Header.Get("OpenAI-Organization"); got != "org_dynamic" {
			t.Fatalf("OpenAI-Organization = %q, want %q", got, "org_dynamic")
		}
		if got := r.Header.Get("OpenAI-Project"); got != "proj_dynamic" {
			t.Fatalf("OpenAI-Project = %q, want %q", got, "proj_dynamic")
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
				"prompt_tokens":     5,
				"completion_tokens": 2,
				"total_tokens":      7,
			},
			"model": "gpt-5.4",
		})
	}))
	defer srv.Close()

	p := NewOpenAI(Config{
		BaseURL:            srv.URL,
		Timeout:            time.Second,
		CredentialProvider: cp,
	})
	if _, err := p.Complete(context.Background(), &Request{
		Model:    "gpt-5.4",
		Messages: []Message{TextMessage("user", "hello")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if cp.calls != 1 {
		t.Fatalf("CredentialProvider calls = %d, want 1", cp.calls)
	}
}

func TestAnthropicCompleteUsesCredentialProvider(t *testing.T) {
	cp := &stubCredentialProvider{
		creds: Credentials{
			APIKey:      "anthropic-key",
			BearerToken: "gateway-token",
			Headers:     map[string]string{"X-Test": "ok"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "anthropic-key" {
			t.Fatalf("x-api-key = %q, want %q", got, "anthropic-key")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer gateway-token" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer gateway-token")
		}
		if got := r.Header.Get("X-Test"); got != "ok" {
			t.Fatalf("X-Test = %q, want %q", got, "ok")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "ok"},
			},
			"usage": map[string]any{
				"input_tokens":  5,
				"output_tokens": 2,
			},
			"model":       "claude-sonnet-4-6",
			"stop_reason": "end_turn",
		})
	}))
	defer srv.Close()

	p := NewAnthropic(Config{
		BaseURL:            srv.URL,
		Timeout:            time.Second,
		CredentialProvider: cp,
	})
	if _, err := p.Complete(context.Background(), &Request{
		Model:    "claude-sonnet-4-6",
		Messages: []Message{TextMessage("user", "hello")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if cp.calls != 1 {
		t.Fatalf("CredentialProvider calls = %d, want 1", cp.calls)
	}
}

func TestGeminiCompleteUsesBearerTokenAndUserProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer oauth-token")
		}
		if got := r.Header.Get("x-goog-user-project"); got != "project-123" {
			t.Fatalf("x-goog-user-project = %q, want %q", got, "project-123")
		}
		if got := r.Header.Get("X-Test"); got != "ok" {
			t.Fatalf("X-Test = %q, want %q", got, "ok")
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
				"promptTokenCount":     5,
				"candidatesTokenCount": 2,
			},
		})
	}))
	defer srv.Close()

	p := NewGemini(Config{
		BaseURL:     srv.URL,
		Timeout:     time.Second,
		BearerToken: "oauth-token",
		UserProject: "project-123",
		Headers:     map[string]string{"X-Test": "ok"},
	})
	if _, err := p.Complete(context.Background(), &Request{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "hello")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestGeminiURLUsesAPIKeyOnlyWhenNoBearerToken(t *testing.T) {
	p := NewGemini(Config{BaseURL: "https://generativelanguage.googleapis.com"})

	url := p.geminiURL(Credentials{APIKey: "api-key"}, "/v1beta/models/gemini-2.5-flash:generateContent", "alt=sse")
	if !strings.Contains(url, "key=api-key") {
		t.Fatalf("geminiURL missing API key query: %q", url)
	}
	if !strings.Contains(url, "alt=sse") {
		t.Fatalf("geminiURL missing extra query: %q", url)
	}

	url = p.geminiURL(Credentials{APIKey: "api-key", BearerToken: "oauth-token"}, "/v1beta/models/gemini-2.5-flash:generateContent", "alt=sse")
	if strings.Contains(url, "key=api-key") {
		t.Fatalf("geminiURL should omit API key when bearer token is present: %q", url)
	}
	if !strings.HasSuffix(url, "?alt=sse") {
		t.Fatalf("geminiURL = %q, want suffix ?alt=sse", url)
	}
}
