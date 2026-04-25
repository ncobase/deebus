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

// convertToOllamaFormat converts messages to Ollama's /api/chat format.
// Ollama requires content as a plain string, not an array of content blocks.
// Images are passed via the "images" field as base64 strings.
func convertToOllamaFormat(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		m := map[string]any{"role": msg.Role}
		var images []string
		var textParts []string
		for _, block := range msg.Content {
			switch b := block.(type) {
			case TextContent:
				textParts = append(textParts, b.Text)
			case ImageContent:
				if b.Source.Type == "base64" {
					images = append(images, b.Source.Data)
				}
			}
		}
		m["content"] = strings.Join(textParts, "\n")
		if len(images) > 0 {
			m["images"] = images
		}
		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}
		if len(msg.ToolCalls) > 0 {
			m["tool_calls"] = msg.ToolCalls
		}
		out = append(out, m)
	}
	return out
}

func (p *OllamaProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": convertToOllamaFormat(req.Messages),
		"stream":   false,
	}
	options := map[string]any{}
	for k, v := range req.Options {
		options[k] = v
	}
	if req.Temperature > 0 {
		options["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		options["top_p"] = req.TopP
	}
	if req.Seed != nil {
		options["seed"] = *req.Seed
	}
	if len(req.Stop) > 0 {
		options["stop"] = req.Stop
	}
	if limit := outputTokenLimit(req); limit > 0 {
		options["num_predict"] = limit
	}
	if len(options) > 0 {
		body["options"] = options
	}
	if format := ollamaFormat(req.ResponseFormat); format != nil {
		body["format"] = format
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
			Reasoning string `json:"reasoning"`
			ToolCalls []struct {
				Function struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		Model           string `json:"model"`
		DoneReason      string `json:"done_reason"`
		PromptEvalCount int    `json:"prompt_eval_count"`
		EvalCount       int    `json:"eval_count"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	content := result.Message.Content
	if strings.TrimSpace(content) == "" {
		content = result.Message.Reasoning
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
		Content:      content,
		Model:        result.Model,
		Provider:     p.Name(),
		InputTokens:  result.PromptEvalCount,
		OutputTokens: result.EvalCount,
		TokensUsed:   result.PromptEvalCount + result.EvalCount,
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
		"messages": convertToOllamaFormat(req.Messages),
		"stream":   true,
	}
	options := map[string]any{}
	for k, v := range req.Options {
		options[k] = v
	}
	if req.Temperature > 0 {
		options["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		options["top_p"] = req.TopP
	}
	if req.Seed != nil {
		options["seed"] = *req.Seed
	}
	if len(req.Stop) > 0 {
		options["stop"] = req.Stop
	}
	if limit := outputTokenLimit(req); limit > 0 {
		options["num_predict"] = limit
	}
	if len(options) > 0 {
		body["options"] = options
	}
	if format := ollamaFormat(req.ResponseFormat); format != nil {
		body["format"] = format
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
					Reasoning string `json:"reasoning"`
					ToolCalls []struct {
						Function struct {
							Name      string         `json:"name"`
							Arguments map[string]any `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
				Done            bool   `json:"done"`
				DoneReason      string `json:"done_reason"`
				PromptEvalCount int    `json:"prompt_eval_count"`
				EvalCount       int    `json:"eval_count"`
			}

			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				continue
			}

			text := event.Message.Content
			if text == "" {
				text = event.Message.Reasoning
			}
			if text != "" {
				select {
				case ch <- &StreamChunk{Content: text}:
				case <-ctx.Done():
					return
				}
			}

			if event.Done {
				final := &StreamChunk{
					Done:         true,
					FinishReason: event.DoneReason,
					InputTokens:  event.PromptEvalCount,
					OutputTokens: event.EvalCount,
					TokensUsed:   event.PromptEvalCount + event.EvalCount,
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
