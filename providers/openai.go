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
	if normalizeAPIMode(p.cfg.APIMode) == "responses" {
		return p.completeResponses(ctx, req)
	}
	body := map[string]any{
		"model":    req.Model,
		"messages": ConvertToOpenAIFormat(req.Messages),
	}
	if req.MaxOutputTokens > 0 {
		body["max_completion_tokens"] = req.MaxOutputTokens
	} else if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	applyCommonChatOptions(body, req)
	if rf := openAIResponseFormat(req.ResponseFormat); rf != nil {
		body["response_format"] = rf
	}
	if reasoning := reasoningMap(req.Reasoning); reasoning != nil {
		body["reasoning"] = reasoning
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
	if req.Cache != nil {
		if req.Cache.Key != "" {
			body["prompt_cache_key"] = req.Cache.Key
		}
		retention, err := normalizeOpenAICacheRetention(req.Cache.Retention)
		if err != nil {
			return nil, err
		}
		if retention != "" {
			body["prompt_cache_retention"] = retention
		}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v1/chat/completions")
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
		Choices []struct {
			Message struct {
				Content          string     `json:"content"`
				ReasoningContent string     `json:"reasoning_content"` // DeepSeek, vLLM (legacy)
				Reasoning        string     `json:"reasoning"`         // vLLM (current), some OpenAI-compat
				ToolCalls        []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			TotalTokens         int `json:"total_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"` // o-series models
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Model string `json:"model"`
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if err := json.Unmarshal(rawBody, &result); err != nil {
		// Fall back to SSE parsing when the endpoint returns streaming format.
		if strings.HasPrefix(strings.TrimSpace(string(rawBody)), "data:") {
			return p.completeFromSSE(ctx, req, rawBody)
		}
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("empty choices in response")
	}

	content := result.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		content = result.Choices[0].Message.ReasoningContent
	}
	if strings.TrimSpace(content) == "" {
		content = result.Choices[0].Message.Reasoning
	}

	return &Response{
		Content:         content,
		Reasoning:       result.Choices[0].Message.ReasoningContent + result.Choices[0].Message.Reasoning,
		Model:           result.Model,
		Provider:        p.Name(),
		InputTokens:     result.Usage.PromptTokens,
		OutputTokens:    result.Usage.CompletionTokens,
		TokensUsed:      result.Usage.TotalTokens,
		ReasoningTokens: result.Usage.CompletionTokensDetails.ReasoningTokens,
		FinishReason:    result.Choices[0].FinishReason,
		ToolCalls:       result.Choices[0].Message.ToolCalls,
		CacheUsage:      CacheUsage{ReadTokens: result.Usage.PromptTokensDetails.CachedTokens},
		Raw:             rawBody,
		CreatedAt:       time.Now(),
	}, nil
}

// completeFromSSE consumes an SSE body (returned by some endpoints even for
// non-streaming requests) and synthesises a Response from the aggregated chunks.
func (p *OpenAIProvider) completeFromSSE(ctx context.Context, req *Request, body []byte) (*Response, error) {
	ch := p.parseSSEStream(ctx, bytes.NewReader(body))

	var content strings.Builder
	var done *StreamChunk
	for chunk := range ch {
		if chunk.Error != nil {
			return nil, chunk.Error
		}
		content.WriteString(chunk.Content)
		if chunk.Done {
			done = chunk
		}
	}
	if done == nil {
		return nil, fmt.Errorf("no final chunk in SSE response")
	}

	return &Response{
		Content:         content.String(),
		Model:           req.Model,
		Provider:        p.Name(),
		InputTokens:     done.InputTokens,
		OutputTokens:    done.OutputTokens,
		TokensUsed:      done.TokensUsed,
		ReasoningTokens: done.ReasoningTokens,
		FinishReason:    done.FinishReason,
		ToolCalls:       done.ToolCalls,
		CacheUsage:      done.CacheUsage,
		CreatedAt:       time.Now(),
	}, nil
}

func (p *OpenAIProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	if normalizeAPIMode(p.cfg.APIMode) == "responses" {
		return p.streamResponses(ctx, req)
	}
	body := map[string]any{
		"model":          req.Model,
		"messages":       ConvertToOpenAIFormat(req.Messages),
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.MaxOutputTokens > 0 {
		body["max_completion_tokens"] = req.MaxOutputTokens
	} else if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	applyCommonChatOptions(body, req)
	if rf := openAIResponseFormat(req.ResponseFormat); rf != nil {
		body["response_format"] = rf
	}
	if reasoning := reasoningMap(req.Reasoning); reasoning != nil {
		body["reasoning"] = reasoning
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
	if req.Cache != nil {
		if req.Cache.Key != "" {
			body["prompt_cache_key"] = req.Cache.Key
		}
		retention, err := normalizeOpenAICacheRetention(req.Cache.Retention)
		if err != nil {
			return nil, err
		}
		if retention != "" {
			body["prompt_cache_retention"] = retention
		}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v1/chat/completions")
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

	respBody := resp.Body
	out := make(chan *StreamChunk, 16)
	go func() {
		defer close(out)
		defer respBody.Close()
		for c := range p.parseSSEStream(ctx, respBody) {
			out <- c
		}
	}()
	return out, nil
}

// parseSSEStream reads OpenAI SSE chunks from r, emitting *StreamChunk values
// on the returned channel. The channel is closed when the stream ends.
func (p *OpenAIProvider) parseSSEStream(ctx context.Context, r io.Reader) <-chan *StreamChunk {
	ch := make(chan *StreamChunk, 16)
	go func() {
		defer close(ch)

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

		scanner := bufio.NewScanner(r)
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
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
						Reasoning        string `json:"reasoning"`
						ToolCalls        []struct {
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
					PromptTokens        int `json:"prompt_tokens"`
					CompletionTokens    int `json:"completion_tokens"`
					TotalTokens         int `json:"total_tokens"`
					PromptTokensDetails struct {
						CachedTokens int `json:"cached_tokens"`
					} `json:"prompt_tokens_details"`
					CompletionTokensDetails struct {
						ReasoningTokens int `json:"reasoning_tokens"`
					} `json:"completion_tokens_details"`
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
				pending.ReasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
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
			text := choice.Delta.Content
			if text == "" {
				text = choice.Delta.ReasoningContent
			}
			if text == "" {
				text = choice.Delta.Reasoning
			}
			if text != "" {
				select {
				case ch <- &StreamChunk{Content: text}:
				case <-ctx.Done():
					return
				}
			}

			// When finish_reason is set, build the final chunk.
			// If usage is present in the same chunk, populate and emit
			// immediately; otherwise hold pending for the next chunk or [DONE].
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
				if chunk.Usage != nil {
					pending.InputTokens = chunk.Usage.PromptTokens
					pending.OutputTokens = chunk.Usage.CompletionTokens
					pending.TokensUsed = chunk.Usage.TotalTokens
					pending.ReasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
					pending.CacheUsage = CacheUsage{
						ReadTokens: chunk.Usage.PromptTokensDetails.CachedTokens,
					}
					select {
					case ch <- pending:
					case <-ctx.Done():
					}
					pending = nil
				}
			}
		}

		// Emit any pending final chunk if the stream closed before [DONE].
		if pending != nil {
			select {
			case ch <- pending:
			case <-ctx.Done():
			}
		}

		if err := scanner.Err(); err != nil {
			select {
			case ch <- &StreamChunk{Error: fmt.Errorf("stream read: %w", err)}:
			case <-ctx.Done():
			}
		}
	}()

	return ch
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

	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v1/embeddings")
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

func normalizeOpenAICacheRetention(retention string) (string, error) {
	switch retention {
	case "":
		return "", nil
	case "in_memory", "in-memory":
		return "in_memory", nil
	case "24h":
		return "24h", nil
	default:
		return "", fmt.Errorf("invalid OpenAI prompt cache retention %q: want in_memory or 24h", retention)
	}
}

func (p *OpenAIProvider) ListModels(ctx context.Context) ([]string, error) {
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

func (p *OpenAIProvider) Health(ctx context.Context) error {
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

func (p *OpenAIProvider) setHeaders(r *http.Request, creds Credentials) {
	r.Header.Set("Content-Type", "application/json")
	switch {
	case creds.BearerToken != "":
		r.Header.Set("Authorization", "Bearer "+creds.BearerToken)
	case creds.APIKey != "":
		r.Header.Set("Authorization", "Bearer "+creds.APIKey)
	}
	if creds.Organization != "" {
		r.Header.Set("OpenAI-Organization", creds.Organization)
	}
	if creds.Project != "" {
		r.Header.Set("OpenAI-Project", creds.Project)
	}
	applyHeaders(r, creds.Headers)
}

func (p *OpenAIProvider) completeResponses(ctx context.Context, req *Request) (*Response, error) {
	body := p.responsesBody(req, false)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v1/responses")
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
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return p.parseResponsesResult(req, rawBody)
}

func (p *OpenAIProvider) streamResponses(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	body := p.responsesBody(req, true)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	endpoint, err := buildProviderEndpoint(p.cfg.BaseURL, "/v1/responses")
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
	out := make(chan *StreamChunk, 16)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		for c := range p.parseResponsesSSE(ctx, resp.Body) {
			out <- c
		}
	}()
	return out, nil
}

func (p *OpenAIProvider) responsesBody(req *Request, stream bool) map[string]any {
	body := map[string]any{
		"model": req.Model,
		"input": ConvertToOpenAIFormat(req.Messages),
	}
	if stream {
		body["stream"] = true
	}
	if limit := outputTokenLimit(req); limit > 0 {
		body["max_output_tokens"] = limit
	}
	applyCommonChatOptions(body, req)
	if req.UserID != "" {
		body["user"] = req.UserID
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
		if req.ToolChoice != "" {
			body["tool_choice"] = req.ToolChoice
		}
	}
	if text := openAIResponsesText(req.ResponseFormat); text != nil {
		body["text"] = text
	}
	if reasoning := reasoningMap(req.Reasoning); reasoning != nil {
		body["reasoning"] = reasoning
	}
	if req.Cache != nil && req.Cache.Key != "" {
		body["prompt_cache_key"] = req.Cache.Key
	}
	mergeOptions(body, req, nil)
	return body
}

func (p *OpenAIProvider) parseResponsesResult(req *Request, rawBody []byte) (*Response, error) {
	var result struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Output []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Name      string `json:"name"`
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
			Summary   []struct {
				Text string `json:"text"`
			} `json:"summary"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			TotalTokens        int `json:"total_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	}
	if err := json.Unmarshal(rawBody, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	var content, reasoning strings.Builder
	var toolCalls []ToolCall
	for _, item := range result.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					content.WriteString(part.Text)
				}
			}
		case "function_call":
			tc := ToolCall{ID: item.CallID, Type: "function"}
			if tc.ID == "" {
				tc.ID = item.ID
			}
			tc.Function.Name = item.Name
			tc.Function.Arguments = item.Arguments
			toolCalls = append(toolCalls, tc)
		case "reasoning":
			for _, summary := range item.Summary {
				reasoning.WriteString(summary.Text)
			}
		}
	}
	finish := result.Status
	if result.IncompleteDetails.Reason != "" {
		finish = result.IncompleteDetails.Reason
	}
	model := result.Model
	if model == "" {
		model = req.Model
	}
	return &Response{
		Content:         content.String(),
		Reasoning:       reasoning.String(),
		Model:           model,
		Provider:        p.Name(),
		InputTokens:     result.Usage.InputTokens,
		OutputTokens:    result.Usage.OutputTokens,
		TokensUsed:      result.Usage.TotalTokens,
		ReasoningTokens: result.Usage.OutputTokensDetails.ReasoningTokens,
		FinishReason:    finish,
		ToolCalls:       toolCalls,
		CacheUsage:      CacheUsage{ReadTokens: result.Usage.InputTokensDetails.CachedTokens},
		Raw:             rawBody,
		CreatedAt:       time.Now(),
	}, nil
}

func (p *OpenAIProvider) parseResponsesSSE(ctx context.Context, r io.Reader) <-chan *StreamChunk {
	ch := make(chan *StreamChunk, 16)
	go func() {
		defer close(ch)
		type tcAccumulator struct {
			id, name  string
			arguments strings.Builder
		}
		tools := map[int]*tcAccumulator{}
		var final *StreamChunk
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "[DONE]" {
				break
			}
			var event struct {
				Type        string          `json:"type"`
				Delta       string          `json:"delta"`
				Text        string          `json:"text"`
				ItemID      string          `json:"item_id"`
				OutputIndex int             `json:"output_index"`
				Name        string          `json:"name"`
				Arguments   string          `json:"arguments"`
				Response    json.RawMessage `json:"response"`
			}
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				select {
				case ch <- &StreamChunk{Error: fmt.Errorf("parse chunk: %w", err)}:
				case <-ctx.Done():
				}
				return
			}
			switch event.Type {
			case "response.output_text.delta":
				select {
				case ch <- &StreamChunk{Content: event.Delta, Raw: []byte(payload)}:
				case <-ctx.Done():
					return
				}
			case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
				select {
				case ch <- &StreamChunk{Reasoning: event.Delta, Raw: []byte(payload)}:
				case <-ctx.Done():
					return
				}
			case "response.function_call_arguments.delta":
				acc := tools[event.OutputIndex]
				if acc == nil {
					acc = &tcAccumulator{id: event.ItemID}
					tools[event.OutputIndex] = acc
				}
				acc.arguments.WriteString(event.Delta)
			case "response.output_item.done":
				if event.Name != "" || event.Arguments != "" {
					acc := tools[event.OutputIndex]
					if acc == nil {
						acc = &tcAccumulator{id: event.ItemID}
						tools[event.OutputIndex] = acc
					}
					acc.name = event.Name
					if event.Arguments != "" && acc.arguments.Len() == 0 {
						acc.arguments.WriteString(event.Arguments)
					}
				}
			case "response.completed", "response.incomplete":
				resp, err := p.parseResponsesResult(&Request{}, event.Response)
				if err == nil {
					final = &StreamChunk{Done: true, FinishReason: resp.FinishReason, InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens, TokensUsed: resp.TokensUsed, ReasoningTokens: resp.ReasoningTokens, CacheUsage: resp.CacheUsage, Raw: event.Response}
				}
			}
		}
		if final == nil {
			final = &StreamChunk{Done: true}
		}
		for idx, acc := range tools {
			tc := ToolCall{Index: idx, ID: acc.id, Type: "function"}
			tc.Function.Name = acc.name
			tc.Function.Arguments = acc.arguments.String()
			final.ToolCalls = append(final.ToolCalls, tc)
		}
		select {
		case ch <- final:
		case <-ctx.Done():
		}
		if err := scanner.Err(); err != nil {
			select {
			case ch <- &StreamChunk{Error: fmt.Errorf("stream read: %w", err)}:
			case <-ctx.Done():
			}
		}
	}()
	return ch
}
