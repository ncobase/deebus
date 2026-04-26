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

// anthropicVersion is the required API version header value.
// Anthropic keeps this constant; new capabilities are gated by anthropic-beta.
const anthropicVersion = "2023-06-01"

// defaultMaxTokens is used when the caller does not specify MaxTokens.
// Anthropic's API requires the field; there is no server-side default.
const defaultMaxTokens = 4096

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
	maxTokens := defaultMaxTokens
	if limit := outputTokenLimit(req); limit > 0 {
		maxTokens = limit
	}

	_, msgs := ExtractSystemMessage(req.Messages)

	body := map[string]any{
		"model":      req.Model,
		"messages":   ConvertToAnthropicFormat(msgs),
		"max_tokens": maxTokens,
	}
	if sys := BuildAnthropicSystem(req.Messages); sys != nil {
		body["system"] = sys
	}
	if req.Cache != nil && req.Cache.Control != nil {
		body["cache_control"] = req.Cache.Control
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
	}
	if len(req.Stop) > 0 {
		body["stop_sequences"] = req.Stop
	}
	if thinking := anthropicThinking(req.Reasoning); thinking != nil {
		body["thinking"] = thinking
	}
	if len(req.Tools) > 0 {
		body["tools"] = ConvertToolsToAnthropic(req.Tools)
		if req.ToolChoice != "" {
			body["tool_choice"] = AnthropicToolChoice(req.ToolChoice)
		}
	}
	if req.UserID != "" {
		body["metadata"] = map[string]any{"user_id": req.UserID}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v1/messages")
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
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Content) == 0 {
		return nil, fmt.Errorf("empty response from anthropic")
	}

	content := ""
	thinking := ""
	var toolCalls []ToolCall
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			content = block.Text
		case "thinking":
			thinking += block.Text
		case "tool_use":
			args, _ := json.Marshal(block.Input)
			tc := ToolCall{
				ID:   block.ID,
				Type: "function",
			}
			tc.Function.Name = block.Name
			tc.Function.Arguments = string(args)
			toolCalls = append(toolCalls, tc)
		}
	}
	if strings.TrimSpace(content) == "" && thinking != "" {
		content = thinking
	}

	// Anthropic returns input_tokens as only the uncached portion.
	// True total input = input_tokens + cache_read + cache_creation.
	totalInput := result.Usage.InputTokens +
		result.Usage.CacheReadInputTokens +
		result.Usage.CacheCreationInputTokens

	return &Response{
		Content:      content,
		Model:        result.Model,
		Provider:     p.Name(),
		InputTokens:  totalInput,
		OutputTokens: result.Usage.OutputTokens,
		TokensUsed:   totalInput + result.Usage.OutputTokens,
		FinishReason: result.StopReason,
		ToolCalls:    toolCalls,
		CacheUsage: CacheUsage{
			CreatedTokens: result.Usage.CacheCreationInputTokens,
			ReadTokens:    result.Usage.CacheReadInputTokens,
		},
		CreatedAt: time.Now(),
	}, nil
}

