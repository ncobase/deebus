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
	body := map[string]any{"contents": ConvertToGeminiFormat(req.Messages)}
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
					Text string `json:"text"`
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

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from gemini")
	}

	return &Response{
		Content:      result.Candidates[0].Content.Parts[0].Text,
		Model:        req.Model,
		Provider:     p.Name(),
		TokensUsed:   result.UsageMetadata.TotalTokenCount,
		FinishReason: result.Candidates[0].FinishReason,
		CreatedAt:    time.Now(),
	}, nil
}

// Stream implements streaming via Gemini's SSE endpoint (:streamGenerateContent?alt=sse).
func (p *GeminiProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	body := map[string]any{"contents": ConvertToGeminiFormat(req.Messages)}
	if req.MaxTokens > 0 {
		body["generationConfig"] = map[string]any{"maxOutputTokens": req.MaxTokens}
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
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
					FinishReason string `json:"finishReason"`
				} `json:"candidates"`
			}

			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}

			if len(event.Candidates) == 0 {
				continue
			}

			cand := event.Candidates[0]
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					select {
					case ch <- &StreamChunk{Content: part.Text}:
					case <-ctx.Done():
						return
					}
				}
			}

			if cand.FinishReason != "" {
				select {
				case ch <- &StreamChunk{Done: true, FinishReason: cand.FinishReason}:
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

// Embed is not yet implemented for Gemini in this library.
func (p *GeminiProvider) Embed(_ context.Context, _ *EmbedRequest) (*EmbedResponse, error) {
	return nil, &ProviderError{
		Type:      ErrTypeInvalidReq,
		Provider:  p.Name(),
		Message:   "gemini embeddings not yet implemented",
		Retryable: false,
		Fallback:  true,
	}
}

func (p *GeminiProvider) Health(_ context.Context) error { return nil }
