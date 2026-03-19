package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OllamaProvider implements Provider for a locally-running Ollama server.
type OllamaProvider struct {
	cfg    Config
	client *http.Client
}

// NewOllama creates a new Ollama provider.
func NewOllama(cfg Config) *OllamaProvider {
	if cfg.Timeout == 0 {
		cfg.Timeout = 120 * time.Second // local models can be slow to load
	}
	return &OllamaProvider{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
}

func (p *OllamaProvider) Name() string { return "ollama" }

func (p *OllamaProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": ConvertToOpenAIFormat(req.Messages),
		"stream":   false,
	}
	if req.Options != nil {
		body["options"] = req.Options
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools // Ollama uses OpenAI-compatible tool format
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, parseError(resp.StatusCode, b, resp.Header, p.Name())
	}

	// Ollama returns arguments as a JSON object; we marshal it back to a string
	// to match the unified ToolCall.Function.Arguments (JSON-string) format.
	var result struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		Model      string `json:"model"`
		DoneReason string `json:"done_reason"`
		EvalCount  int    `json:"eval_count"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	var toolCalls []ToolCall
	for _, otc := range result.Message.ToolCalls {
		args, _ := json.Marshal(otc.Function.Arguments)
		tc := ToolCall{Type: "function"}
		tc.Function.Name = otc.Function.Name
		tc.Function.Arguments = string(args)
		toolCalls = append(toolCalls, tc)
	}

	return &Response{
		Content:      result.Message.Content,
		Model:        result.Model,
		Provider:     p.Name(),
		TokensUsed:   result.EvalCount,
		FinishReason: result.DoneReason,
		ToolCalls:    toolCalls,
		CreatedAt:    time.Now(),
	}, nil
}

// Stream implements streaming via Ollama's NDJSON streaming endpoint.
// Tool calls, if any, are delivered in the final done chunk.
func (p *OllamaProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": ConvertToOpenAIFormat(req.Messages),
		"stream":   true,
	}
	if req.Options != nil {
		body["options"] = req.Options
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

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
			var event struct {
				Message struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Function struct {
							Name      string         `json:"name"`
							Arguments map[string]any `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
				Done       bool   `json:"done"`
				DoneReason string `json:"done_reason"`
				EvalCount  int    `json:"eval_count"`
			}

			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				continue
			}

			if event.Message.Content != "" {
				select {
				case ch <- &StreamChunk{Content: event.Message.Content}:
				case <-ctx.Done():
					return
				}
			}

			if event.Done {
				final := &StreamChunk{
					Done:         true,
					FinishReason: event.DoneReason,
					TokensUsed:   event.EvalCount,
				}
				for _, otc := range event.Message.ToolCalls {
					args, _ := json.Marshal(otc.Function.Arguments)
					tc := ToolCall{Type: "function"}
					tc.Function.Name = otc.Function.Name
					tc.Function.Arguments = string(args)
					final.ToolCalls = append(final.ToolCalls, tc)
				}
				select {
				case ch <- final:
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

func (p *OllamaProvider) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	body := map[string]any{
		"model": req.Model,
		"input": req.Input,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/api/embed", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

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
		Model      string      `json:"model"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &EmbedResponse{
		Embeddings: result.Embeddings,
		Model:      result.Model,
	}, nil
}

func (p *OllamaProvider) Health(_ context.Context) error { return nil }
