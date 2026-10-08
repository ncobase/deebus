package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// KlingProvider calls the official Kling API. Requests use a short-lived JWT
// derived from the access key and secret.
type KlingProvider struct {
	cfg        Config
	client     *http.Client
	tokenMu    sync.Mutex
	token      string
	tokenUntil time.Time
}

// NewKling creates a Kling provider.
func NewKling(cfg Config) *KlingProvider {
	cfg = applyDefaultBaseURL("kling", cfg)
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &KlingProvider{cfg: cfg, client: newHTTPClient(cfg.Timeout)}
}

// Name returns "kling".
func (p *KlingProvider) Name() string { return "kling" }

// Complete reports that this provider has no chat API.
func (p *KlingProvider) Complete(context.Context, *Request) (*Response, error) {
	return nil, unsupportedCapability(p.Name(), "chat completions")
}

// Stream reports that this provider has no chat API.
func (p *KlingProvider) Stream(context.Context, *Request) (<-chan *StreamChunk, error) {
	return nil, unsupportedCapability(p.Name(), "chat completions")
}

// Embed reports that this provider has no embeddings API.
func (p *KlingProvider) Embed(context.Context, *EmbedRequest) (*EmbedResponse, error) {
	return nil, unsupportedCapability(p.Name(), "embeddings")
}

// ListModels reports that this provider has no model list.
func (p *KlingProvider) ListModels(context.Context) ([]string, error) {
	return nil, unsupportedTargeted(p.Name(), "model listing")
}

// Health reports that this provider has no health endpoint.
func (p *KlingProvider) Health(context.Context) error {
	return unsupportedTargeted(p.Name(), "health checks")
}

// SubmitOperation calls POST /v1/videos/text2video, /v1/videos/image2video, or /v1/images/generations.
func (p *KlingProvider) SubmitOperation(ctx context.Context, req *OperationRequest) (*Operation, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("operation prompt required")
	}
	body := map[string]any{
		"model_name": req.Model,
		"prompt":     req.Prompt,
	}
	if req.NegativePrompt != "" {
		body["negative_prompt"] = req.NegativePrompt
	}
	if req.AspectRatio != "" {
		body["aspect_ratio"] = req.AspectRatio
	}
	collection := ""
	switch req.Kind {
	case OperationImage:
		collection = "images"
	case OperationVideo:
		if req.DurationSeconds > 0 {
			body["duration"] = fmt.Sprintf("%d", req.DurationSeconds)
		}
		if req.Image != nil && req.Image.URL != "" {
			body["image"] = req.Image.URL
			collection = "image2video"
		} else if req.Image != nil {
			return nil, fmt.Errorf("kling image-to-video requires an image url")
		} else {
			collection = "text2video"
		}
	default:
		return nil, unsupportedCapability(p.Name(), "async "+string(req.Kind))
	}
	payload, err := p.call(ctx, http.MethodPost, klingPath(collection, ""), body)
	if err != nil {
		return nil, err
	}
	op := payload.asOperation()
	op.ID = collection + "/" + payload.Data.TaskID
	op.Model = req.Model
	op.Kind = req.Kind
	return op, nil
}

// GetOperation queries the collection encoded in id.
func (p *KlingProvider) GetOperation(ctx context.Context, id string) (*Operation, error) {
	collection, taskID, err := splitKlingID(id)
	if err != nil {
		return nil, err
	}
	payload, err := p.call(ctx, http.MethodGet, klingPath(collection, taskID), nil)
	if err != nil {
		return nil, err
	}
	op := payload.asOperation()
	op.ID = collection + "/" + taskID
	return op, nil
}

// CancelOperation reports that Kling has no official cancel method.
func (p *KlingProvider) CancelOperation(context.Context, string) error {
	return unsupportedTargeted(p.Name(), "operation cancel")
}

// ReadOperation downloads the result URL returned by Kling.
func (p *KlingProvider) ReadOperation(ctx context.Context, id string) (*OperationAsset, error) {
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
	return readAsset(p.client, httpReq, mediaType)
}

