package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CohereProvider implements Provider for the Cohere Chat API v2.
type CohereProvider struct {
	cfg    Config
	client *http.Client
}

// NewCohere creates a new Cohere provider.
func NewCohere(cfg Config) *CohereProvider {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &CohereProvider{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
}

func (p *CohereProvider) Name() string { return "cohere" }

func (p *CohereProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		content := ExtractText(msg.Content)
		messages = append(messages, map[string]any{"role": msg.Role, "content": content})
	}

	body := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   false,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/v2/chat", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, parseError(resp.StatusCode, b, resp.Header, p.Name())
	}

	var result struct {
		Message struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
		Usage struct {
			Tokens struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"tokens"`
		} `json:"usage"`
		FinishReason string `json:"finish_reason"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	content := ""
	if len(result.Message.Content) > 0 {
		content = result.Message.Content[0].Text
	}

	return &Response{
		Content:      content,
		Model:        req.Model,
		Provider:     p.Name(),
		TokensUsed:   result.Usage.Tokens.InputTokens + result.Usage.Tokens.OutputTokens,
		FinishReason: result.FinishReason,
		CreatedAt:    time.Now(),
	}, nil
}

// Stream implements streaming via Cohere's SSE endpoint.
func (p *CohereProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		content := ExtractText(msg.Content)
		messages = append(messages, map[string]any{"role": msg.Role, "content": content})
	}

	body := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   true,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/v2/chat", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, parseError(resp.StatusCode, b, resp.Header, p.Name())
	}

	ch := make(chan *StreamChunk, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")

			var event struct {
				Type  string `json:"type"`
				Delta struct {
					Type         string `json:"type"`
					Text         string `json:"text"`
					FinishReason string `json:"finish_reason"`
				} `json:"delta"`
			}

			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}

			switch event.Type {
			case "content-delta":
				if event.Delta.Text != "" {
					select {
					case ch <- &StreamChunk{Content: event.Delta.Text}:
					case <-ctx.Done():
						return
					}
				}
			case "message-end":
				select {
				case ch <- &StreamChunk{Done: true, FinishReason: event.Delta.FinishReason}:
				case <-ctx.Done():
				}
				return
			}
		}

		if err := scanner.Err(); err != nil {
			select {
			case ch <- &StreamChunk{Error: fmt.Errorf("stream read: %w", err)}:
			case <-ctx.Done():
			}
		}
	}()

	return ch, nil
}

func (p *CohereProvider) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	body := map[string]any{
		"model":      req.Model,
		"texts":      req.Input,
		"input_type": "search_document",
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/v1/embed", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, parseError(resp.StatusCode, b, resp.Header, p.Name())
	}

	var result struct {
		Embeddings [][]float64 `json:"embeddings"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &EmbedResponse{
		Embeddings: result.Embeddings,
		Model:      req.Model,
	}, nil
}

func (p *CohereProvider) Health(_ context.Context) error { return nil }

func (p *CohereProvider) setHeaders(r *http.Request) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
}
