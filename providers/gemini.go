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

// GeminiProvider implements Provider for the Google Gemini API.
type GeminiProvider struct {
	cfg    Config
	client *http.Client
}

// NewGemini creates a new Gemini provider.
func NewGemini(cfg Config) *GeminiProvider {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &GeminiProvider{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
}

func (p *GeminiProvider) Name() string { return "gemini" }

func (p *GeminiProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	system, msgs := ExtractSystemMessage(req.Messages)

	body := map[string]any{"contents": ConvertToGeminiFormat(msgs)}

	if system != "" {
		body["systemInstruction"] = map[string]any{
			"role":  "system",
			"parts": []map[string]any{{"text": system}},
		}
	}
	if req.Cache != nil && req.Cache.CachedContent != "" {
		body["cachedContent"] = req.Cache.CachedContent
	}

	if req.Temperature > 0 || req.MaxTokens > 0 {
		gc := map[string]any{}
		if req.Temperature > 0 {
			gc["temperature"] = req.Temperature
		}
		if req.MaxTokens > 0 {
			gc["maxOutputTokens"] = req.MaxTokens
		}
		body["generationConfig"] = gc
	}

	if len(req.Tools) > 0 {
		body["tools"] = ConvertToolsToGemini(req.Tools)
		if req.ToolChoice != "" {
			body["toolConfig"] = GeminiToolConfig(req.ToolChoice)
		}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}

	url := p.geminiURL(creds, fmt.Sprintf("/v1beta/models/%s:generateContent", req.Model), "")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
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
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string         `json:"name"`
						Args map[string]any `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount        int `json:"promptTokenCount"`
			CandidatesTokenCount    int `json:"candidatesTokenCount"`
			TotalTokenCount         int `json:"totalTokenCount"`
			CachedContentTokenCount int `json:"cachedContentTokenCount"` // context caching
			ThoughtsTokenCount      int `json:"thoughtsTokenCount"`      // thinking models
		} `json:"usageMetadata"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Candidates) == 0 {
		return nil, fmt.Errorf("empty response from gemini")
	}

	cand := result.Candidates[0]
	content := ""
	var toolCalls []ToolCall

	for _, part := range cand.Content.Parts {
		if part.Text != "" {
			content += part.Text
		}
		if part.FunctionCall != nil {
			args, _ := json.Marshal(part.FunctionCall.Args)
			tc := ToolCall{Type: "function"}
			tc.Function.Name = part.FunctionCall.Name
			tc.Function.Arguments = string(args)
			toolCalls = append(toolCalls, tc)
		}
	}

	if content == "" && len(toolCalls) == 0 {
		return nil, fmt.Errorf("empty response from gemini")
	}

	// thoughtsTokenCount (thinking models) is billed in addition to candidatesTokenCount.
	// Compute TokensUsed from parts; totalTokenCount excludes cached tokens so is unreliable.
	input := result.UsageMetadata.PromptTokenCount
	output := result.UsageMetadata.CandidatesTokenCount + result.UsageMetadata.ThoughtsTokenCount

	return &Response{
		Content:         content,
		Model:           req.Model,
		Provider:        p.Name(),
		InputTokens:     input,
		OutputTokens:    output,
		TokensUsed:      input + output,
		ReasoningTokens: result.UsageMetadata.ThoughtsTokenCount,
		FinishReason:    cand.FinishReason,
		ToolCalls:       toolCalls,
		CacheUsage:      CacheUsage{ReadTokens: result.UsageMetadata.CachedContentTokenCount},
		CreatedAt:       time.Now(),
	}, nil
}

