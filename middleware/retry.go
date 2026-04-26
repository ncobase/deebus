package middleware

import (
	"context"
	"math"
	"math/rand"
	"time"

	"github.com/ncobase/deebus/providers"
)

// RetryMiddleware retries failed requests with exponential backoff and equal
// jitter. It only retries errors that are explicitly marked retryable
// (e.g. 429, 5xx, network). Non-retryable errors (400, 401, 403) are returned
// immediately. Retry-After headers on 429 responses are honoured.
type RetryMiddleware struct {
	provider   providers.Provider
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
}

// NewRetry wraps p with retry logic using up to maxRetries additional attempts.
func NewRetry(p providers.Provider, maxRetries int) *RetryMiddleware {
	return &RetryMiddleware{
		provider:   p,
		maxRetries: maxRetries,
		baseDelay:  500 * time.Millisecond,
		maxDelay:   30 * time.Second,
	}
}

func (m *RetryMiddleware) Name() string { return m.provider.Name() }

func (m *RetryMiddleware) Complete(ctx context.Context, req *providers.Request) (*providers.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		resp, err := m.provider.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !providers.IsRetryable(err) {
			return nil, err
		}
		if attempt < m.maxRetries {
			if err := sleepWithContext(ctx, m.backoff(attempt, retryAfter(err))); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func (m *RetryMiddleware) Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error) {
	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		ch, err := m.provider.Stream(ctx, req)
		if err == nil {
			return ch, nil
		}
		lastErr = err
		if !providers.IsRetryable(err) {
			return nil, err
		}
		if attempt < m.maxRetries {
			if err := sleepWithContext(ctx, m.backoff(attempt, retryAfter(err))); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func (m *RetryMiddleware) Embed(ctx context.Context, req *providers.EmbedRequest) (*providers.EmbedResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		resp, err := m.provider.Embed(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !providers.IsRetryable(err) {
			return nil, err
		}
		if attempt < m.maxRetries {
			if err := sleepWithContext(ctx, m.backoff(attempt, retryAfter(err))); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func (m *RetryMiddleware) Health(ctx context.Context) error {
	return m.provider.Health(ctx)
}

func (m *RetryMiddleware) ListModels(ctx context.Context) ([]string, error) {
	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		resp, err := m.provider.ListModels(ctx)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !providers.IsRetryable(err) {
			return nil, err
		}
		if attempt < m.maxRetries {
			if err := sleepWithContext(ctx, m.backoff(attempt, retryAfter(err))); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func (m *RetryMiddleware) CreateCache(ctx context.Context, req *providers.CreateCacheRequest) (*providers.Cache, error) {
	cp, err := cacheProvider(m.provider)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		resp, err := cp.CreateCache(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !providers.IsRetryable(err) {
			return nil, err
		}
		if attempt < m.maxRetries {
			if err := sleepWithContext(ctx, m.backoff(attempt, retryAfter(err))); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func (m *RetryMiddleware) GetCache(ctx context.Context, name string) (*providers.Cache, error) {
	cp, err := cacheProvider(m.provider)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		resp, err := cp.GetCache(ctx, name)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !providers.IsRetryable(err) {
			return nil, err
		}
		if attempt < m.maxRetries {
			if err := sleepWithContext(ctx, m.backoff(attempt, retryAfter(err))); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func (m *RetryMiddleware) ListCaches(ctx context.Context, req *providers.ListCachesRequest) (*providers.ListCachesResponse, error) {
	cp, err := cacheProvider(m.provider)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		resp, err := cp.ListCaches(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !providers.IsRetryable(err) {
			return nil, err
		}
		if attempt < m.maxRetries {
			if err := sleepWithContext(ctx, m.backoff(attempt, retryAfter(err))); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func (m *RetryMiddleware) UpdateCache(ctx context.Context, req *providers.UpdateCacheRequest) (*providers.Cache, error) {
	cp, err := cacheProvider(m.provider)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		resp, err := cp.UpdateCache(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !providers.IsRetryable(err) {
			return nil, err
		}
		if attempt < m.maxRetries {
			if err := sleepWithContext(ctx, m.backoff(attempt, retryAfter(err))); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func (m *RetryMiddleware) DeleteCache(ctx context.Context, name string) error {
	cp, err := cacheProvider(m.provider)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		err := cp.DeleteCache(ctx, name)
		if err == nil {
			return nil
		}
		lastErr = err
		if !providers.IsRetryable(err) {
			return err
		}
		if attempt < m.maxRetries {
			if err := sleepWithContext(ctx, m.backoff(attempt, retryAfter(err))); err != nil {
				return err
			}
		}
	}
	return lastErr
}

// backoff computes the wait duration for a given attempt number using equal
// jitter: delay = cap/2 + random(0, cap/2), where cap = base * 2^attempt.
// If the server provided a Retry-After hint, that takes precedence.
func (m *RetryMiddleware) backoff(attempt int, hint time.Duration) time.Duration {
	if hint > 0 {
		return hint
	}
	exp := float64(m.baseDelay) * math.Pow(2, float64(attempt))
	cap := time.Duration(exp)
	if cap > m.maxDelay {
		cap = m.maxDelay
	}
	half := cap / 2
	// Equal jitter: deterministic half + random half
	if half <= 0 {
		return cap
	}
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

// retryAfter extracts the RetryAfter hint from a ProviderError, if present.
func retryAfter(err error) time.Duration {
	pe := unwrap(err)
	if pe == nil {
		return 0
	}
	return pe.RetryAfter
}

func unwrap(err error) *providers.ProviderError {
	for err != nil {
		if pe, ok := err.(*providers.ProviderError); ok {
			return pe
		}
		type uw interface{ Unwrap() error }
		u, ok := err.(uw)
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return nil
}

// sleepWithContext waits for d or until ctx is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
