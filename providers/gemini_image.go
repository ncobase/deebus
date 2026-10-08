package providers

import (
	"context"
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
	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text       string `json:"text"`
					InlineData *struct {
						MIMEType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := p.postGemini(ctx, fmt.Sprintf("/v1beta/models/%s:generateContent", req.Model), body, &payload); err != nil {
		return nil, err
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
	return &ImageResponse{Images: images, Model: req.Model, Provider: p.Name()}, nil
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
