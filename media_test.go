package deebus

import (
	"context"
	"strings"
	"testing"

	"github.com/ncobase/deebus/providers"
)

type imageMock struct {
	*agentMockProvider
	model string
	calls int
}

func (m *imageMock) GenerateImage(_ context.Context, req *providers.ImageRequest) (*providers.ImageResponse, error) {
	m.calls++
	m.model = req.Model
	return &providers.ImageResponse{Images: []providers.Image{{B64JSON: "img"}}}, nil
}

func TestGenerateImageFallsBackToSupportingProvider(t *testing.T) {
	cfg := Config{
		Primary:   "plain/chat",
		Fallbacks: []string{"images/painter"},
		Providers: map[string]ProviderConfig{
			"plain":  {Type: "ollama", BaseURL: "http://127.0.0.1:9"},
			"images": {Type: "ollama", BaseURL: "http://127.0.0.1:9"},
		},
		RetryConfigured: true,
		Retry:           0,
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	imageProvider := &imageMock{agentMockProvider: &agentMockProvider{}}
	client.providers["images"] = imageProvider

	resp, err := client.GenerateImage(context.Background(), &ImageRequest{Prompt: "a lighthouse"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Images[0].B64JSON != "img" {
		t.Fatalf("image = %#v", resp.Images)
	}
	if imageProvider.model != "painter" {
		t.Fatalf("model = %s", imageProvider.model)
	}
	requests, _, _, success, failed := client.Stats.Get()
	if requests != 1 || success != 1 || failed != 0 {
		t.Fatalf("stats requests=%d success=%d failed=%d", requests, success, failed)
	}
}

func TestGenerateImageExplicitRouteSkipsChain(t *testing.T) {
	cfg := Config{
		Primary: "plain/chat",
		Providers: map[string]ProviderConfig{
			"plain":  {Type: "ollama", BaseURL: "http://127.0.0.1:9"},
			"images": {Type: "ollama", BaseURL: "http://127.0.0.1:9"},
		},
		RetryConfigured: true,
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	imageProvider := &imageMock{agentMockProvider: &agentMockProvider{}}
	client.providers["images"] = imageProvider

	if _, err := client.GenerateImage(context.Background(), &ImageRequest{
		Model:  "images/painter",
		Prompt: "a lighthouse",
	}); err != nil {
		t.Fatal(err)
	}
	if imageProvider.calls != 1 || imageProvider.model != "painter" {
		t.Fatalf("calls=%d model=%s", imageProvider.calls, imageProvider.model)
	}
}

func TestGenerateImageRejectsOversizedPrompt(t *testing.T) {
	cfg := Config{
		Primary: "images/painter",
		Providers: map[string]ProviderConfig{
			"images": {Type: "ollama", BaseURL: "http://127.0.0.1:9"},
		},
		RequestPolicy: RequestPolicy{Limits: RequestLimits{MaxTextBytes: 4}},
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GenerateImage(context.Background(), &ImageRequest{Prompt: "too long"})
	if err == nil || !strings.Contains(err.Error(), "maxTextBytes") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewClientAcceptsCompatibleProvider(t *testing.T) {
	client, err := NewClient(Config{
		Primary: "groq/llama-3.3-70b-versatile",
		Providers: map[string]ProviderConfig{
			"groq": {
				Type:    "groq",
				APIKey:  "key",
				BaseURL: "https://api.groq.com/openai/v1",
				APIMode: "chat_completions",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := client.providers["groq"]; !ok {
		t.Fatal("groq provider was not built")
	}
}
