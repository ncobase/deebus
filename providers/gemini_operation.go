package providers

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// SubmitOperation starts Veo through POST /v1beta/models/{model}:predictLongRunning.
func (p *GeminiProvider) SubmitOperation(ctx context.Context, req *OperationRequest) (*Operation, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("operation prompt required")
	}
	if req.Kind != OperationVideo {
		return nil, unsupportedCapability(p.Name(), "async "+string(req.Kind))
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("gemini model required")
	}
	instance := map[string]any{"prompt": req.Prompt}
	if req.Image != nil {
		image, err := geminiVideoImage(*req.Image)
		if err != nil {
			return nil, err
		}
		instance["image"] = image
	}
	body := map[string]any{"instances": []any{instance}}
	params := map[string]any{}
	if req.AspectRatio != "" {
		params["aspectRatio"] = req.AspectRatio
	}
	if req.Resolution != "" {
		params["resolution"] = req.Resolution
	}
	if req.DurationSeconds > 0 {
		params["durationSeconds"] = req.DurationSeconds
	}
	if len(params) > 0 {
		body["parameters"] = params
	}
	var payload struct {
		Name string `json:"name"`
	}
	path := "/v1beta/models/" + req.Model + ":predictLongRunning"
	if err := p.postGemini(ctx, path, body, &payload); err != nil {
		return nil, err
	}
	if payload.Name == "" {
		return nil, fmt.Errorf("gemini operation id missing")
	}
	return &Operation{
		ID:       payload.Name,
		Provider: p.Name(),
		Model:    req.Model,
		Kind:     OperationVideo,
		Status:   OperationRunning,
	}, nil
}

// GetOperation calls GET /v1beta/{operation}.
func (p *GeminiProvider) GetOperation(ctx context.Context, id string) (*Operation, error) {
	if err := validOperationID(id); err != nil {
		return nil, err
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	endpoint, err := p.geminiURL(creds, "/v1beta/"+strings.TrimLeft(id, "/"), "")
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq, creds)
	var payload geminiOperation
	if err := doProviderJSONRequest(ctx, p.client, httpReq, p.Name(), &payload); err != nil {
		return nil, err
	}
	return payload.asOperation(p.Name(), id), nil
}

// CancelOperation calls POST /v1beta/{operation}:cancel.
func (p *GeminiProvider) CancelOperation(ctx context.Context, id string) error {
	if err := validOperationID(id); err != nil {
		return err
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}
	endpoint, err := p.geminiURL(creds, "/v1beta/"+strings.TrimLeft(id, "/")+":cancel", "")
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq, creds)
	return doProviderJSONRequest(ctx, p.client, httpReq, p.Name(), nil)
}

// ReadOperation downloads the finished video when its URL is on the provider host.
func (p *GeminiProvider) ReadOperation(ctx context.Context, id string) (*OperationAsset, error) {
	op, err := p.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	if op.Status != OperationSucceeded || op.Result == nil || len(op.Result.Videos) == 0 || op.Result.Videos[0].URL == "" {
		return nil, fmt.Errorf("gemini operation has no video")
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, op.Result.Videos[0].URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if sameHost(p.cfg.BaseURL, op.Result.Videos[0].URL) {
		p.setHeaders(httpReq, creds)
	}
	return readAsset(p.client, httpReq, op.Result.Videos[0].MediaType)
}

type geminiOperation struct {
	Done  bool `json:"done"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Response struct {
		GenerateVideoResponse struct {
			GeneratedSamples []struct {
				Video struct {
					URI      string `json:"uri"`
					MIMEType string `json:"mimeType"`
				} `json:"video"`
			} `json:"generatedSamples"`
		} `json:"generateVideoResponse"`
	} `json:"response"`
}

func (g geminiOperation) asOperation(provider, id string) *Operation {
	op := &Operation{ID: id, Provider: provider, Kind: OperationVideo, Status: OperationRunning}
	if g.Error != nil && g.Error.Message != "" {
		op.Status = OperationFailed
		op.Error = g.Error.Message
		return op
	}
	if !g.Done {
		return op
	}
	op.Status = OperationSucceeded
	videos := make([]MediaAsset, 0, len(g.Response.GenerateVideoResponse.GeneratedSamples))
	for _, sample := range g.Response.GenerateVideoResponse.GeneratedSamples {
		if sample.Video.URI == "" {
			continue
		}
		mediaType := sample.Video.MIMEType
		if mediaType == "" {
			mediaType = "video/mp4"
		}
		videos = append(videos, MediaAsset{URL: sample.Video.URI, MediaType: mediaType})
	}
	if len(videos) > 0 {
		op.Result = &OperationResult{Videos: videos}
	}
	return op
}

func geminiVideoImage(image ImageInput) (map[string]any, error) {
	if image.URL != "" {
		return map[string]any{"uri": image.URL, "mimeType": imageMediaType(image)}, nil
	}
	if len(image.Data) == 0 {
		return nil, fmt.Errorf("reference image data or url required")
	}
	return map[string]any{
		"bytesBase64Encoded": base64.StdEncoding.EncodeToString(image.Data),
		"mimeType":           imageMediaType(image),
	}, nil
}

func imageMediaType(image ImageInput) string {
	if image.MediaType != "" {
		return image.MediaType
	}
	return "image/png"
}

func readAsset(client *http.Client, req *http.Request, mediaType string) (*OperationAsset, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, networkError("asset", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp.StatusCode, readErrorBody(resp.Body), resp.Header, "asset")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxOperationAssetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read asset: %w", err)
	}
	if int64(len(data)) > maxOperationAssetBytes {
		return nil, fmt.Errorf("operation asset exceeds %d bytes", maxOperationAssetBytes)
	}
	if headerType := resp.Header.Get("Content-Type"); headerType != "" {
		mediaType = headerType
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return &OperationAsset{Data: data, MediaType: mediaType}, nil
}
