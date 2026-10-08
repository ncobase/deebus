package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (p *OpenAIProvider) submitDashScope(ctx context.Context, req *OperationRequest) (*Operation, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("operation prompt required")
	}
	var path string
	input := map[string]any{"prompt": req.Prompt}
	params := map[string]any{}
	switch req.Kind {
	case OperationImage:
		path = "/api/v1/services/aigc/text2image/image-synthesis"
		if req.Size != "" {
			params["size"] = strings.ReplaceAll(req.Size, "x", "*")
		}
		if req.N > 0 {
			params["n"] = req.N
		}
	case OperationVideo:
		path = "/api/v1/services/aigc/video-generation/video-synthesis"
		if req.Size != "" {
			params["size"] = strings.ReplaceAll(req.Size, "x", "*")
		}
		if req.AspectRatio != "" {
			params["ratio"] = req.AspectRatio
		}
		if req.Resolution != "" {
			params["resolution"] = req.Resolution
		}
		if req.DurationSeconds > 0 {
			params["duration"] = req.DurationSeconds
		}
		if req.NegativePrompt != "" {
			input["negative_prompt"] = req.NegativePrompt
		}
		if req.Image != nil {
			if req.Image.URL == "" {
				return nil, fmt.Errorf("dashscope video reference requires an image url")
			}
			input["img_url"] = req.Image.URL
		}
	default:
		return nil, unsupportedCapability(p.Name(), "async "+string(req.Kind))
	}
	body := map[string]any{"model": req.Model, "input": input}
	if len(params) > 0 {
		body["parameters"] = params
	}
	payload, err := p.dashScope(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	op := payload.asOperation(p.Name(), req.Kind)
	op.Model = req.Model
	return op, nil
}

func (p *OpenAIProvider) getDashScope(ctx context.Context, id string) (*Operation, error) {
	if err := validOperationID(id); err != nil {
		return nil, err
	}
	payload, err := p.dashScope(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	return payload.asOperation(p.Name(), ""), nil
}

type dashScopeTask struct {
	Output struct {
		TaskID     string `json:"task_id"`
		TaskStatus string `json:"task_status"`
		Message    string `json:"message"`
		Code       string `json:"code"`
		VideoURL   string `json:"video_url"`
		Results    []struct {
			URL     string `json:"url"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"results"`
	} `json:"output"`
	Message string `json:"message"`
}

func (d dashScopeTask) asOperation(provider string, kind OperationKind) *Operation {
	op := &Operation{
		ID:       d.Output.TaskID,
		Provider: provider,
		Kind:     kind,
		Status:   mapDashScopeStatus(d.Output.TaskStatus),
		Error:    firstNonEmpty(d.Output.Message, d.Message),
	}
	if d.Output.Code != "" && op.Error == "" {
		op.Error = d.Output.Code
	}
	var images []Image
	for _, result := range d.Output.Results {
		if result.URL == "" {
			if result.Message != "" && op.Error == "" {
				op.Error = result.Message
			}
			continue
		}
		images = append(images, Image{URL: result.URL, MediaType: "image/png"})
	}
	var videos []MediaAsset
	if d.Output.VideoURL != "" {
		videos = append(videos, MediaAsset{URL: d.Output.VideoURL, MediaType: "video/mp4"})
		if op.Kind == "" {
			op.Kind = OperationVideo
		}
	}
	if len(images) > 0 && op.Kind == "" {
		op.Kind = OperationImage
	}
	if len(images) > 0 || len(videos) > 0 {
		op.Result = &OperationResult{Images: images, Videos: videos}
	}
	return op
}

func (p *OpenAIProvider) dashScope(ctx context.Context, method, path string, body any) (*dashScopeTask, error) {
	endpoint, err := dashScopeEndpoint(p.cfg.BaseURL, path)
	if err != nil {
		return nil, err
	}
	var payload dashScopeTask
	err = p.callPrepared(ctx, method, endpoint, body, &payload, func(req *http.Request) {
		if method == http.MethodPost {
			req.Header.Set("X-DashScope-Async", "enable")
		}
	})
	if err != nil {
		return nil, err
	}
	return &payload, nil
}

func dashScopeEndpoint(baseURL, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid dashscope base URL")
	}
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func mapDashScopeStatus(status string) OperationStatus {
	switch strings.ToUpper(status) {
	case "PENDING":
		return OperationQueued
	case "RUNNING":
		return OperationRunning
	case "SUCCEEDED":
		return OperationSucceeded
	case "FAILED":
		return OperationFailed
	case "CANCELED", "CANCELLED":
		return OperationCancelled
	case "UNKNOWN":
		return OperationFailed
	default:
		return OperationRunning
	}
}

func (p *OpenAIProvider) callPrepared(ctx context.Context, method, endpoint string, body, out any, prepare func(*http.Request)) error {
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if prepare != nil {
		prepare(req)
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}
	p.setAuth(req, creds)
	return doProviderJSONRequest(ctx, p.client, req, p.Name(), out)
}