func (p *AnthropicProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	maxTokens := defaultMaxTokens
	if limit := outputTokenLimit(req); limit > 0 {
		maxTokens = limit
	}

	_, msgs := ExtractSystemMessage(req.Messages)

	body := map[string]any{
		"model":      req.Model,
		"messages":   ConvertToAnthropicFormat(msgs),
		"max_tokens": maxTokens,
		"stream":     true,
	}
	if sys := BuildAnthropicSystem(req.Messages); sys != nil {
		body["system"] = sys
	}
	if req.Cache != nil && req.Cache.Control != nil {
		body["cache_control"] = req.Cache.Control
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
	}
	if len(req.Stop) > 0 {
		body["stop_sequences"] = req.Stop
	}
	if thinking := anthropicThinking(req.Reasoning); thinking != nil {
		body["thinking"] = thinking
	}
	if len(req.Tools) > 0 {
		body["tools"] = ConvertToolsToAnthropic(req.Tools)
		if req.ToolChoice != "" {
			body["tool_choice"] = AnthropicToolChoice(req.ToolChoice)
		}
	}
	if req.UserID != "" {
		body["metadata"] = map[string]any{"user_id": req.UserID}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v1/messages")
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

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, parseError(resp.StatusCode, b, resp.Header, p.Name())
	}

	ch := make(chan *StreamChunk, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		// blockAccumulator tracks state for one content block.
		type blockAccumulator struct {
			blockType   string // "text" or "tool_use"
			toolID      string
			toolName    string
			jsonBuilder strings.Builder
		}
		blocks := map[int]*blockAccumulator{}

		var inputTokens, outputTokens int
		var cacheCreated, cacheRead int
		var stopReason string

		// The event shape varies by Anthropic SSE event type.
		var event struct {
			Type  string `json:"type"`
			Index int    `json:"index"`

			// content_block_start
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`

			// content_block_delta
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`

			// message_start
			Message struct {
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`

			// message_delta usage
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")

			// Reset before each decode to avoid stale fields.
			event.Type = ""
			event.Index = 0
			event.ContentBlock.Type = ""
			event.ContentBlock.ID = ""
			event.ContentBlock.Name = ""
			event.Delta.Type = ""
			event.Delta.Text = ""
			event.Delta.PartialJSON = ""
			event.Delta.StopReason = ""

			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}

			switch event.Type {
			case "message_start":
				inputTokens = event.Message.Usage.InputTokens
				cacheCreated = event.Message.Usage.CacheCreationInputTokens
				cacheRead = event.Message.Usage.CacheReadInputTokens

			case "content_block_start":
				blocks[event.Index] = &blockAccumulator{
					blockType: event.ContentBlock.Type,
					toolID:    event.ContentBlock.ID,
					toolName:  event.ContentBlock.Name,
				}

			case "content_block_delta":
				acc, ok := blocks[event.Index]
				if !ok {
					continue
				}
				switch event.Delta.Type {
				case "thinking_delta":
					if event.Delta.Text != "" {
						select {
						case ch <- &StreamChunk{Reasoning: event.Delta.Text}:
						case <-ctx.Done():
							return
						}
					}
				case "text_delta":
					if event.Delta.Text != "" {
						select {
						case ch <- &StreamChunk{Content: event.Delta.Text}:
						case <-ctx.Done():
							return
						}
					}
				case "input_json_delta":
					acc.jsonBuilder.WriteString(event.Delta.PartialJSON)
				}

			case "message_delta":
				stopReason = event.Delta.StopReason
				outputTokens = event.Usage.OutputTokens

			case "message_stop":
				// Assemble any tool calls from accumulated blocks.
				var toolCalls []ToolCall
				for _, acc := range blocks {
					if acc.blockType == "tool_use" {
						tc := ToolCall{
							ID:   acc.toolID,
							Type: "function",
						}
						tc.Function.Name = acc.toolName
						tc.Function.Arguments = acc.jsonBuilder.String()
						toolCalls = append(toolCalls, tc)
					}
				}
				// inputTokens from message_start is only the uncached portion;
				// add cache buckets for the true total input.
				totalInput := inputTokens + cacheCreated + cacheRead
				final := &StreamChunk{
					Done:         true,
					FinishReason: stopReason,
					InputTokens:  totalInput,
					OutputTokens: outputTokens,
					TokensUsed:   totalInput + outputTokens,
					ToolCalls:    toolCalls,
					CacheUsage: CacheUsage{
						CreatedTokens: cacheCreated,
						ReadTokens:    cacheRead,
					},
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

func (p *AnthropicProvider) ListModels(ctx context.Context) ([]string, error) {
	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v1/models")
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	p.setHeaders(httpReq, creds)

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := doProviderJSONRequest(ctx, p.client, httpReq, p.Name(), &payload); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		models = append(models, item.ID)
	}
	return normalizeModelNames(models), nil
}

func (p *AnthropicProvider) Health(ctx context.Context) error {
	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v1/models")
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}
	p.setHeaders(httpReq, creds)

	return doProviderJSONRequest(ctx, p.client, httpReq, p.Name(), &struct{}{})
}

func (p *AnthropicProvider) setHeaders(r *http.Request, creds Credentials) {
	r.Header.Set("Content-Type", "application/json")
	if creds.APIKey != "" {
		r.Header.Set("x-api-key", creds.APIKey)
	}
	switch {
	case creds.BearerToken != "":
		r.Header.Set("Authorization", "Bearer "+creds.BearerToken)
	case creds.APIKey != "":
		r.Header.Set("Authorization", "Bearer "+creds.APIKey)
	}
	r.Header.Set("anthropic-version", anthropicVersion)
	applyHeaders(r, creds.Headers)
}
