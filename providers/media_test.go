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

func TestAzureResponsesUsesV1Route(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/responses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("responses route should not send api-version, query = %s", r.URL.RawQuery)
		}
		if r.Header.Get("api-key") != "azure-key" {
			t.Errorf("api-key = %s", r.Header.Get("api-key"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"gpt-4o"`) {
			t.Errorf("body = %s", body)
		}
		_, _ = w.Write([]byte(`{"id":"resp_1","model":"gpt-4o","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer srv.Close()

	p := NewAzure(Config{APIKey: "azure-key", BaseURL: srv.URL, APIMode: "responses", Timeout: time.Second})
	resp, err := p.Complete(context.Background(), &Request{
		Model:    "gpt-4o",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" || resp.Provider != "azure" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestOpenAIEditImageUsesCurrentJSONContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		if !strings.Contains(text, `"images"`) || !strings.Contains(text, "data:image/png;base64,aW1n") {
			t.Errorf("body = %s", text)
		}
		if !strings.Contains(text, `"file_id":"file_mask"`) {
			t.Errorf("mask missing: %s", text)
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"ZWRpdA=="}]}`))
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "sk-test", BaseURL: srv.URL, Timeout: time.Second})
	resp, err := p.EditImage(context.Background(), &ImageEditRequest{
		Model:  "gpt-image-1",
		Prompt: "make the sky blue",
		Images: []ImageInput{{Data: []byte("img"), MediaType: "image/png"}},
		Mask:   &ImageInput{FileID: "file_mask"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Images[0].B64JSON != "ZWRpdA==" {
		t.Fatalf("image = %#v", resp.Images)
	}
}

func TestGeminiSpeechAndTranscription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		if strings.Contains(r.URL.Path, "gemini-tts") {
			if !strings.Contains(text, `"voice":"Kore"`) || !strings.Contains(text, "AUDIO_WAV") {
				t.Errorf("speech body = %s", text)
			}
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"YQ=="}}]}}]}`))
			return
		}
		if !strings.Contains(text, "inlineData") || !strings.Contains(text, "Transcribe this audio") {
			t.Errorf("transcribe body = %s", text)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`))
	}))
	defer srv.Close()

	p := NewGemini(Config{APIKey: "key", BaseURL: srv.URL, Timeout: time.Second})
	speech, err := p.SynthesizeSpeech(context.Background(), &SpeechRequest{Model: "gemini-tts", Input: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if string(speech.Audio) != "a" || speech.MediaType != "audio/wav" {
		t.Fatalf("speech = %#v", speech)
	}
	if _, err := p.SynthesizeSpeech(context.Background(), &SpeechRequest{Model: "gemini-tts", Input: "hello", Format: "mp3"}); err == nil || !IsFallback(err) {
		t.Fatalf("mp3 should fall back, err = %v", err)
	}
	transcript, err := p.Transcribe(context.Background(), &TranscribeRequest{
		Model:     "gemini-2.5-flash",
		Data:      []byte("audio"),
		MediaType: "audio/mpeg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Text != "hello" {
		t.Fatalf("text = %q", transcript.Text)
	}
}

func TestGeminiEditImageSendsInlineSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "inlineData") || !strings.Contains(string(body), "blue sky") {
			t.Errorf("body = %s", body)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"eQ=="}}]}}]}`))
	}))
	defer srv.Close()

	p := NewGemini(Config{APIKey: "key", BaseURL: srv.URL, Timeout: time.Second})
	resp, err := p.EditImage(context.Background(), &ImageEditRequest{
		Model:  "gemini-image",
		Prompt: "blue sky",
		Images: []ImageInput{{Data: []byte{1, 2, 3}, MediaType: "image/png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Images[0].B64JSON != "eQ==" {
		t.Fatalf("image = %#v", resp.Images)
	}
}

func TestAzureResponsesBaseURLIsNotDoubled(t *testing.T) {
	p := NewAzure(Config{BaseURL: "https://example.openai.azure.com/openai/v1"})
	got, err := p.openAIEndpoint("gpt-4o", "/v1/responses")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.openai.azure.com/openai/v1/responses" {
		t.Fatalf("endpoint = %s", got)
	}
}

func TestGeminiMultiSpeakerSpeech(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		if !strings.Contains(text, `"speaker":"Joe"`) || !strings.Contains(text, `"voiceName":"Puck"`) {
			t.Errorf("body = %s", text)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"YQ=="}}]}}]}`))
	}))
	defer srv.Close()

	p := NewGemini(Config{APIKey: "key", BaseURL: srv.URL, Timeout: time.Second})
	if _, err := p.SynthesizeSpeech(context.Background(), &SpeechRequest{
		Model: "gemini-tts",
		Speakers: []SpeechTurn{
			{Speaker: "Joe", Voice: "Puck", Text: "Hello"},
			{Speaker: "Jane", Voice: "Kore", Text: "Hi"},
		},
	}); err != nil {
		t.Fatal(err)
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
