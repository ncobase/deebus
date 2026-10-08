package deebus

import (
	"context"
	"fmt"
	"strings"

	"github.com/ncobase/deebus/providers"
)

type modelRoute struct {
	provider string
	model    string
}

// GenerateImage creates images. Set Model to "provider/model" to select one
// provider. A bare model name is sent to each provider in the fallback chain.
func (c *Client) GenerateImage(ctx context.Context, req *ImageRequest) (*ImageResponse, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("image prompt required")
	}
	if err := c.rejectMediaLimits(c.config.RequestPolicy.Limits.rejectText("image prompt", len(req.Prompt))); err != nil {
		return nil, err
	}
	attempt := *req
	return dispatch(c, ctx, req.Model, func(p providers.Provider, model string) (*ImageResponse, error) {
		generator, ok := p.(providers.ImageGenerator)
		if !ok {
			return nil, providers.Unsupported(p.Name(), "image generation")
		}
		attempt.Model = model
		return generator.GenerateImage(ctx, &attempt)
	})
}

// SynthesizeSpeech turns text into audio. OpenAI-compatible providers default
// to voice "alloy" and mp3. Gemini defaults to voice "Kore" and wav, and can
// speak multiple turns. A provider that cannot honor the requested format or
// speaker list is skipped.
func (c *Client) SynthesizeSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error) {
	if req == nil || (strings.TrimSpace(req.Input) == "" && len(req.Speakers) == 0) {
		return nil, fmt.Errorf("speech input required")
	}
	textBytes := len(req.Input) + len(req.Instructions)
	for _, turn := range req.Speakers {
		textBytes += len(turn.Text) + len(turn.Style)
	}
	if err := c.rejectMediaLimits(c.config.RequestPolicy.Limits.rejectText("speech input", textBytes)); err != nil {
		return nil, err
	}
	attempt := *req
	attempt.Speakers = append([]SpeechTurn(nil), req.Speakers...)
	return dispatch(c, ctx, req.Model, func(p providers.Provider, model string) (*SpeechResponse, error) {
		synth, ok := p.(providers.SpeechSynthesizer)
		if !ok {
			return nil, providers.Unsupported(p.Name(), "speech synthesis")
		}
		attempt.Model = model
		return synth.SynthesizeSpeech(ctx, &attempt)
	})
}

// EditImage changes or extends source images. The retired variations endpoint
// is not available; describe the variation in the edit prompt instead.
func (c *Client) EditImage(ctx context.Context, req *ImageEditRequest) (*ImageResponse, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("image prompt required")
	}
	if len(req.Images) == 0 {
		return nil, fmt.Errorf("at least one source image required")
	}
	limits := c.config.RequestPolicy.Limits
	if err := c.rejectMediaLimits(limits.rejectText("image prompt", len(req.Prompt))); err != nil {
		return nil, err
	}
	mediaBytes := 0
	for _, image := range req.Images {
		mediaBytes += len(image.Data)
	}
	if req.Mask != nil {
		mediaBytes += len(req.Mask.Data)
	}
	if err := c.rejectMediaLimits(limits.rejectMedia("image edit", mediaBytes)); err != nil {
		return nil, err
	}
	attempt := *req
	attempt.Images = append([]ImageInput(nil), req.Images...)
	if req.Mask != nil {
		mask := *req.Mask
		attempt.Mask = &mask
	}
	return dispatch(c, ctx, req.Model, func(p providers.Provider, model string) (*ImageResponse, error) {
		editor, ok := p.(providers.ImageEditor)
		if !ok {
			return nil, providers.Unsupported(p.Name(), "image editing")
		}
		attempt.Model = model
		return editor.EditImage(ctx, &attempt)
	})
}

// Transcribe turns audio bytes into text.
func (c *Client) Transcribe(ctx context.Context, req *TranscribeRequest) (*TranscribeResponse, error) {
	if req == nil || len(req.Data) == 0 {
		return nil, fmt.Errorf("audio data required")
	}
	limits := c.config.RequestPolicy.Limits
	if err := c.rejectMediaLimits(limits.rejectMedia("audio", len(req.Data))); err != nil {
		return nil, err
	}
	if err := c.rejectMediaLimits(limits.rejectText("transcript prompt", len(req.Prompt))); err != nil {
		return nil, err
	}
	attempt := *req
	return dispatch(c, ctx, req.Model, func(p providers.Provider, model string) (*TranscribeResponse, error) {
		item, ok := p.(providers.Transcriber)
		if !ok {
			return nil, providers.Unsupported(p.Name(), "transcription")
		}
		attempt.Model = model
		return item.Transcribe(ctx, &attempt)
	})
}

// Rerank orders documents by relevance to a query. MaxMessages limits the
// document count and MaxTextBytes limits the combined query and document text.
func (c *Client) Rerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error) {
	if req == nil || strings.TrimSpace(req.Query) == "" {
		return nil, fmt.Errorf("rerank query required")
	}
	if len(req.Documents) == 0 {
		return nil, fmt.Errorf("rerank documents required")
	}
	limits := c.config.RequestPolicy.Limits
	if err := c.rejectMediaLimits(limits.rejectCount("documents", len(req.Documents))); err != nil {
		return nil, err
	}
	textBytes := len(req.Query)
	for _, doc := range req.Documents {
		textBytes += len(doc)
	}
	if err := c.rejectMediaLimits(limits.rejectText("rerank text", textBytes)); err != nil {
		return nil, err
	}
	attempt := *req
	attempt.Documents = append([]string(nil), req.Documents...)
	return dispatch(c, ctx, req.Model, func(p providers.Provider, model string) (*RerankResponse, error) {
		item, ok := p.(providers.Reranker)
		if !ok {
			return nil, providers.Unsupported(p.Name(), "rerank")
		}
		attempt.Model = model
		return item.Rerank(ctx, &attempt)
	})
}

func (c *Client) rejectMediaLimits(err error) error {
	if err == nil {
		return nil
	}
	c.Stats.RecordRequest(false, 0, 0, 0, 0)
	return fmt.Errorf("request policy failed: %w", err)
}

func dispatch[T any](c *Client, ctx context.Context, explicit string, run func(providers.Provider, string) (T, error)) (T, error) {
	var zero T
	routes, err := c.mediaRoutes(explicit)
	if err != nil {
		c.Stats.RecordRequest(false, 0, 0, 0, 0)
		return zero, err
	}
	var lastErr error
	for _, route := range routes {
		p, ok := c.getProvider(route.provider)
		if !ok {
			lastErr = fmt.Errorf("provider %q not configured", route.provider)
			continue
		}
		resp, err := run(p, route.model)
		if err == nil {
			c.Stats.RecordRequest(true, 0, 0, 0, 0)
			return resp, nil
		}
		lastErr = err
		if !providers.IsFallback(err) {
			break
		}
	}
	c.Stats.RecordRequest(false, 0, 0, 0, 0)
	if lastErr == nil {
		lastErr = fmt.Errorf("no provider available")
	}
	return zero, fmt.Errorf("all providers failed: %w", lastErr)
}

func (c *Client) mediaRoutes(explicit string) ([]modelRoute, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" && strings.Contains(explicit, "/") {
		provider, model, err := parseModel(explicit)
		if err != nil {
			return nil, err
		}
		return []modelRoute{{provider: provider, model: model}}, nil
	}
	var out []modelRoute
	var firstErr error
	for _, item := range c.modelChain() {
		provider, model, err := parseModel(item)
		if err != nil {
			firstErr = err
			continue
		}
		if explicit != "" {
			model = explicit
		}
		out = append(out, modelRoute{provider: provider, model: model})
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}
