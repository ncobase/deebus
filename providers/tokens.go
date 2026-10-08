package providers

import "context"

// TokenCounter asks a provider to count input tokens with its official endpoint.
// Providers without such an endpoint return an error that is eligible for fallback.
type TokenCounter interface {
	CountTokens(ctx context.Context, req *Request) (*TokenCount, error)
}

// TokenCount is the provider-reported size of the input that would be sent.
type TokenCount struct {
	InputTokens     int
	CacheReadTokens int
	Model           string
	Provider        string
}
