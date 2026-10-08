package deebus

import (
	"context"
	"fmt"

	"github.com/ncobase/deebus/providers"
)

// CountTokens asks the provider chain to count input tokens using each
// provider's official endpoint. OpenAI-compatible providers, including Qwen,
// have no such endpoint and are skipped. Anthropic uses
// /v1/messages/count_tokens, Gemini uses countTokens, and Cohere uses
// /v1/tokenize on the joined message text.
func (c *Client) CountTokens(ctx context.Context, req *Request) (*TokenCount, error) {
	if req == nil {
		return nil, fmt.Errorf("request required")
	}
	if err := c.config.RequestPolicy.Limits.Validate(req); err != nil {
		c.Stats.RecordRequest(false, 0, 0, 0, 0)
		return nil, fmt.Errorf("request policy failed: %w", err)
	}
	explicit := req.Model
	return dispatch(c, ctx, explicit, func(p providers.Provider, model string) (*TokenCount, error) {
		counter, ok := p.(providers.TokenCounter)
		if !ok {
			return nil, providers.Unsupported(p.Name(), "token counting")
		}
		attempt := *req
		attempt.Model = model
		return counter.CountTokens(ctx, &attempt)
	})
}