// Stream implements streaming via Gemini's SSE endpoint (:streamGenerateContent?alt=sse).
func (p *GeminiProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	system, msgs := ExtractSystemMessage(req.Messages)

	body := map[string]any{"contents": ConvertToGeminiFormat(msgs)}

	if system != "" {
		body["systemInstruction"] = map[string]any{
			"role":  "system",
			"parts": []map[string]any{{"text": system}},
		}
	}
	if req.Cache != nil && req.Cache.CachedContent != "" {
		body["cachedContent"] = req.Cache.CachedContent
	}

	if req.Temperature > 0 || req.MaxTokens > 0 {
		gc := map[string]any{}
		if req.Temperature > 0 {
			gc["temperature"] = req.Temperature
		}
		if req.MaxTokens > 0 {
			gc["maxOutputTokens"] = req.MaxTokens
		}
		body["generationConfig"] = gc
	}

	if len(req.Tools) > 0 {
		body["tools"] = ConvertToolsToGemini(req.Tools)
		if req.ToolChoice != "" {
			body["toolConfig"] = GeminiToolConfig(req.ToolChoice)
		}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}

	url := p.geminiURL(creds, fmt.Sprintf("/v1beta/models/%s:streamGenerateContent", req.Model), "alt=sse")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
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

		var inputTokens, outputTokens, cacheRead, thoughts int

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")

			var event struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text         string `json:"text"`
							FunctionCall *struct {
								Name string         `json:"name"`
								Args map[string]any `json:"args"`
							} `json:"functionCall"`
						} `json:"parts"`
					} `json:"content"`
					FinishReason string `json:"finishReason"`
				} `json:"candidates"`
				UsageMetadata struct {
					PromptTokenCount        int `json:"promptTokenCount"`
					CandidatesTokenCount    int `json:"candidatesTokenCount"`
					CachedContentTokenCount int `json:"cachedContentTokenCount"`
					ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
				} `json:"usageMetadata"`
			}

			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}

			if event.UsageMetadata.PromptTokenCount > 0 {
				inputTokens = event.UsageMetadata.PromptTokenCount
				outputTokens = event.UsageMetadata.CandidatesTokenCount
				cacheRead = event.UsageMetadata.CachedContentTokenCount
				thoughts = event.UsageMetadata.ThoughtsTokenCount
			}
			if len(event.Candidates) == 0 {
				continue
			}

			cand := event.Candidates[0]
			var toolCalls []ToolCall

			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					select {
					case ch <- &StreamChunk{Content: part.Text}:
					case <-ctx.Done():
						return
					}
				}
				if part.FunctionCall != nil {
					args, _ := json.Marshal(part.FunctionCall.Args)
					tc := ToolCall{Type: "function"}
					tc.Function.Name = part.FunctionCall.Name
					tc.Function.Arguments = string(args)
					toolCalls = append(toolCalls, tc)
				}
			}

			if cand.FinishReason != "" {
				select {
				case ch <- &StreamChunk{
					Done:            true,
					FinishReason:    cand.FinishReason,
					InputTokens:     inputTokens,
					OutputTokens:    outputTokens + thoughts,
					TokensUsed:      inputTokens + outputTokens + thoughts,
					ReasoningTokens: thoughts,
					ToolCalls:       toolCalls,
					CacheUsage:      CacheUsage{ReadTokens: cacheRead},
				}:
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

// Embed generates embeddings using the Gemini batchEmbedContents endpoint.
func (p *GeminiProvider) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	// Map unified InputType to Gemini taskType.
	taskType := geminiTaskType(req.InputType)

	requests := make([]map[string]any, len(req.Input))
	for i, text := range req.Input {
		r := map[string]any{
			"model":   "models/" + req.Model,
			"content": map[string]any{"parts": []map[string]any{{"text": text}}},
		}
		if taskType != "" {
			r["taskType"] = taskType
		}
		requests[i] = r
	}

	body := map[string]any{"requests": requests}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}

	url := p.geminiURL(creds, fmt.Sprintf("/v1beta/models/%s:batchEmbedContents", req.Model), "")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
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
		Embeddings []struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	embeddings := make([][]float64, len(result.Embeddings))
	for i, e := range result.Embeddings {
		embeddings[i] = e.Values
	}

	return &EmbedResponse{
		Embeddings: embeddings,
		Model:      req.Model,
	}, nil
}

func (p *GeminiProvider) Health(_ context.Context) error { return nil }

func (p *GeminiProvider) setHeaders(r *http.Request, creds Credentials) {
	r.Header.Set("Content-Type", "application/json")
	switch {
	case creds.BearerToken != "":
		r.Header.Set("Authorization", "Bearer "+creds.BearerToken)
	case creds.APIKey != "" && !strings.Contains(p.cfg.BaseURL, "googleapis.com"):
		r.Header.Set("Authorization", "Bearer "+creds.APIKey)
	}
	if creds.UserProject != "" {
		r.Header.Set("x-goog-user-project", creds.UserProject)
	}
	applyHeaders(r, creds.Headers)
}

// geminiURL builds an endpoint URL for the Gemini provider.
// For the native Google API (googleapis.com) the key is passed as a query
// parameter (?key=); for all other base URLs the key is omitted from the URL
// and authentication relies on the Authorization: Bearer header instead.
func (p *GeminiProvider) geminiURL(creds Credentials, path, extraQuery string) string {
	base := p.cfg.BaseURL + path
	if strings.Contains(p.cfg.BaseURL, "googleapis.com") && creds.BearerToken == "" && creds.APIKey != "" {
		q := "key=" + creds.APIKey
		if extraQuery != "" {
			q += "&" + extraQuery
		}
		return base + "?" + q
	}
	if extraQuery != "" {
		return base + "?" + extraQuery
	}
	return base
}

// geminiTaskType maps a unified InputType string to a Gemini taskType constant.
func geminiTaskType(inputType string) string {
	switch inputType {
	case "search_query":
		return "RETRIEVAL_QUERY"
	case "search_document":
		return "RETRIEVAL_DOCUMENT"
	case "classification":
		return "CLASSIFICATION"
	case "clustering":
		return "CLUSTERING"
	default:
		return ""
	}
}
