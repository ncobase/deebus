package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	jimengHost     = "visual.volcengineapi.com"
	jimengVersion  = "2022-08-31"
	jimengRegion   = "cn-north-1"
	jimengService  = "cv"
	jimengImageKey = "jimeng_t2i_v40"
)

// JimengProvider calls the official Volcengine visual API used by Jimeng.
// AccessKey and Secret sign each request. Region and service are fixed by
// the official visual contract unless the caller overrides region.
type JimengProvider struct {
	cfg    Config
	client *http.Client
}

// NewJimeng creates a Jimeng visual provider.
func NewJimeng(cfg Config) *JimengProvider {
	cfg = applyDefaultBaseURL("jimeng", cfg)
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if strings.TrimSpace(cfg.Region) == "" {
		cfg.Region = jimengRegion
	}
	return &JimengProvider{cfg: cfg, client: newHTTPClient(cfg.Timeout)}
}

func (p *JimengProvider) Name() string { return "jimeng" }

func (p *JimengProvider) Complete(context.Context, *Request) (*Response, error) {
	return nil, unsupportedCapability(p.Name(), "chat completions")
}

func (p *JimengProvider) Stream(context.Context, *Request) (<-chan *StreamChunk, error) {
	return nil, unsupportedCapability(p.Name(), "chat completions")
}

func (p *JimengProvider) Embed(context.Context, *EmbedRequest) (*EmbedResponse, error) {
	return nil, unsupportedCapability(p.Name(), "embeddings")
}

func (p *JimengProvider) ListModels(context.Context) ([]string, error) {
	return nil, unsupportedTargeted(p.Name(), "model listing")
}

func (p *JimengProvider) Health(context.Context) error {
	return unsupportedTargeted(p.Name(), "health checks")
}

func (p *JimengProvider) SubmitOperation(ctx context.Context, req *OperationRequest) (*Operation, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("operation prompt required")
	}
	reqKey := strings.TrimSpace(req.Model)
	switch req.Kind {
	case OperationImage:
		if reqKey == "" {
			reqKey = jimengImageKey
		}
	case OperationVideo:
		if reqKey == "" {
			return nil, fmt.Errorf("jimeng video requires the official req_key as the model")
		}
	default:
		return nil, unsupportedCapability(p.Name(), "async "+string(req.Kind))
	}
	body := map[string]any{"req_key": reqKey, "prompt": req.Prompt}
	if req.Image != nil && req.Image.URL != "" {
		body["image_urls"] = []string{req.Image.URL}
	} else if req.Image != nil {
		return nil, fmt.Errorf("jimeng reference image requires a url")
	}
	var payload jimengEnvelope
	if err := p.call(ctx, "CVSync2AsyncSubmitTask", body, &payload); err != nil {
		return nil, err
	}
	if payload.Data.TaskID == "" {
		return nil, fmt.Errorf("jimeng task id missing")
	}
	return &Operation{
		ID:       reqKey + "/" + payload.Data.TaskID,
		Provider: p.Name(),
		Model:    reqKey,
		Kind:     req.Kind,
		Status:   OperationQueued,
	}, nil
}

func (p *JimengProvider) GetOperation(ctx context.Context, id string) (*Operation, error) {
	reqKey, taskID, err := splitJimengID(id)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"req_key":  reqKey,
		"task_id":  taskID,
		"req_json": `{"return_url":true}`,
	}
	var payload jimengEnvelope
	if err := p.call(ctx, "CVSync2AsyncGetResult", body, &payload); err != nil {
		return nil, err
	}
	op := payload.asOperation(reqKey + "/" + taskID)
	op.Model = reqKey
	return op, nil
}

func (p *JimengProvider) CancelOperation(context.Context, string) error {
	return unsupportedTargeted(p.Name(), "operation cancel")
}

func (p *JimengProvider) ReadOperation(ctx context.Context, id string) (*OperationAsset, error) {
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

func (p *JimengProvider) call(ctx context.Context, action string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	query := url.Values{}
	query.Set("Action", action)
	query.Set("Version", jimengVersion)
	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + "/?" + canonicalQuery(query)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if err := signVolcengine(httpReq, data, p.cfg.AccessKey, p.cfg.Secret, p.cfg.Region, jimengService, time.Now()); err != nil {
		return err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return networkError(p.Name(), err)
	}
	defer resp.Body.Close()
	raw, err := readResponseBody(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return parseError(resp.StatusCode, raw, resp.Header, p.Name())
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	envelope, ok := out.(*jimengEnvelope)
	if ok && envelope.Code != 0 && envelope.Code != 10000 {
		message := envelope.Message
		if message == "" {
			message = "jimeng request failed"
		}
		return fmt.Errorf("jimeng: %s", message)
	}
	return nil
}

func splitJimengID(id string) (string, string, error) {
	if err := validOperationID(id); err != nil {
		return "", "", err
	}
	reqKey, taskID, ok := strings.Cut(id, "/")
	if !ok || reqKey == "" || taskID == "" || strings.Contains(taskID, "/") {
		return "", "", fmt.Errorf("invalid jimeng operation id")
	}
	return reqKey, taskID, nil
}

type jimengEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		TaskID    string   `json:"task_id"`
		Status    string   `json:"status"`
		ImageURLs []string `json:"image_urls"`
		VideoURL  string   `json:"video_url"`
	} `json:"data"`
}

func (e jimengEnvelope) asOperation(id string) *Operation {
	op := &Operation{ID: id, Provider: "jimeng", Status: mapJimengStatus(e.Data.Status)}
	if e.Code != 0 && e.Code != 10000 {
		op.Status = OperationFailed
		op.Error = e.Message
		return op
	}
	var images []Image
	for _, raw := range e.Data.ImageURLs {
		if raw != "" {
			images = append(images, Image{URL: raw, MediaType: "image/png"})
		}
	}
	var videos []MediaAsset
	if e.Data.VideoURL != "" {
		videos = append(videos, MediaAsset{URL: e.Data.VideoURL, MediaType: "video/mp4"})
		op.Kind = OperationVideo
	}
	if len(images) > 0 && op.Kind == "" {
		op.Kind = OperationImage
	}
	if len(images) > 0 || len(videos) > 0 {
		op.Result = &OperationResult{Images: images, Videos: videos}
	}
	return op
}

func mapJimengStatus(status string) OperationStatus {
	switch strings.ToLower(status) {
	case "", "in_queue":
		return OperationQueued
	case "generating":
		return OperationRunning
	case "done":
		return OperationSucceeded
	case "not_found", "expired":
		return OperationFailed
	default:
		return OperationRunning
	}
}
