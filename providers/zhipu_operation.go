package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (p *OpenAIProvider) submitZhipuVideo(ctx context.Context, req *OperationRequest) (*Operation, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("operation prompt required")
	}
	if req.Kind != OperationVideo {
		return nil, unsupportedCapability(p.Name(), "async "+string(req.Kind))
	}
	body := map[string]any{"model": req.Model, "prompt": req.Prompt}
	if req.Size != "" {
		body["size"] = req.Size
	}
	var payload zhipuTask
	if err := p.callAbsolute(ctx, http.MethodPost, "/videos/generations", body, &payload); err != nil {
		return nil, err
	}
	op := payload.asOperation(p.Name())
	op.Model = req.Model
	op.Kind = OperationVideo
	return op, nil
}

func (p *OpenAIProvider) getZhipu(ctx context.Context, id string) (*Operation, error) {
	if err := validOperationID(id); err != nil {
		return nil, err
	}
	var payload zhipuTask
	if err := p.callAbsolute(ctx, http.MethodGet, "/async-result/"+url.PathEscape(id), nil, &payload); err != nil {
		return nil, err
	}
	if payload.ID == "" {
		payload.ID = id
	}
	op := payload.asOperation(p.Name())
	op.Kind = OperationVideo
	return op, nil
}

type zhipuTask struct {
	ID          string `json:"id"`
	Model       string `json:"model"`
	TaskStatus  string `json:"task_status"`
	Message     string `json:"message"`
	VideoResult []struct {
		URL string `json:"url"`
	} `json:"video_result"`
}

func (z zhipuTask) asOperation(provider string) *Operation {
	op := &Operation{
		ID:       z.ID,
		Provider: provider,
		Model:    z.Model,
		Kind:     OperationVideo,
		Status:   mapZhipuStatus(z.TaskStatus),
		Error:    z.Message,
	}
	videos := make([]MediaAsset, 0, len(z.VideoResult))
	for _, video := range z.VideoResult {
		if video.URL != "" {
			videos = append(videos, MediaAsset{URL: video.URL, MediaType: "video/mp4"})
		}
	}
	if len(videos) > 0 {
		op.Result = &OperationResult{Videos: videos}
	}
	return op
}

func mapZhipuStatus(status string) OperationStatus {
	switch strings.ToUpper(status) {
	case "PROCESSING", "PROCESSING_QUEUE":
		return OperationRunning
	case "SUCCESS":
		return OperationSucceeded
	case "FAIL", "FAILED":
		return OperationFailed
	case "PENDING":
		return OperationQueued
	default:
		return OperationRunning
	}
}
