package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
)

func (p *OpenAIProvider) GenerateImage(ctx context.Context, req *ImageRequest) (*ImageResponse, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("image prompt required")
	}
	body := map[string]any{
		"model":  req.Model,
		"prompt": req.Prompt,
	}
	if req.N > 0 {
		body["n"] = req.N
	}
	if req.Size != "" {
		body["size"] = req.Size
	}
	if req.Quality != "" {
		body["quality"] = req.Quality
	}
	if req.Style != "" {
		body["style"] = req.Style
	}
	if req.ResponseFormat != "" {
		body["response_format"] = req.ResponseFormat
	}
	if req.UserID != "" {
		body["user"] = req.UserID
	}
	var payload struct {
		Data []struct {
			B64JSON       string `json:"b64_json"`
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := p.postJSON(ctx, req.Model, "/v1/images/generations", body, &payload); err != nil {
		return nil, err
	}
	images := make([]Image, 0, len(payload.Data))
	for _, item := range payload.Data {
		images = append(images, Image{
			B64JSON:       item.B64JSON,
			URL:           item.URL,
			RevisedPrompt: item.RevisedPrompt,
			MediaType:     "image/png",
		})
	}
	return &ImageResponse{Images: images, Model: req.Model, Provider: p.Name()}, nil
}

func (p *OpenAIProvider) SynthesizeSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error) {
	if req == nil || strings.TrimSpace(req.Input) == "" {
		return nil, fmt.Errorf("speech input required")
	}
	format := req.Format
	if format == "" {
		format = "mp3"
	}
	voice := req.Voice
	if voice == "" {
		voice = "alloy"
	}
	body := map[string]any{
		"model":           req.Model,
		"input":           req.Input,
		"voice":           voice,
		"response_format": format,
	}
	if req.Speed > 0 {
		body["speed"] = req.Speed
	}
	audio, err := p.postBytes(ctx, req.Model, "/v1/audio/speech", body)
	if err != nil {
		return nil, err
	}
	return &SpeechResponse{
		Audio:     audio,
		MediaType: speechMediaType(format),
		Model:     req.Model,
		Provider:  p.Name(),
	}, nil
}

func (p *OpenAIProvider) Transcribe(ctx context.Context, req *TranscribeRequest) (*TranscribeResponse, error) {
	if req == nil || len(req.Data) == 0 {
		return nil, fmt.Errorf("audio data required")
	}
	format := req.Format
	if format == "" {
		format = "json"
	}
	fileName := req.FileName
	if fileName == "" {
		fileName = "audio.mp3"
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return nil, fmt.Errorf("create audio part: %w", err)
	}
	if _, err := part.Write(req.Data); err != nil {
		return nil, fmt.Errorf("write audio part: %w", err)
	}
	fields := map[string]string{
		"model":           req.Model,
		"response_format": format,
	}
	if req.Language != "" {
		fields["language"] = req.Language
	}
	if req.Prompt != "" {
		fields["prompt"] = req.Prompt
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("write field %s: %w", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	endpoint, err := p.openAIEndpoint(req.Model, "/v1/audio/transcriptions")
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

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	defer resp.Body.Close()
	raw, err := readResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp.StatusCode, raw, resp.Header, p.Name())
	}
	text := strings.TrimSpace(string(raw))
	if format != "text" {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("decode transcript: %w", err)
		}
		text = payload.Text
	}
	return &TranscribeResponse{Text: text, Model: req.Model, Provider: p.Name()}, nil
}

func (p *OpenAIProvider) postJSON(ctx context.Context, model, path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	endpoint, err := p.openAIEndpoint(model, path)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}
	p.setHeaders(httpReq, creds)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return networkError(p.Name(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return parseError(resp.StatusCode, readErrorBody(resp.Body), resp.Header, p.Name())
	}
	raw, err := readResponseBody(resp.Body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (p *OpenAIProvider) postBytes(ctx context.Context, model, path string, body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	endpoint, err := p.openAIEndpoint(model, path)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	p.setHeaders(httpReq, creds)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	defer resp.Body.Close()
	raw, err := readResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp.StatusCode, raw, resp.Header, p.Name())
	}
	return raw, nil
}

func speechMediaType(format string) string {
	switch strings.ToLower(format) {
	case "wav":
		return "audio/wav"
	case "opus":
		return "audio/opus"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	case "pcm":
		return "audio/pcm"
	default:
		return "audio/mpeg"
	}
}
