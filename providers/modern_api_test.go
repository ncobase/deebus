package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAIResponsesCompleteMapsModernFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %s, want /v1/responses", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["max_output_tokens"]; got != float64(128) {
			t.Fatalf("max_output_tokens = %#v", got)
		}
		if _, ok := body["reasoning"].(map[string]any); !ok {
			t.Fatalf("reasoning missing: %#v", body["reasoning"])
		}
		text, ok := body["text"].(map[string]any)
		if !ok {
			t.Fatalf("text config missing: %#v", body["text"])
		}
		format, ok := text["format"].(map[string]any)
		if !ok || format["type"] != "json_schema" {
			t.Fatalf("format = %#v", text["format"])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"model":  "gpt-5.4",
			"output": []map[string]any{
				{"type": "reasoning", "summary": []map[string]any{{"text": "checked"}}},
				{"type": "message", "content": []map[string]any{{"type": "output_text", "text": "{\"ok\":true}"}}},
			},
			"usage": map[string]any{
				"input_tokens":          10,
				"output_tokens":         5,
				"total_tokens":          15,
				"output_tokens_details": map[string]any{"reasoning_tokens": 2},
			},
		})
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "sk-test", BaseURL: srv.URL, APIMode: "responses", Timeout: time.Second})
	resp, err := p.Complete(context.Background(), &Request{
		Model:           "gpt-5.4",
		Messages:        []Message{TextMessage("user", "return json")},
		MaxOutputTokens: 128,
		Reasoning:       &ReasoningConfig{Effort: "medium"},
		ResponseFormat: &ResponseFormat{
			Type:   "json_schema",
			Name:   "result",
			Schema: map[string]any{"type": "object"},
			Strict: true,
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != `{"ok":true}` || resp.Reasoning != "checked" || resp.ReasoningTokens != 2 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestGeminiCompleteMapsStructuredThinkingConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gc, ok := body["generationConfig"].(map[string]any)
		if !ok {
			t.Fatalf("generationConfig missing: %#v", body)
		}
		if gc["responseMimeType"] != "application/json" {
			t.Fatalf("responseMimeType = %#v", gc["responseMimeType"])
		}
		if _, ok := gc["responseSchema"].(map[string]any); !ok {
			t.Fatalf("responseSchema missing: %#v", gc)
		}
		thinking, ok := gc["thinkingConfig"].(map[string]any)
		if !ok || thinking["includeThoughts"] != true {
			t.Fatalf("thinkingConfig = %#v", gc["thinkingConfig"])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates":    []map[string]any{{"content": map[string]any{"parts": []map[string]any{{"text": "ok"}}}, "finishReason": "STOP"}},
			"usageMetadata": map[string]any{"promptTokenCount": 1, "candidatesTokenCount": 1},
		})
	}))
	defer srv.Close()

	p := NewGemini(Config{APIKey: "key", BaseURL: srv.URL, Timeout: time.Second})
	_, err := p.Complete(context.Background(), &Request{
		Model:          "gemini-3-flash-preview",
		Messages:       []Message{TextMessage("user", "hello")},
		ResponseFormat: &ResponseFormat{Type: "json_schema", Schema: map[string]any{"type": "object"}},
		Reasoning:      &ReasoningConfig{IncludeThoughts: true, BudgetTokens: 64},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestOllamaCompleteMapsStructuredFormatAndOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if _, ok := body["format"].(map[string]any); !ok {
			t.Fatalf("format = %#v", body["format"])
		}
		options, ok := body["options"].(map[string]any)
		if !ok || options["num_predict"] != float64(32) || options["temperature"] != 0.2 {
			t.Fatalf("options = %#v", body["options"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": "{}"}, "model": "llama", "done_reason": "stop"})
	}))
	defer srv.Close()

	p := NewOllama(Config{BaseURL: srv.URL, Timeout: time.Second})
	_, err := p.Complete(context.Background(), &Request{
		Model:           "llama",
		Messages:        []Message{TextMessage("user", "json")},
		MaxOutputTokens: 32,
		Temperature:     0.2,
		ResponseFormat:  &ResponseFormat{Type: "json_schema", Schema: map[string]any{"type": "object"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}
