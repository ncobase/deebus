package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Rerank calls POST /v2/rerank.
func (p *CohereProvider) Rerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error) {
	if req == nil || strings.TrimSpace(req.Query) == "" {
		return nil, fmt.Errorf("rerank query required")
	}
	if len(req.Documents) == 0 {
		return nil, fmt.Errorf("rerank documents required")
	}
	body := map[string]any{
		"model":     req.Model,
		"query":     req.Query,
		"documents": req.Documents,
	}
	if req.TopN > 0 {
		body["top_n"] = req.TopN
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v2/rerank")
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
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp.StatusCode, readErrorBody(resp.Body), resp.Header, p.Name())
	}
	var payload struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
	}
	raw, err := readResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	results := make([]RerankResult, 0, len(payload.Results))
	for _, item := range payload.Results {
		ranked := RerankResult{Index: item.Index, Score: item.RelevanceScore}
		if item.Index >= 0 && item.Index < len(req.Documents) {
			ranked.Document = req.Documents[item.Index]
		}
		results = append(results, ranked)
	}
	return &RerankResponse{Results: results, Model: req.Model, Provider: p.Name()}, nil
}
