package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (p *OpenAIProvider) submitDoubaoVideo(ctx context.Context, req *OperationRequest) (*Operation, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("operation prompt required")
	}
	if req.Kind != OperationVideo {
		return nil, unsupportedCapability(p.Name(), "async "+string(req.Kind))
	}
	content := []any{map[string]string{"type": "text", "text": req.Prompt}}
	if req.Image != nil && req.Image.URL != "" {
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": req.Image.URL},
		})
	} else if req.Image != nil {
		return nil, fmt.Errorf("doubao video reference requires an image url")
	}
	body := map[string]any{"model": req.Model, "content": content}
	var payload struct {
		ID string `json:"id"`
	}
	if err := p.callAbsolute(ctx, http.MethodPost, "/contents/generations/tasks", body, &payload); err != nil {
		return nil, err
	}
	if payload.ID == "" {
		return nil, fmt.Errorf("doubao task id missing")
	}
	return &Operation{ID: payload.ID, Provider: p.Name(), Model: req.Model, Kind: OperationVideo, Status: OperationQueued}, nil
}

func (p *OpenAIProvider) getDoubaoVideo(ctx context.Context, id string) (*Operation, error) {
	if err := validOperationID(id); err != nil {
		return nil, err
	}
	var payload struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Status  string `json:"status"`
		Content struct {
			VideoURL string `json:"video_url"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := p.callAbsolute(ctx, http.MethodGet, "/contents/generations/tasks/"+url.PathEscape(id), nil, &payload); err != nil {
		return nil, err
	}
	if payload.ID == "" {
		payload.ID = id
	}
	op := &Operation{
		ID:       payload.ID,
		Provider: p.Name(),
		Model:    payload.Model,
		Kind:     OperationVideo,
		Status:   mapDoubaoStatus(payload.Status),
	}
	if payload.Error != nil {
		op.Error = payload.Error.Message
	}
	if payload.Content.VideoURL != "" {
		op.Result = &OperationResult{Videos: []MediaAsset{{URL: payload.Content.VideoURL, MediaType: "video/mp4"}}}
	}
	return op, nil
}

func mapDoubaoStatus(status string) OperationStatus {
	switch strings.ToLower(status) {
	case "queued":
		return OperationQueued
	case "running":
		return OperationRunning
	case "succeeded":
		return OperationSucceeded
	case "failed":
		return OperationFailed
	case "cancelled", "canceled":
		return OperationCancelled
	default:
		return OperationRunning
	}
}
