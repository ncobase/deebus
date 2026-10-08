package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIBatchUploadsJSONL(t *testing.T) {
	var uploaded bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/files":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "custom_id") || !strings.Contains(string(body), "gpt-4o") {
				t.Errorf("jsonl missing: %s", body)
			}
			uploaded = true
			_, _ = w.Write([]byte(`{"id":"file_1"}`))
		case "/v1/batches":
			_, _ = w.Write([]byte(`{"id":"batch_1","status":"validating"}`))
		case "/v1/batches/batch_1":
			_, _ = w.Write([]byte(`{"id":"batch_1","status":"completed","output_file_id":"file_out","request_counts":{"completed":1}}`))
		case "/v1/files/file_out/content":
			_, _ = w.Write([]byte("{\"custom_id\":\"a\",\"response\":{\"body\":{\"choices\":[{\"message\":{\"content\":\"ok\"}}]}}}\n"))
		default:
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "sk", BaseURL: srv.URL, Timeout: time.Second})
	batch, err := p.SubmitBatch(context.Background(), &BatchRequest{Items: []BatchItem{{
		CustomID: "a",
		Request:  &Request{Model: "gpt-4o", Messages: []Message{TextMessage("user", "hi")}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !uploaded || batch.ID != "batch_1" || batch.Status != OperationRunning {
		t.Fatalf("batch = %#v uploaded=%v", batch, uploaded)
	}
	got, err := p.GetBatch(context.Background(), "batch_1")
	if err != nil {
		t.Fatal(err)
	}
	results, err := p.ReadBatch(context.Background(), got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Content != "ok" {
		t.Fatalf("results = %#v", results)
	}
}

func TestAnthropicBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/messages/batches" && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"custom_id":"a"`) || strings.Contains(string(body), "max_tokens") == false {
				t.Errorf("body = %s", body)
			}
			_, _ = w.Write([]byte(`{"id":"msgbatch_1","processing_status":"in_progress"}`))
		case r.URL.Path == "/v1/messages/batches/msgbatch_1/results":
			_, _ = w.Write([]byte("{\"custom_id\":\"a\",\"result\":{\"type\":\"succeeded\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}}\n"))
		case r.URL.Path == "/v1/messages/batches/msgbatch_1/cancel":
			_, _ = w.Write([]byte(`{"id":"msgbatch_1","processing_status":"canceling"}`))
		default:
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewAnthropic(Config{APIKey: "sk-ant", BaseURL: srv.URL, Timeout: time.Second})
	batch, err := p.SubmitBatch(context.Background(), &BatchRequest{Items: []BatchItem{{
		CustomID: "a",
		Request:  &Request{Model: "claude", Messages: []Message{TextMessage("user", "hi")}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if batch.ID != "msgbatch_1" || batch.Status != OperationRunning {
		t.Fatalf("batch = %#v", batch)
	}
	results, err := p.ReadBatch(context.Background(), batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Content != "ok" {
		t.Fatalf("results = %#v", results)
	}
	if err := p.CancelBatch(context.Background(), batch.ID); err != nil {
		t.Fatal(err)
	}
}

func TestHunyuanStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-TC-Action") != "ChatCompletions" {
			t.Errorf("action = %s", r.Header.Get("X-TC-Action"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"Stream":true`) {
			t.Errorf("body = %s", body)
		}
		_, _ = w.Write([]byte("data: {\"Choices\":[{\"Delta\":{\"Content\":\"he\"}}]}\n\ndata: {\"Choices\":[{\"Delta\":{\"Content\":\"llo\"},\"FinishReason\":\"stop\"}],\"Usage\":{\"PromptTokens\":1,\"CompletionTokens\":2}}\n\ndata: [DONE]\n"))
	}))
	defer srv.Close()
	p := NewHunyuan(Config{AccessKey: "AKID", Secret: "sek", BaseURL: srv.URL, Timeout: time.Second})
	stream, err := p.Stream(context.Background(), &Request{Model: "hunyuan-turbo", Messages: []Message{TextMessage("user", "hi")}})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for chunk := range stream {
		if chunk.Error != nil {
			t.Fatal(chunk.Error)
		}
		text += chunk.Content
	}
	if text != "hello" {
		t.Fatalf("text = %q", text)
	}
}

func TestKlingReusesJWT(t *testing.T) {
	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"t","task_status":"submitted"}}`))
	}))
	defer srv.Close()
	p := NewKling(Config{AccessKey: "ak", Secret: "sek", BaseURL: srv.URL, Timeout: time.Second})
	req := &OperationRequest{Model: "kling-v1", Kind: OperationVideo, Prompt: "a"}
	if _, err := p.SubmitOperation(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SubmitOperation(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 || tokens[0] == "" || tokens[0] != tokens[1] {
		t.Fatalf("tokens = %#v", tokens)
	}
}
