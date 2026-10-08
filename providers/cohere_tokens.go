package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// CountTokens calls Cohere's official /v1/tokenize endpoint.
// That endpoint accepts text, so the message text is joined in order.
// It does not apply Cohere's chat template.
func (p *CohereProvider) CountTokens(ctx context.Context, req *Request) (*TokenCount, error) {
	if req == nil {
		return nil, fmt.Errorf("request required")
	}
	text := cohereTokenText(req.Messages)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("tokenize text required")
	}
	body := map[string]any{"model": req.Model, "text": text}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v1/tokenize")
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	p.setHeaders(httpReq, creds)
	var payload struct {
		Tokens []int `json:"tokens"`
	}
	if err := doProviderJSONRequest(ctx, p.client, httpReq, p.Name(), &payload); err != nil {
		return nil, err
	}
	return &TokenCount{
		InputTokens: len(payload.Tokens),
		Model:       req.Model,
		Provider:    p.Name(),
	}, nil
}

func cohereTokenText(messages []Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if text := strings.TrimSpace(ExtractText(message.Content)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
