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

func TestHunyuanChatSignsTC3(t *testing.T) {
	var action, version, auth, bodyText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action = r.Header.Get("X-TC-Action")
		version = r.Header.Get("X-TC-Version")
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		bodyText = string(body)
		if strings.Contains(auth, "sek") {
			t.Fatal("secret key was sent")
		}
		_, _ = w.Write([]byte(`{"Response":{"Choices":[{"Message":{"Content":"ok"},"FinishReason":"stop"}],"Usage":{"PromptTokens":2,"CompletionTokens":1,"TotalTokens":3}}}`))
	}))
	defer srv.Close()

	p := NewHunyuan(Config{AccessKey: "AKID", Secret: "sek", BaseURL: srv.URL, Region: "ap-guangzhou", Timeout: time.Second})
	resp, err := p.Complete(context.Background(), &Request{
		Model:    "hunyuan-turbo",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" || resp.InputTokens != 2 || resp.OutputTokens != 1 {
		t.Fatalf("response = %#v", resp)
	}
	if action != "ChatCompletions" || version != hunyuanAPIVersion {
		t.Fatalf("action=%s version=%s", action, version)
	}
	if !strings.Contains(auth, "TC3-HMAC-SHA256 Credential=AKID/") || !strings.Contains(auth, "/hunyuan/tc3_request") {
		t.Fatalf("authorization = %s", auth)
	}
	if !strings.Contains(bodyText, `"Role":"user"`) || !strings.Contains(bodyText, `"Content":"hi"`) {
		t.Fatalf("body = %s", bodyText)
	}
}

func TestHunyuanImageAndEmbedding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-TC-Action") {
		case "TextToImage":
			_, _ = w.Write([]byte(`{"Response":{"ResultImage":"https://example.com/a.png"}}`))
		case "GetEmbedding":
			_, _ = w.Write([]byte(`{"Response":{"Data":[{"Embedding":[0.1,0.2]}]}}`))
		default:
			t.Errorf("action = %s", r.Header.Get("X-TC-Action"))
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := NewHunyuan(Config{AccessKey: "AKID", Secret: "sek", BaseURL: srv.URL, Timeout: time.Second})
	image, err := p.GenerateImage(context.Background(), &ImageRequest{Prompt: "a cat"})
	if err != nil {
		t.Fatal(err)
	}
	if image.Images[0].URL != "https://example.com/a.png" {
		t.Fatalf("image = %#v", image)
	}
	embed, err := p.Embed(context.Background(), &EmbedRequest{Input: []string{"hello"}, Model: "hunyuan-embedding"})
	if err != nil {
		t.Fatal(err)
	}
	if len(embed.Embeddings) != 1 || len(embed.Embeddings[0]) != 2 {
		t.Fatalf("embed = %#v", embed.Embeddings)
	}
}

func TestDoubaoVideoAndChatPaths(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/v3/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
		case "/api/v3/contents/generations/tasks":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"text":"a lake"`) {
				t.Errorf("body = %s", body)
			}
			_, _ = w.Write([]byte(`{"id":"cgt-1"}`))
		case "/api/v3/contents/generations/tasks/cgt-1":
			_, _ = w.Write([]byte(`{"id":"cgt-1","status":"succeeded","content":{"video_url":"https://example.com/a.mp4"}}`))
		default:
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewOpenAICompatible("doubao", Config{APIKey: "ark-key", BaseURL: srv.URL + "/api/v3", Timeout: time.Second})
	chat, err := p.Complete(context.Background(), &Request{
		Model:    "doubao-pro",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if chat.Content != "ok" || !strings.HasPrefix(auth, "Bearer ") {
		t.Fatalf("chat = %#v auth=%s", chat, auth)
	}
	op, err := p.SubmitOperation(context.Background(), &OperationRequest{
		Model: "doubao-seedance", Kind: OperationVideo, Prompt: "a lake",
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.ID != "cgt-1" || op.Status != OperationQueued {
		t.Fatalf("submit = %#v", op)
	}
	got, err := p.GetOperation(context.Background(), "cgt-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != OperationSucceeded || got.Result.Videos[0].URL != "https://example.com/a.mp4" {
		t.Fatalf("get = %#v", got)
	}
}

func TestSignTC3ChangesWithBody(t *testing.T) {
	now := time.Unix(1551113065, 0)
	first := signedAuthorization(t, []byte(`{"Limit":1}`), now)
	second := signedAuthorization(t, []byte(`{"Limit":2}`), now)
	if first == second {
		t.Fatal("signature ignored the request body")
	}
	if !strings.Contains(first, "Credential=AKID/2019-02-25/hunyuan/tc3_request") {
		t.Fatalf("authorization = %s", first)
	}
}

func signedAuthorization(t *testing.T, body []byte, now time.Time) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://hunyuan.tencentcloudapi.com/", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	if err := signTC3(req, body, "AKID", "secret", "hunyuan", "ChatCompletions", hunyuanAPIVersion, "ap-guangzhou", now); err != nil {
		t.Fatal(err)
	}
	return req.Header.Get("Authorization")
}
