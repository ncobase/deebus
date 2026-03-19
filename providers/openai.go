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
	if req.UserID != "" {
		body["user"] = req.UserID
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
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
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
		InputTokens:  result.Usage.PromptTokens,
		OutputTokens: result.Usage.CompletionTokens,
		TokensUsed:   result.Usage.TotalTokens,
		FinishReason: result.Choices[0].FinishReason,
		ToolCalls:    result.Choices[0].Message.ToolCalls,
		CacheUsage:   CacheUsage{ReadTokens: result.Usage.PromptTokensDetails.CachedTokens},
		CreatedAt:    time.Now(),
	}, nil
}

func (p *OpenAIProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	body := map[string]any{
		"model":             req.Model,
		"messages":          ConvertToOpenAIFormat(req.Messages),
		"stream":            true,
		"stream_options":    map[string]any{"include_usage": true},
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
	if req.UserID != "" {
		body["user"] = req.UserID
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

		// Accumulates streaming tool call deltas by index.
		// The first delta for each index carries id, type, and name;
		// subsequent deltas carry only argument fragments.
		type tcAccumulator struct {
			id        string
			typ       string
			name      string
			arguments strings.Builder
		}
		accumulators := map[int]*tcAccumulator{}

		// Pending final chunk: built when finish_reason is set, emitted after
		// the usage chunk arrives (stream_options.include_usage = true).
		var pending *StreamChunk

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "[DONE]" {
				// Emit any pending final chunk that didn't get a usage update.
				if pending != nil {
					select {
					case ch <- pending:
					case <-ctx.Done():
					}
				}
				return
			}

			var chunk struct {
				Choices []struct {
					Delta struct {
						Content   string `json:"content"`
						ToolCalls []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Type     string `json:"type"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				// Usage chunk emitted by stream_options.include_usage=true.
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
					PromptTokensDetails struct {
						CachedTokens int `json:"cached_tokens"`
					} `json:"prompt_tokens_details"`
				} `json:"usage"`
			}

			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				select {
				case ch <- &StreamChunk{Error: fmt.Errorf("parse chunk: %w", err)}:
				case <-ctx.Done():
				}
				return
			}

			// Usage-only chunk (choices is empty): patch and emit the pending final.
			if len(chunk.Choices) == 0 && chunk.Usage != nil && pending != nil {
				pending.InputTokens = chunk.Usage.PromptTokens
				pending.OutputTokens = chunk.Usage.CompletionTokens
				pending.TokensUsed = chunk.Usage.TotalTokens
				pending.CacheUsage = CacheUsage{
					ReadTokens: chunk.Usage.PromptTokensDetails.CachedTokens,
				}
				select {
				case ch <- pending:
				case <-ctx.Done():
				}
				pending = nil
				continue
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]

			// Accumulate tool call fragments.
			for _, tc := range choice.Delta.ToolCalls {
				acc, ok := accumulators[tc.Index]
				if !ok {
					acc = &tcAccumulator{
						id:  tc.ID,
						typ: tc.Type,
					}
					accumulators[tc.Index] = acc
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				acc.arguments.WriteString(tc.Function.Arguments)
			}

			// Emit text content chunks as they arrive.
			if choice.Delta.Content != "" {
				select {
				case ch <- &StreamChunk{Content: choice.Delta.Content}:
				case <-ctx.Done():
					return
				}
			}

			// When finish_reason is set, build the final chunk and hold it
			// until the usage chunk arrives (next iteration).
			if choice.FinishReason != "" {
				pending = &StreamChunk{
					Done:         true,
					FinishReason: choice.FinishReason,
				}
				if len(accumulators) > 0 {
					toolCalls := make([]ToolCall, 0, len(accumulators))
					for idx, acc := range accumulators {
						tc := ToolCall{
							Index: idx,
							ID:    acc.id,
							Type:  acc.typ,
						}
						tc.Function.Name = acc.name
						tc.Function.Arguments = acc.arguments.String()
						toolCalls = append(toolCalls, tc)
					}
					pending.ToolCalls = toolCalls
				}
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
