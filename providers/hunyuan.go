package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	hunyuanAPIVersion = "2023-09-01"
	hunyuanService    = "hunyuan"
	hunyuanRegion     = "ap-guangzhou"
)

// HunyuanProvider calls the official Tencent Hunyuan API with TC3 signatures.
type HunyuanProvider struct {
	cfg    Config
	client *http.Client
}

// NewHunyuan creates a Hunyuan provider. AccessKey is SecretId and Secret is SecretKey.
func NewHunyuan(cfg Config) *HunyuanProvider {
	cfg = applyDefaultBaseURL("hunyuan", cfg)
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if strings.TrimSpace(cfg.Region) == "" {
		cfg.Region = hunyuanRegion
	}
	return &HunyuanProvider{cfg: cfg, client: newHTTPClient(cfg.Timeout)}
}

func (p *HunyuanProvider) Name() string { return "hunyuan" }

func (p *HunyuanProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	body := map[string]any{
		"Model":    req.Model,
		"Messages": hunyuanMessages(req.Messages),
		"Stream":   false,
	}
	var payload hunyuanChatResponse
	if err := p.call(ctx, "ChatCompletions", body, &payload); err != nil {
		return nil, err
	}
	if err := payload.err(); err != nil {
		return nil, err
	}
	content := ""
	finish := ""
	if len(payload.Response.Choices) > 0 {
		content = payload.Response.Choices[0].Message.Content
		finish = payload.Response.Choices[0].FinishReason
	}
	return &Response{
		Content:      content,
		Model:        req.Model,
		Provider:     p.Name(),
		InputTokens:  payload.Response.Usage.PromptTokens,
		OutputTokens: payload.Response.Usage.CompletionTokens,
		TokensUsed:   payload.Response.Usage.TotalTokens,
		FinishReason: finish,
		CreatedAt:    time.Now(),
	}, nil
}

func (p *HunyuanProvider) Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error) {
	body := map[string]any{
		"Model":    req.Model,
		"Messages": hunyuanMessages(req.Messages),
		"Stream":   true,
	}
	resp, err := p.open(ctx, "ChatCompletions", body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, parseError(resp.StatusCode, readErrorBody(resp.Body), resp.Header, p.Name())
	}
	ch := make(chan *StreamChunk)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		scanner := newStreamScanner(resp.Body)
		var input, output int
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == line && strings.HasPrefix(line, "{") {
				data = line
			}
			if data == "[DONE]" {
				ch <- &StreamChunk{Done: true, InputTokens: input, OutputTokens: output, TokensUsed: input + output}
				return
			}
			var event hunyuanStreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				ch <- &StreamChunk{Error: fmt.Errorf("decode hunyuan stream: %w", err), Done: true}
				return
			}
			if event.Error != nil && event.Error.Message != "" {
				ch <- &StreamChunk{Error: fmt.Errorf("hunyuan: %s", event.Error.Message), Done: true}
				return
			}
			if event.Usage.PromptTokens > 0 {
				input = event.Usage.PromptTokens
			}
			if event.Usage.CompletionTokens > 0 {
				output = event.Usage.CompletionTokens
			}
			for _, choice := range event.Choices {
				content := choice.Delta.Content
				if content == "" {
					content = choice.Message.Content
				}
				chunk := &StreamChunk{Content: content, FinishReason: choice.FinishReason}
				if choice.FinishReason != "" {
					chunk.Done = true
					chunk.InputTokens = input
					chunk.OutputTokens = output
					chunk.TokensUsed = input + output
				}
				ch <- chunk
				if chunk.Done {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- &StreamChunk{Error: fmt.Errorf("read hunyuan stream: %w", err), Done: true}
			return
		}
		ch <- &StreamChunk{Done: true, InputTokens: input, OutputTokens: output, TokensUsed: input + output}
	}()
	return ch, nil
}

