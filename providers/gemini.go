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

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		p.cfg.BaseURL, req.Model, p.cfg.APIKey)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
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
			TotalTokenCount int `json:"totalTokenCount"`
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

	return &Response{
		Content:      content,
		Model:        req.Model,
		Provider:     p.Name(),
		TokensUsed:   result.UsageMetadata.TotalTokenCount,
		FinishReason: cand.FinishReason,
		ToolCalls:    toolCalls,
		CreatedAt:    time.Now(),
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

	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?key=%s&alt=sse",
		p.cfg.BaseURL, req.Model, p.cfg.APIKey)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
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

		var totalTokens int

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
					TotalTokenCount int `json:"totalTokenCount"`
				} `json:"usageMetadata"`
			}

			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}

			if event.UsageMetadata.TotalTokenCount > 0 {
				totalTokens = event.UsageMetadata.TotalTokenCount
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
					Done:         true,
					FinishReason: cand.FinishReason,
					TokensUsed:   totalTokens,
					ToolCalls:    toolCalls,
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

	url := fmt.Sprintf("%s/v1beta/models/%s:batchEmbedContents?key=%s",
		p.cfg.BaseURL, req.Model, p.cfg.APIKey)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
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
