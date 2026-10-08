package providers

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (p *OpenAIProvider) SubmitOperation(ctx context.Context, req *OperationRequest) (*Operation, error) {
	switch p.name {
	case "qwen", "qwen-intl":
		return p.submitDashScope(ctx, req)
	case "zhipu":
		return p.submitZhipuVideo(ctx, req)
	case "doubao":
		return p.submitDoubaoVideo(ctx, req)
	default:
		return p.submitOpenAIVideo(ctx, req)
	}
}

func (p *OpenAIProvider) GetOperation(ctx context.Context, id string) (*Operation, error) {
	switch p.name {
	case "qwen", "qwen-intl":
		return p.getDashScope(ctx, id)
	case "zhipu":
		return p.getZhipu(ctx, id)
	case "doubao":
		return p.getDoubaoVideo(ctx, id)
	default:
		return p.getOpenAIVideo(ctx, id)
	}
}

func (p *OpenAIProvider) CancelOperation(context.Context, string) error {
	return unsupportedTargeted(p.Name(), "operation cancel")
}

func (p *OpenAIProvider) ReadOperation(ctx context.Context, id string) (*OperationAsset, error) {
	op, err := p.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	assetURL, mediaType := firstAsset(op)
	if assetURL == "" {
		return nil, fmt.Errorf("operation has no downloadable result")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	// Result URLs are often time-limited CDN links. Attach the provider
	// credential only when the URL is on the provider host.
	if sameHost(p.cfg.BaseURL, assetURL) {
		creds, err := p.cfg.credentials(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve credentials: %w", err)
		}
		p.setAuth(httpReq, creds)
	}
	return readAsset(p.client, httpReq, mediaType)
}

func (p *OpenAIProvider) submitOpenAIVideo(ctx context.Context, req *OperationRequest) (*Operation, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("operation prompt required")
	}
	if req.Kind != OperationVideo {
		return nil, unsupportedCapability(p.Name(), "async "+string(req.Kind))
	}
	if req.Image != nil && len(req.Image.Data) > 0 {
		return p.submitOpenAIVideoMultipart(ctx, req)
	}
	if req.Image != nil {
		return nil, fmt.Errorf("openai video reference requires image bytes")
	}
	body := map[string]any{"model": req.Model, "prompt": req.Prompt}
	if req.Size != "" {
		body["size"] = req.Size
	}
	if req.DurationSeconds > 0 {
		body["seconds"] = strconv.Itoa(req.DurationSeconds)
	}
	var payload openAIVideo
	if err := p.postJSON(ctx, "", "/v1/videos", body, &payload); err != nil {
		return nil, err
	}
	return p.finishOpenAIVideo(payload)
}

func (p *OpenAIProvider) submitOpenAIVideoMultipart(ctx context.Context, req *OperationRequest) (*Operation, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	fields := map[string]string{"model": req.Model, "prompt": req.Prompt}
	if req.Size != "" {
		fields["size"] = req.Size
	}
	if req.DurationSeconds > 0 {
		fields["seconds"] = strconv.Itoa(req.DurationSeconds)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	name := req.Image.FileName
	if name == "" {
		name = "reference.png"
	}
	part, err := writer.CreateFormFile("input_reference", name)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(req.Image.Data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	endpoint, err := p.openAIEndpoint("", "/v1/videos")
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	p.setAuth(httpReq, creds)
	var payload openAIVideo
	if err := doProviderJSONRequest(ctx, p.client, httpReq, p.Name(), &payload); err != nil {
		return nil, err
	}
	return p.finishOpenAIVideo(payload)
}

func (p *OpenAIProvider) getOpenAIVideo(ctx context.Context, id string) (*Operation, error) {
	if err := validOperationID(id); err != nil {
		return nil, err
	}
	endpoint, err := p.openAIEndpoint("", "/v1/videos/"+url.PathEscape(id))
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
	var payload openAIVideo
	if err := doProviderJSONRequest(ctx, p.client, httpReq, p.Name(), &payload); err != nil {
		return nil, err
	}
	return p.finishOpenAIVideo(payload)
}

type openAIVideo struct {
	ID       string `json:"id"`
	Model    string `json:"model"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (v openAIVideo) asOperation(provider string) *Operation {
	op := &Operation{
		ID:       v.ID,
		Provider: provider,
		Model:    v.Model,
		Kind:     OperationVideo,
		Status:   mapOpenAIVideoStatus(v.Status),
		Progress: v.Progress,
	}
	if v.Error != nil {
		op.Error = v.Error.Message
	}
	return op
}

func (p *OpenAIProvider) finishOpenAIVideo(payload openAIVideo) (*Operation, error) {
	op := payload.asOperation(p.Name())
	if op.Status != OperationSucceeded || op.ID == "" {
		return op, nil
	}
	content, err := p.openAIEndpoint("", "/v1/videos/"+url.PathEscape(op.ID)+"/content")
	if err != nil {
		return nil, err
	}
	op.Result = &OperationResult{Videos: []MediaAsset{{URL: content, MediaType: "video/mp4"}}}
	return op, nil
}

func mapOpenAIVideoStatus(status string) OperationStatus {
	switch strings.ToLower(status) {
	case "queued":
		return OperationQueued
	case "in_progress":
		return OperationRunning
	case "completed":
		return OperationSucceeded
	case "failed":
		return OperationFailed
	default:
		return OperationRunning
	}
}

func firstAsset(op *Operation) (string, string) {
	if op == nil || op.Status != OperationSucceeded || op.Result == nil {
		return "", ""
	}
	if len(op.Result.Videos) > 0 {
		return op.Result.Videos[0].URL, op.Result.Videos[0].MediaType
	}
	if len(op.Result.Images) > 0 {
		return op.Result.Images[0].URL, op.Result.Images[0].MediaType
	}
	if len(op.Result.Audio) > 0 {
		return op.Result.Audio[0].URL, op.Result.Audio[0].MediaType
	}
	return "", ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
