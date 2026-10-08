package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// SubmitBatch calls POST /v1/messages/batches.
func (p *AnthropicProvider) SubmitBatch(ctx context.Context, req *BatchRequest) (*Batch, error) {
	if req == nil || len(req.Items) == 0 {
		return nil, fmt.Errorf("batch items required")
	}
	requests := make([]map[string]any, 0, len(req.Items))
	for i, item := range req.Items {
		if item.Request == nil {
			return nil, fmt.Errorf("batch item %d request required", i)
		}
		customID := item.CustomID
		if customID == "" {
			customID = fmt.Sprintf("item-%d", i)
		}
		requests = append(requests, map[string]any{
			"custom_id": customID,
			"params":    anthropicBody(item.Request, false),
		})
	}
	data, err := json.Marshal(map[string]any{"requests": requests})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return p.anthropicBatch(ctx, http.MethodPost, "/v1/messages/batches", data)
}

// GetBatch calls GET /v1/messages/batches/{id}.
func (p *AnthropicProvider) GetBatch(ctx context.Context, id string) (*Batch, error) {
	if err := validOperationID(id); err != nil {
		return nil, err
	}
	return p.anthropicBatch(ctx, http.MethodGet, "/v1/messages/batches/"+url.PathEscape(id), nil)
}

// CancelBatch calls POST /v1/messages/batches/{id}/cancel.
func (p *AnthropicProvider) CancelBatch(ctx context.Context, id string) error {
	if err := validOperationID(id); err != nil {
		return err
	}
	_, err := p.anthropicBatch(ctx, http.MethodPost, "/v1/messages/batches/"+url.PathEscape(id)+"/cancel", []byte("{}"))
	return err
}

// ReadBatch calls GET /v1/messages/batches/{id}/results.
func (p *AnthropicProvider) ReadBatch(ctx context.Context, id string) ([]BatchResult, error) {
	if err := validOperationID(id); err != nil {
		return nil, err
	}
	raw, err := p.anthropicBytes(ctx, http.MethodGet, "/v1/messages/batches/"+url.PathEscape(id)+"/results", nil)
	if err != nil {
		return nil, err
	}
	return parseAnthropicBatchResults(raw)
}

func (p *AnthropicProvider) anthropicBatch(ctx context.Context, method, path string, body []byte) (*Batch, error) {
	raw, err := p.anthropicBytes(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	var payload struct {
		ID               string `json:"id"`
		ProcessingStatus string `json:"processing_status"`
		RequestCounts    struct {
			Succeeded int `json:"succeeded"`
			Errored   int `json:"errored"`
		} `json:"request_counts"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &Batch{
		ID:        payload.ID,
		Provider:  p.Name(),
		Status:    mapAnthropicBatchStatus(payload.ProcessingStatus),
		Succeeded: payload.RequestCounts.Succeeded,
		Errored:   payload.RequestCounts.Errored,
	}, nil
}

func (p *AnthropicProvider) anthropicBytes(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, path)
	if err != nil {
		return nil, err
	}
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if method == http.MethodPost {
		httpReq.Header.Set("Content-Type", "application/json")
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
	raw, err := readResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp.StatusCode, raw, resp.Header, p.Name())
	}
	return raw, nil
}

func mapAnthropicBatchStatus(status string) OperationStatus {
	switch strings.ToLower(status) {
	case "in_progress", "canceling":
		return OperationRunning
	case "ended":
		return OperationSucceeded
	default:
		return OperationQueued
	}
}

func parseAnthropicBatchResults(raw []byte) ([]BatchResult, error) {
	var results []BatchResult
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), maxBufferedBody)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row struct {
			CustomID string `json:"custom_id"`
			Result   struct {
				Type    string `json:"type"`
				Message struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("decode batch result: %w", err)
		}
		item := BatchResult{CustomID: row.CustomID}
		if row.Result.Error != nil {
			item.Error = row.Result.Error.Message
		}
		for _, block := range row.Result.Message.Content {
			item.Content += block.Text
		}
		results = append(results, item)
	}
	return results, scanner.Err()
}
