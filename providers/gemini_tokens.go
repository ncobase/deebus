package providers

import (
	"context"
	"fmt"
)

// CountTokens calls the official Gemini models.countTokens method.
// The body is the same input the generateContent call would send.
func (p *GeminiProvider) CountTokens(ctx context.Context, req *Request) (*TokenCount, error) {
	if req == nil {
		return nil, fmt.Errorf("request required")
	}
	if req.Model == "" {
		return nil, fmt.Errorf("gemini model required")
	}
	var payload struct {
		TotalTokens             int `json:"totalTokens"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
	}
	path := "/v1beta/models/" + req.Model + ":countTokens"
	if err := p.postGemini(ctx, path, geminiCountBody(req), &payload); err != nil {
		return nil, err
	}
	return &TokenCount{
		InputTokens:     payload.TotalTokens,
		CacheReadTokens: payload.CachedContentTokenCount,
		Model:           req.Model,
		Provider:        p.Name(),
	}, nil
}

// geminiCountBody is the input portion of a generateContent request.
// Output controls such as maxOutputTokens are not part of countTokens.
func geminiCountBody(req *Request) map[string]any {
	system, msgs := ExtractSystemMessage(req.Messages)
	body := map[string]any{"contents": ConvertToGeminiFormat(msgs)}
	if system != "" {
		body["systemInstruction"] = map[string]any{
			"role":  "system",
			"parts": []map[string]any{{"text": system}},
		}
	}
	if req.Cache != nil && req.Cache.CachedContent != "" {
		body["cachedContent"] = req.Cache.CachedContent
	}
	if len(req.Tools) > 0 {
		body["tools"] = ConvertToolsToGemini(req.Tools)
		if req.ToolChoice != "" {
			body["toolConfig"] = GeminiToolConfig(req.ToolChoice)
		}
	}
	return body
}
