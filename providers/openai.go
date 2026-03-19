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

// OpenAIProvider implements Provider for the OpenAI Chat Completions API and
// any compatible endpoint (Azure OpenAI, DeepSeek, Mistral, etc.).
type OpenAIProvider struct {
	cfg    Config
	client *http.Client
}

// NewOpenAI creates a new OpenAI-compatible provider.
func NewOpenAI(cfg Config) *OpenAIProvider {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &OpenAIProvider{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
}

func (p *OpenAIProvider) Name() string { return "openai" }

func (p *OpenAIProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": ConvertToOpenAIFormat(req.Messages),
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
		if req.ToolChoice != "" {
			body["tool_choice"] = req.ToolChoice
		}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/v1/chat/completions", bytes.NewReader(data))
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
		Choices []struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("empty choices in response")
	}

	return &Response{
		Content:      result.Choices[0].Message.Content,
		Model:        result.Model,
		Provider:     p.Name(),
		TokensUsed:   result.Usage.TotalTokens,
		FinishReason: result.Choices[0].FinishReason,
		ToolCalls:    result.Choices[0].Message.ToolCalls,
		CreatedAt:    time.Now(),
	}, nil
}

func (p *OpenAIProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": ConvertToOpenAIFormat(req.Messages),
		"stream":   true,
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
		p.cfg.BaseURL+"/v1/chat/completions", bytes.NewReader(data))
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
			if payload == "[DONE]" {
				select {
				case ch <- &StreamChunk{Done: true}:
				case <-ctx.Done():
				}
				return
			}

			var chunk struct {
				Choices []struct {
					Delta struct {
						Content   string     `json:"content"`
						ToolCalls []ToolCall `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				select {
				case ch <- &StreamChunk{Error: fmt.Errorf("parse chunk: %w", err)}:
				case <-ctx.Done():
				}
				return
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			sc := &StreamChunk{
				Content:      chunk.Choices[0].Delta.Content,
				Done:         chunk.Choices[0].FinishReason != "",
				FinishReason: chunk.Choices[0].FinishReason,
			}
			if len(chunk.Choices[0].Delta.ToolCalls) > 0 {
				tc := chunk.Choices[0].Delta.ToolCalls[0]
				sc.ToolCall = &tc
			}

			select {
			case ch <- sc:
			case <-ctx.Done():
				return
			}
			if sc.Done {
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

func (p *OpenAIProvider) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	body := map[string]any{
		"model": req.Model,
		"input": req.Input,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/v1/embeddings", bytes.NewReader(data))
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
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	embeddings := make([][]float64, len(result.Data))
	for i, d := range result.Data {
		embeddings[i] = d.Embedding
	}

	return &EmbedResponse{
		Embeddings: embeddings,
		Model:      result.Model,
		TokensUsed: result.Usage.TotalTokens,
	}, nil
}

func (p *OpenAIProvider) Health(_ context.Context) error { return nil }

func (p *OpenAIProvider) setHeaders(r *http.Request) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
}
