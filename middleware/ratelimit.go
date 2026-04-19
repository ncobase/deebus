package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/ncobase/deebus/providers"
)

// RateLimitMiddleware implements a continuous token bucket rate limiter.
// Tokens refill proportionally to elapsed time, not in discrete bursts.
// If requestsPerSecond is <= 0, the middleware is a no-op.
type RateLimitMiddleware struct {
	provider  providers.Provider
	capacity  float64
	refillRPS float64
	tokens    float64
	lastCheck time.Time
	mu        sync.Mutex
}

// NewRateLimit wraps p with a rate limiter capped at requestsPerSecond req/s.
func NewRateLimit(p providers.Provider, requestsPerSecond int) *RateLimitMiddleware {
	cap := float64(requestsPerSecond)
	return &RateLimitMiddleware{
		provider:  p,
		capacity:  cap,
		refillRPS: cap,
		tokens:    cap, // start full
		lastCheck: time.Now(),
	}
}

func (r *RateLimitMiddleware) Name() string { return r.provider.Name() }

func (r *RateLimitMiddleware) Complete(ctx context.Context, req *providers.Request) (*providers.Response, error) {
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return r.provider.Complete(ctx, req)
}

func (r *RateLimitMiddleware) Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error) {
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return r.provider.Stream(ctx, req)
}

func (r *RateLimitMiddleware) Embed(ctx context.Context, req *providers.EmbedRequest) (*providers.EmbedResponse, error) {
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return r.provider.Embed(ctx, req)
}

func (r *RateLimitMiddleware) Health(ctx context.Context) error {
	return r.provider.Health(ctx)
}

func (r *RateLimitMiddleware) CreateCache(ctx context.Context, req *providers.CreateCacheRequest) (*providers.Cache, error) {
	cp, err := cacheProvider(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return cp.CreateCache(ctx, req)
}

func (r *RateLimitMiddleware) GetCache(ctx context.Context, name string) (*providers.Cache, error) {
	cp, err := cacheProvider(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return cp.GetCache(ctx, name)
}

func (r *RateLimitMiddleware) ListCaches(ctx context.Context, req *providers.ListCachesRequest) (*providers.ListCachesResponse, error) {
	cp, err := cacheProvider(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return cp.ListCaches(ctx, req)
}

func (r *RateLimitMiddleware) UpdateCache(ctx context.Context, req *providers.UpdateCacheRequest) (*providers.Cache, error) {
	cp, err := cacheProvider(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return cp.UpdateCache(ctx, req)
}

func (r *RateLimitMiddleware) DeleteCache(ctx context.Context, name string) error {
	cp, err := cacheProvider(r.provider)
	if err != nil {
		return err
	}
	if err := r.acquire(ctx); err != nil {
		return err
	}
	return cp.DeleteCache(ctx, name)
}

// acquire blocks until a token is available or ctx is cancelled.
func (r *RateLimitMiddleware) acquire(ctx context.Context) error {
	if r.capacity <= 0 {
		return nil
	}

	for {
		wait := r.tryAcquire()
		if wait == 0 {
			return nil
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// tryAcquire attempts to consume a token. Returns 0 on success or the duration
// to wait before trying again.
func (r *RateLimitMiddleware) tryAcquire() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(r.lastCheck).Seconds()
	r.lastCheck = now

	// Refill proportionally to elapsed time
	r.tokens += elapsed * r.refillRPS
	if r.tokens > r.capacity {
		r.tokens = r.capacity
	}

	if r.tokens >= 1.0 {
		r.tokens--
		return 0
	}

	// Return how long until the next token is available
	return time.Duration((1.0 - r.tokens) / r.refillRPS * float64(time.Second))
}
