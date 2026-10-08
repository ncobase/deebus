package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaultBaseURLs(t *testing.T) {
	if got := DefaultBaseURL("qwen"); got != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("qwen = %s", got)
	}
	if got := DefaultBaseURL("qwen-intl"); got != "https://dashscope-intl.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("qwen-intl = %s", got)
	}
	if got := ResolveBaseURL("openai", " https://example.com "); got != "https://example.com" {
		t.Fatalf("override = %s", got)
	}
	if DefaultBaseURL("azure") != "" {
		t.Fatal("azure has no single official base URL")
	}
	if DefaultBaseURL("perplexity") != "https://api.perplexity.ai" {
		t.Fatalf("perplexity = %s", DefaultBaseURL("perplexity"))
	}
	p := NewOpenAICompatible("perplexity", Config{})
	got, err := p.openAIEndpoint("sonar", "/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.perplexity.ai/chat/completions" {
		t.Fatalf("perplexity chat = %s", got)
	}
}

func TestOpenAICountTokensDoesNotCallNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("openai has no token counting endpoint")
	}))
	defer srv.Close()
	p := NewOpenAI(Config{APIKey: "sk-test", BaseURL: srv.URL, Timeout: time.Second})
	_, err := p.CountTokens(context.Background(), &Request{Model: "gpt-4o", Messages: []Message{TextMessage("user", "hi")}})
	if err == nil || !IsFallback(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestAnthropicCountTokensUsesOfficialRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") == "" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("headers = %v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "max_tokens") {
			t.Errorf("count_tokens must not send max_tokens: %s", body)
		}
		if !strings.Contains(string(body), `"model":"claude"`) {
			t.Errorf("body = %s", body)
		}
		_, _ = w.Write([]byte(`{"input_tokens":42}`))
	}))
	defer srv.Close()

	p := NewAnthropic(Config{APIKey: "sk-ant", BaseURL: srv.URL, Timeout: time.Second})
	got, err := p.CountTokens(context.Background(), &Request{
		Model:    "claude",
		Messages: []Message{TextMessage("user", "hello")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.InputTokens != 42 || got.Provider != "anthropic" {
		t.Fatalf("count = %#v", got)
	}
}

func TestGeminiCountTokensSendsInputBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":countTokens") {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		if !strings.Contains(text, "systemInstruction") || strings.Contains(text, "maxOutputTokens") {
			t.Errorf("body = %s", text)
		}
		_, _ = w.Write([]byte(`{"totalTokens":9,"cachedContentTokenCount":2}`))
	}))
	defer srv.Close()

	p := NewGemini(Config{APIKey: "key", BaseURL: srv.URL, Timeout: time.Second})
	got, err := p.CountTokens(context.Background(), &Request{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			TextMessage("system", "rules"),
			TextMessage("user", "hello"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.InputTokens != 9 || got.CacheReadTokens != 2 {
		t.Fatalf("count = %#v", got)
	}
}

func TestCohereTokenizeUsesOfficialTextEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tokenize" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Text != "hello\nworld" {
			t.Errorf("text = %q", payload.Text)
		}
		_, _ = w.Write([]byte(`{"tokens":[1,2,3]}`))
	}))
	defer srv.Close()

	p := NewCohere(Config{APIKey: "key", BaseURL: srv.URL, Timeout: time.Second})
	got, err := p.CountTokens(context.Background(), &Request{
		Model: "command-r",
		Messages: []Message{
			TextMessage("user", "hello"),
			TextMessage("assistant", "world"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.InputTokens != 3 || got.Provider != "cohere" {
		t.Fatalf("count = %#v", got)
	}
}

func TestQwenUsesCompatibleProvider(t *testing.T) {
	p, err := New("qwen", Config{APIKey: "key", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "qwen" {
		t.Fatalf("name = %s", p.Name())
	}
	if _, err := p.(TokenCounter).CountTokens(context.Background(), &Request{Model: "qwen-plus"}); err == nil || !IsFallback(err) {
		t.Fatalf("qwen count err = %v", err)
	}
}
