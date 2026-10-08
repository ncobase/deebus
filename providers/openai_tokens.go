package providers

import "context"

// CountTokens reports that OpenAI and its compatible wire protocol have no
// official input-token counting endpoint. The error is safe to fall back from.
func (p *OpenAIProvider) CountTokens(context.Context, *Request) (*TokenCount, error) {
	return nil, unsupportedCapability(p.Name(), "token counting")
}
