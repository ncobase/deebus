package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAIEmbedSendsUserIdentifier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("missing authorization header")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["model"] != "text-embedding-test" {
			t.Fatalf("unexpected model: %#v", body["model"])
		}
		if body["user"] != "user-test-123" {
			t.Fatalf("embedding user identifier was not forwarded: %#v", body)
		}
		if body["encoding_format"] != "float" {
			t.Fatalf("embedding encoding_format was not forwarded: %#v", body)
		}
		input, ok := body["input"].([]any)
		if !ok || len(input) != 2 || input[0] != "alpha" || input[1] != "beta" {
			t.Fatalf("unexpected input: %#v", body["input"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "object": "list",
		  "data": [
		    {"object": "embedding", "index": 0, "embedding": [0.1, 0.2]},
		    {"object": "embedding", "index": 1, "embedding": [0.3, 0.4]}
		  ],
		  "model": "text-embedding-test",
		  "usage": {"prompt_tokens": 4, "total_tokens": 4}
		}`))
	}))
	defer server.Close()

	provider := NewOpenAI(Config{APIKey: "sk-test", BaseURL: server.URL, Timeout: time.Second})
	resp, err := provider.Embed(context.Background(), &EmbedRequest{
		Model:          "text-embedding-test",
		Input:          []string{"alpha", "beta"},
		EncodingFormat: "float",
		UserID:         "user-test-123",
	})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if resp.Model != "text-embedding-test" || resp.TokensUsed != 4 || len(resp.Embeddings) != 2 {
		t.Fatalf("unexpected embedding response: %+v", resp)
	}
}
