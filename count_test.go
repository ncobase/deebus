package deebus

import (
	"context"
	"testing"

	"github.com/ncobase/deebus/providers"
)

type countMock struct {
	*agentMockProvider
	model string
}

func (m *countMock) CountTokens(_ context.Context, req *providers.Request) (*providers.TokenCount, error) {
	m.model = req.Model
	return &providers.TokenCount{InputTokens: 7, Provider: "images"}, nil
}

func TestCountTokensSkipsProviderWithoutOfficialEndpoint(t *testing.T) {
	client, err := NewClient(Config{
		Primary:         "plain/chat",
		Fallbacks:       []string{"counted/claude"},
		RetryConfigured: true,
		Retry:           0,
		Providers: map[string]ProviderConfig{
			"plain":   {Type: "ollama", BaseURL: "http://127.0.0.1:9"},
			"counted": {Type: "ollama", BaseURL: "http://127.0.0.1:9"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	counter := &countMock{agentMockProvider: &agentMockProvider{}}
	client.providers["counted"] = counter

	got, err := client.CountTokens(context.Background(), &Request{
		Messages: []Message{TextMessage("user", "hello")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.InputTokens != 7 || counter.model != "claude" {
		t.Fatalf("count = %#v model=%s", got, counter.model)
	}
}
