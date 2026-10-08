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

func TestOpenAIGenerateImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"prompt":"a red door"`) {
			t.Errorf("body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1n","revised_prompt":"red door"}]}`))
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "sk-test", BaseURL: srv.URL, Timeout: time.Second})
	resp, err := p.GenerateImage(context.Background(), &ImageRequest{Model: "gpt-image-1", Prompt: "a red door"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Images) != 1 || resp.Images[0].B64JSON != "aW1n" || resp.Images[0].RevisedPrompt != "red door" {
		t.Fatalf("images = %#v", resp.Images)
	}
}

func TestAzureImageUsesDeploymentURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/openai/deployments/gpt-image/images/generations") {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("api-version") != "2025-04-01-preview" {
			t.Errorf("api-version = %s", r.URL.Query().Get("api-version"))
		}
		if r.Header.Get("api-key") != "azure-key" {
			t.Errorf("api-key = %s", r.Header.Get("api-key"))
		}
		_, _ = w.Write([]byte(`{"data":[{"url":"https://example.com/cat.png"}]}`))
	}))
	defer srv.Close()

	p := NewAzure(Config{APIKey: "azure-key", BaseURL: srv.URL, APIVersion: "2025-04-01-preview", Timeout: time.Second})
	resp, err := p.GenerateImage(context.Background(), &ImageRequest{Model: "gpt-image", Prompt: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Images[0].URL != "https://example.com/cat.png" {
		t.Fatalf("url = %s", resp.Images[0].URL)
	}
}

func TestOpenAISpeechAndTranscribe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/audio/speech":
			_, _ = w.Write([]byte("AUDIO"))
		case "/v1/audio/transcriptions":
			if !strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
				t.Errorf("content-type = %s", r.Header.Get("Content-Type"))
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "name=\"model\"") || !strings.Contains(string(body), "whisper-1") {
				t.Errorf("multipart missing model: %s", body)
			}
			_, _ = w.Write([]byte(`{"text":"hello world"}`))
		default:
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewOpenAICompatible("groq", Config{APIKey: "sk-test", BaseURL: srv.URL, Timeout: time.Second})
	if p.Name() != "groq" {
		t.Fatalf("name = %s", p.Name())
	}
	speech, err := p.SynthesizeSpeech(context.Background(), &SpeechRequest{Model: "tts-1", Input: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if string(speech.Audio) != "AUDIO" || speech.MediaType != "audio/mpeg" {
		t.Fatalf("speech = %#v", speech)
	}
	transcript, err := p.Transcribe(context.Background(), &TranscribeRequest{
		Model: "whisper-1",
		Data:  []byte("fake-audio"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Text != "hello world" || transcript.Provider != "groq" {
		t.Fatalf("transcript = %#v", transcript)
	}
}

func TestGeminiGenerateImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":generateContent") {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "responseModalities") {
			t.Errorf("body = %s", body)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"a cat"},{"inlineData":{"mimeType":"image/png","data":"e30="}}]}}]}`))
	}))
	defer srv.Close()

	p := NewGemini(Config{APIKey: "key", BaseURL: srv.URL, Timeout: time.Second})
	resp, err := p.GenerateImage(context.Background(), &ImageRequest{Model: "gemini-2.5-flash-image", Prompt: "cat", Size: "1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Images) != 1 || resp.Images[0].B64JSON != "e30=" || resp.Images[0].MediaType != "image/png" {
		t.Fatalf("images = %#v", resp.Images)
	}
}

func TestCohereRerank(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/rerank" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.2}]}`))
	}))
	defer srv.Close()

	p := NewCohere(Config{APIKey: "key", BaseURL: srv.URL, Timeout: time.Second})
	resp, err := p.Rerank(context.Background(), &RerankRequest{
		Model:     "rerank-v3.5",
		Query:     "cats",
		Documents: []string{"dogs", "cats"},
		TopN:      2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 || resp.Results[0].Document != "cats" || resp.Results[0].Score != 0.9 {
		t.Fatalf("results = %#v", resp.Results)
	}
}

func TestRegistryBuildsCompatibleProvider(t *testing.T) {
	p, err := New("xai", Config{APIKey: "key", BaseURL: "https://api.x.ai", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "xai" {
		t.Fatalf("name = %s", p.Name())
	}
	if _, err := New("missing", Config{}); err == nil {
		t.Fatal("expected unknown provider error")
	}
}
