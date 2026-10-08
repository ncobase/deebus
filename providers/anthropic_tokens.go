package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// CountTokens calls the official Anthropic /v1/messages/count_tokens endpoint.
// max_tokens is omitted because that endpoint does not accept it.
func (p *AnthropicProvider) CountTokens(ctx context.Context, req *Request) (*TokenCount, error) {
	if req == nil {
		return nil, fmt.Errorf("request required")
	}
	body := anthropicBody(req, false)
	delete(body, "max_tokens")
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v1/messages/count_tokens")
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
		InputTokens int `json:"input_tokens"`
	}
	if err := doProviderJSONRequest(ctx, p.client, httpReq, p.Name(), &payload); err != nil {
		return nil, err
	}
	return &TokenCount{
		InputTokens: payload.InputTokens,
		Model:       req.Model,
		Provider:    p.Name(),
	}, nil
}