func (p *KlingProvider) call(ctx context.Context, method, path string, body any) (*klingEnvelope, error) {
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + path
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if method == http.MethodPost {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	token, err := p.bearer(time.Now())
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	var payload klingEnvelope
	if err := doProviderJSONRequest(ctx, p.client, httpReq, p.Name(), &payload); err != nil {
		return nil, err
	}
	if payload.Code != 0 {
		message := payload.Message
		if message == "" {
			message = "kling request failed"
		}
		return nil, &ProviderError{
			Type:       ErrTypeUnknown,
			Provider:   p.Name(),
			StatusCode: http.StatusBadGateway,
			Message:    message,
			Retryable:  false,
			Fallback:   true,
		}
	}
	return &payload, nil
}

func (p *KlingProvider) bearer(now time.Time) (string, error) {
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()
	if p.token != "" && now.Before(p.tokenUntil) {
		return p.token, nil
	}
	token, err := KlingToken(p.cfg.AccessKey, p.cfg.Secret, now)
	if err != nil {
		return "", err
	}
	p.token = token
	// Official tokens last 30 minutes. Refresh before expiry.
	p.tokenUntil = now.Add(25 * time.Minute)
	return token, nil
}

func klingPath(collection, taskID string) string {
	var path string
	switch collection {
	case "images":
		path = "/v1/images/generations"
	case "image2video":
		path = "/v1/videos/image2video"
	default:
		path = "/v1/videos/text2video"
	}
	if taskID == "" {
		return path
	}
	return path + "/" + url.PathEscape(taskID)
}

func splitKlingID(id string) (string, string, error) {
	if err := validOperationID(id); err != nil {
		return "", "", err
	}
	collection, taskID, ok := strings.Cut(id, "/")
	if !ok || taskID == "" || strings.Contains(taskID, "/") {
		return "", "", fmt.Errorf("invalid kling operation id")
	}
	switch collection {
	case "images", "image2video", "text2video":
		return collection, taskID, nil
	default:
		return "", "", fmt.Errorf("invalid kling operation id")
	}
}

type klingEnvelope struct {
	Code    int       `json:"code"`
	Message string    `json:"message"`
	Data    klingData `json:"data"`
}

type klingData struct {
	TaskID        string `json:"task_id"`
	TaskStatus    string `json:"task_status"`
	TaskStatusMsg string `json:"task_status_msg"`
	TaskResult    struct {
		Videos []struct {
			URL string `json:"url"`
		} `json:"videos"`
		Images []struct {
			URL string `json:"url"`
		} `json:"images"`
	} `json:"task_result"`
}

func (e klingEnvelope) asOperation() *Operation {
	op := &Operation{
		Provider: "kling",
		Status:   mapKlingStatus(e.Data.TaskStatus),
		Error:    e.Data.TaskStatusMsg,
	}
	var videos []MediaAsset
	for _, video := range e.Data.TaskResult.Videos {
		if video.URL != "" {
			videos = append(videos, MediaAsset{URL: video.URL, MediaType: "video/mp4"})
		}
	}
	var images []Image
	for _, image := range e.Data.TaskResult.Images {
		if image.URL != "" {
			images = append(images, Image{URL: image.URL, MediaType: "image/png"})
		}
	}
	if len(videos) > 0 || len(images) > 0 {
		op.Result = &OperationResult{Videos: videos, Images: images}
	}
	if e.Code != 0 && op.Status != OperationFailed {
		op.Status = OperationFailed
		op.Error = e.Message
	}
	return op
}

func mapKlingStatus(status string) OperationStatus {
	switch strings.ToLower(status) {
	case "submitted":
		return OperationQueued
	case "processing":
		return OperationRunning
	case "succeed", "succeeded":
		return OperationSucceeded
	case "failed":
		return OperationFailed
	default:
		return OperationRunning
	}
}
