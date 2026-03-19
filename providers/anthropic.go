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

const anthropicVersion = "2023-06-01"

// AnthropicProvider implements Provider for the Anthropic Messages API.
type AnthropicProvider struct {
	cfg    Config
	client *http.Client
}

// NewAnthropic creates a new Anthropic provider.
func NewAnthropic(cfg Config) *AnthropicProvider {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &AnthropicProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

func (p *AnthropicProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	body := map[string]any{
		"model":      req.Model,
		"messages":   ConvertToAnthropicFormat(req.Messages),
		"max_tokens": 1024,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/v1/messages", bytes.NewReader(data))
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
		Content []struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			ID    string `json:"id"`
			Name  string `json:"name"`
			Input any    `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	content := ""
	var toolCalls []ToolCall
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			content = block.Text
		case "tool_use":
			args, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: block.Name, Arguments: string(args)},
			})
		}
	}

	return &Response{
		Content:      content,
		Model:        result.Model,
		Provider:     p.Name(),
		TokensUsed:   result.Usage.InputTokens + result.Usage.OutputTokens,
		FinishReason: result.StopReason,
		ToolCalls:    toolCalls,
		CreatedAt:    time.Now(),
	}, nil
}

func (p *AnthropicProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	body := map[string]any{
		"model":      req.Model,
		"messages":   ConvertToAnthropicFormat(req.Messages),
		"max_tokens": 1024,
		"stream":     true,
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
		p.cfg.BaseURL+"/v1/messages", bytes.NewReader(data))
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
					StopReason   string `json:"stop_reason"`
				} `json:"delta"`
			}

			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}

			switch event.Type {
			case "content_block_delta":
				if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
					select {
					case ch <- &StreamChunk{Content: event.Delta.Text}:
					case <-ctx.Done():
						return
					}
				}
			case "message_delta":
				if event.Delta.StopReason != "" {
					select {
					case ch <- &StreamChunk{Done: true, FinishReason: event.Delta.StopReason}:
					case <-ctx.Done():
					}
					return
				}
			case "message_stop":
				select {
				case ch <- &StreamChunk{Done: true, FinishReason: "stop"}:
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

// Embed is not supported by Anthropic.
func (p *AnthropicProvider) Embed(_ context.Context, _ *EmbedRequest) (*EmbedResponse, error) {
	return nil, &ProviderError{
		Type:      ErrTypeInvalidReq,
		Provider:  p.Name(),
		Message:   "anthropic does not support embeddings",
		Retryable: false,
		Fallback:  true,
	}
}

func (p *AnthropicProvider) Health(_ context.Context) error { return nil }

func (p *AnthropicProvider) setHeaders(r *http.Request) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", p.cfg.APIKey)
	r.Header.Set("anthropic-version", anthropicVersion)
}