func (p *HunyuanProvider) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	if req == nil || len(req.Input) == 0 {
		return nil, fmt.Errorf("embed input required")
	}
	body := map[string]any{"InputList": req.Input}
	var payload struct {
		Response struct {
			Data []struct {
				Embedding []float64 `json:"Embedding"`
			} `json:"Data"`
			Error *hunyuanError `json:"Error"`
		} `json:"Response"`
	}
	if err := p.call(ctx, "GetEmbedding", body, &payload); err != nil {
		return nil, err
	}
	if payload.Response.Error != nil && payload.Response.Error.Message != "" {
		return nil, fmt.Errorf("hunyuan: %s", payload.Response.Error.Message)
	}
	vectors := make([][]float64, 0, len(payload.Response.Data))
	for _, item := range payload.Response.Data {
		vectors = append(vectors, item.Embedding)
	}
	return &EmbedResponse{Embeddings: vectors, Model: req.Model}, nil
}

func (p *HunyuanProvider) GenerateImage(ctx context.Context, req *ImageRequest) (*ImageResponse, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("image prompt required")
	}
	body := map[string]any{"Prompt": req.Prompt, "RspImgType": "url"}
	var payload struct {
		Response struct {
			ResultImage string        `json:"ResultImage"`
			Error       *hunyuanError `json:"Error"`
		} `json:"Response"`
	}
	if err := p.call(ctx, "TextToImage", body, &payload); err != nil {
		return nil, err
	}
	if payload.Response.Error != nil && payload.Response.Error.Message != "" {
		return nil, fmt.Errorf("hunyuan: %s", payload.Response.Error.Message)
	}
	return &ImageResponse{
		Images:   []Image{{URL: payload.Response.ResultImage, MediaType: "image/png"}},
		Model:    req.Model,
		Provider: p.Name(),
	}, nil
}

func (p *HunyuanProvider) ListModels(context.Context) ([]string, error) {
	return nil, unsupportedTargeted(p.Name(), "model listing")
}

func (p *HunyuanProvider) Health(context.Context) error {
	return unsupportedTargeted(p.Name(), "health checks")
}

func (p *HunyuanProvider) call(ctx context.Context, action string, body, out any) error {
	resp, err := p.open(ctx, action, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return parseError(resp.StatusCode, readErrorBody(resp.Body), resp.Header, p.Name())
	}
	if out == nil {
		return nil
	}
	raw, err := readResponseBody(resp.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (p *HunyuanProvider) open(ctx context.Context, action string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + "/"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if err := signTC3(httpReq, data, p.cfg.AccessKey, p.cfg.Secret, hunyuanService, action, hunyuanAPIVersion, p.cfg.Region, time.Now()); err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	return resp, nil
}

func hunyuanMessages(messages []Message) []map[string]string {
	out := make([]map[string]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, map[string]string{
			"Role":    message.Role,
			"Content": ExtractText(message.Content),
		})
	}
	return out
}

type hunyuanError struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

type hunyuanChatResponse struct {
	Response struct {
		Choices []struct {
			Message struct {
				Content string `json:"Content"`
			} `json:"Message"`
			FinishReason string `json:"FinishReason"`
		} `json:"Choices"`
		Usage struct {
			PromptTokens     int `json:"PromptTokens"`
			CompletionTokens int `json:"CompletionTokens"`
			TotalTokens      int `json:"TotalTokens"`
		} `json:"Usage"`
		Error *hunyuanError `json:"Error"`
	} `json:"Response"`
}

type hunyuanStreamEvent struct {
	Choices []struct {
		Delta struct {
			Content string `json:"Content"`
		} `json:"Delta"`
		Message struct {
			Content string `json:"Content"`
		} `json:"Message"`
		FinishReason string `json:"FinishReason"`
	} `json:"Choices"`
	Usage struct {
		PromptTokens     int `json:"PromptTokens"`
		CompletionTokens int `json:"CompletionTokens"`
	} `json:"Usage"`
	Error *hunyuanError `json:"Error"`
}

func (r hunyuanChatResponse) err() error {
	if r.Response.Error == nil || r.Response.Error.Message == "" {
		return nil
	}
	return fmt.Errorf("hunyuan: %s", r.Response.Error.Message)
}
