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
	messages := cohereMessages(req.Messages)

	body := map[string]any{
		"model":    req.Model,
		"messages": messages,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if len(req.Tools) > 0 {
		// Cohere v2 uses standard JSON Schema format — same as our unified Tool type.
		body["tools"] = req.Tools
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
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"` // JSON-encoded string
				} `json:"function"`
			} `json:"tool_calls"`
			ToolPlan string `json:"tool_plan"`
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

	var toolCalls []ToolCall
	for _, ctc := range result.Message.ToolCalls {
		tc := ToolCall{
			ID:   ctc.ID,
			Type: ctc.Type,
		}
		tc.Function.Name = ctc.Function.Name
		tc.Function.Arguments = ctc.Function.Arguments
		toolCalls = append(toolCalls, tc)
	}

	return &Response{
		Content:      content,
		Model:        req.Model,
		Provider:     p.Name(),
		InputTokens:  result.Usage.Tokens.InputTokens,
		OutputTokens: result.Usage.Tokens.OutputTokens,
		TokensUsed:   result.Usage.Tokens.InputTokens + result.Usage.Tokens.OutputTokens,
		FinishReason: result.FinishReason,
		ToolCalls:    toolCalls,
		CreatedAt:    time.Now(),
	}, nil
}

// Stream implements streaming via Cohere's SSE endpoint, including tool-call events.
func (p *CohereProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	messages := cohereMessages(req.Messages)

	body := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   true,
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

		// Accumulates streaming tool-call argument fragments by index.
		type tcAccumulator struct {
			id       string
			typ      string
			name     string
			argsBuf  strings.Builder
		}
		accumulators := map[int]*tcAccumulator{}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")

			var event struct {
				Type  string `json:"type"`
				Index int    `json:"index"`
				Delta struct {
					Type         string `json:"type"`
					Text         string `json:"text"`
					FinishReason string `json:"finish_reason"`
					ToolCall     struct {
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_call"`
				} `json:"delta"`
				Usage struct {
					Tokens struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"tokens"`
				} `json:"usage"`
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

			case "tool-call-start":
				accumulators[event.Index] = &tcAccumulator{
					id:   event.Delta.ToolCall.ID,
					typ:  event.Delta.ToolCall.Type,
					name: event.Delta.ToolCall.Function.Name,
				}

			case "tool-call-delta":
				if acc, ok := accumulators[event.Index]; ok {
					acc.argsBuf.WriteString(event.Delta.ToolCall.Function.Arguments)
				}

			case "message-end":
				final := &StreamChunk{
					Done:         true,
					FinishReason: event.Delta.FinishReason,
					InputTokens:  event.Usage.Tokens.InputTokens,
					OutputTokens: event.Usage.Tokens.OutputTokens,
					TokensUsed:   event.Usage.Tokens.InputTokens + event.Usage.Tokens.OutputTokens,
				}
				for idx, acc := range accumulators {
					tc := ToolCall{
						Index: idx,
						ID:    acc.id,
						Type:  acc.typ,
					}
					tc.Function.Name = acc.name
					tc.Function.Arguments = acc.argsBuf.String()
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

func (p *CohereProvider) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	inputType := "search_document"
	if req.InputType != "" {
		inputType = req.InputType
	}

	body := map[string]any{
		"model":      req.Model,
		"texts":      req.Input,
		"input_type": inputType,
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
		Model      string      `json:"model"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	model := result.Model
	if model == "" {
		model = req.Model
	}

	return &EmbedResponse{
		Embeddings: result.Embeddings,
		Model:      model,
	}, nil
}

func (p *CohereProvider) Health(_ context.Context) error { return nil }

func (p *CohereProvider) setHeaders(r *http.Request) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
}

// cohereMessages converts the unified message slice to Cohere's v2 chat format.
// Regular messages use a plain string content; assistant messages with tool calls
// include the tool_calls array; tool result messages use the document content type.
func cohereMessages(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, msg := range msgs {
		switch msg.Role {
		case "tool":
			// Cohere v2 tool result: role="tool", tool_call_id, content as document.
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": msg.ToolCallID,
				"content": []map[string]any{
					{
						"type":     "document",
						"document": map[string]any{"data": ExtractText(msg.Content)},
					},
				},
			})

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				m := map[string]any{
					"role":       "assistant",
					"tool_calls": msg.ToolCalls,
				}
				if text := ExtractText(msg.Content); text != "" {
					m["tool_plan"] = text
				}
				out = append(out, m)
			} else {
				out = append(out, map[string]any{
					"role":    "assistant",
					"content": ExtractText(msg.Content),
				})
			}

		default:
			out = append(out, map[string]any{
				"role":    msg.Role,
				"content": ExtractText(msg.Content),
			})
		}
	}
	return out
}
