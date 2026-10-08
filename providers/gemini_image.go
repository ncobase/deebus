package providers

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

func (p *GeminiProvider) GenerateImage(ctx context.Context, req *ImageRequest) (*ImageResponse, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("image prompt required")
	}
	config := map[string]any{
		"responseModalities": []string{"TEXT", "IMAGE"},
	}
	if ratio := geminiAspectRatio(req.Size); ratio != "" {
		config["imageConfig"] = map[string]any{"aspectRatio": ratio}
	}
	body := map[string]any{
		"contents": []map[string]any{{
			"role":  "user",
			"parts": []map[string]any{{"text": req.Prompt}},
		}},
		"generationConfig": config,
	}
	payload, err := p.geminiContent(ctx, req.Model, body)
	if err != nil {
		return nil, err
	}
	return &ImageResponse{Images: imagesFromGemini(payload), Model: req.Model, Provider: p.Name()}, nil
}

func (p *GeminiProvider) EditImage(ctx context.Context, req *ImageEditRequest) (*ImageResponse, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("image prompt required")
	}
	if len(req.Images) == 0 {
		return nil, fmt.Errorf("at least one source image required")
	}
	parts := make([]map[string]any, 0, len(req.Images)+2)
	for i, image := range req.Images {
		part, err := geminiImagePart(image)
		if err != nil {
			return nil, fmt.Errorf("image %d: %w", i, err)
		}
		parts = append(parts, part)
	}
	if req.Mask != nil {
		part, err := geminiImagePart(*req.Mask)
		if err != nil {
			return nil, fmt.Errorf("mask: %w", err)
		}
		parts = append(parts, part)
		parts = append(parts, map[string]any{"text": "The previous image is an edit mask. " + req.Prompt})
	} else {
		parts = append(parts, map[string]any{"text": req.Prompt})
	}
	config := map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}}
	if ratio := geminiAspectRatio(req.Size); ratio != "" {
		config["imageConfig"] = map[string]any{"aspectRatio": ratio}
	}
	payload, err := p.geminiContent(ctx, req.Model, map[string]any{
		"contents":         []map[string]any{{"role": "user", "parts": parts}},
		"generationConfig": config,
	})
	if err != nil {
		return nil, err
	}
	return &ImageResponse{Images: imagesFromGemini(payload), Model: req.Model, Provider: p.Name()}, nil
}

func imagesFromGemini(payload *geminiContentResponse) []Image {
	if payload == nil {
		return nil
	}
	images := make([]Image, 0, 1)
	var revised string
	for _, candidate := range payload.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" && revised == "" {
				revised = part.Text
			}
			if part.InlineData == nil || part.InlineData.Data == "" {
				continue
			}
			mediaType := part.InlineData.MIMEType
			if mediaType == "" {
				mediaType = "image/png"
			}
			images = append(images, Image{
				B64JSON:       part.InlineData.Data,
				RevisedPrompt: revised,
				MediaType:     mediaType,
			})
		}
	}
	return images
}

func geminiImagePart(image ImageInput) (map[string]any, error) {
	if image.URL != "" || image.FileID != "" {
		return nil, unsupportedCapability("gemini", "remote image references")
	}
	if len(image.Data) == 0 {
		return nil, fmt.Errorf("image data required")
	}
	mediaType := image.MediaType
	if mediaType == "" {
		mediaType = "image/png"
	}
	return map[string]any{"inlineData": map[string]string{
		"mimeType": mediaType,
		"data":     base64.StdEncoding.EncodeToString(image.Data),
	}}, nil
}

func geminiAspectRatio(size string) string {
	switch strings.TrimSpace(size) {
	case "", "auto":
		return ""
	case "1024x1024", "1:1":
		return "1:1"
	case "1536x1024", "16:9":
		return "16:9"
	case "1024x1536", "9:16":
		return "9:16"
	case "4:3":
		return "4:3"
	case "3:4":
		return "3:4"
	default:
		if strings.Contains(size, ":") {
			return size
		}
		return ""
	}
}
