package middleware

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ncobase/deebus/internal/circuit"
	"github.com/ncobase/deebus/providers"
)

// mockProvider is a test double for providers.Provider. Each call invokes
// the registered hook; if the hook is nil, it returns a zero-value success.
type mockProvider struct {
	name       string
	completeFn func(context.Context, *providers.Request) (*providers.Response, error)
	streamFn   func(context.Context, *providers.Request) (<-chan *providers.StreamChunk, error)
	embedFn    func(context.Context, *providers.EmbedRequest) (*providers.EmbedResponse, error)
	callCount  atomic.Int64
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Complete(ctx context.Context, req *providers.Request) (*providers.Response, error) {
	m.callCount.Add(1)
	if m.completeFn != nil {
		return m.completeFn(ctx, req)
	}
	return &providers.Response{Content: "ok"}, nil
}

func (m *mockProvider) Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error) {
	m.callCount.Add(1)
	if m.streamFn != nil {
		return m.streamFn(ctx, req)
	}
	ch := make(chan *providers.StreamChunk, 1)
	ch <- &providers.StreamChunk{Content: "ok", Done: true}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Embed(ctx context.Context, req *providers.EmbedRequest) (*providers.EmbedResponse, error) {
	m.callCount.Add(1)
	if m.embedFn != nil {
		return m.embedFn(ctx, req)
	}
	return &providers.EmbedResponse{Embeddings: [][]float64{{1, 2, 3}}}, nil
}

func (m *mockProvider) Health(ctx context.Context) error { return nil }

type mockCacheProvider struct {
	*mockProvider
	createCacheFn func(context.Context, *providers.CreateCacheRequest) (*providers.Cache, error)
	getCacheFn    func(context.Context, string) (*providers.Cache, error)
	listCachesFn  func(context.Context, *providers.ListCachesRequest) (*providers.ListCachesResponse, error)
	updateCacheFn func(context.Context, *providers.UpdateCacheRequest) (*providers.Cache, error)
	deleteCacheFn func(context.Context, string) error
	createCalls   atomic.Int64
	getCalls      atomic.Int64
	listCalls     atomic.Int64
	updateCalls   atomic.Int64
	deleteCalls   atomic.Int64
}

func newMockCacheProvider() *mockCacheProvider {
	return &mockCacheProvider{mockProvider: &mockProvider{name: "mock"}}
}

func (m *mockCacheProvider) CreateCache(ctx context.Context, req *providers.CreateCacheRequest) (*providers.Cache, error) {
	m.createCalls.Add(1)
	if m.createCacheFn != nil {
		return m.createCacheFn(ctx, req)
	}
	return &providers.Cache{Name: "cachedContents/demo", Provider: m.Name(), Model: req.Model}, nil
}

func (m *mockCacheProvider) GetCache(ctx context.Context, name string) (*providers.Cache, error) {
	m.getCalls.Add(1)
	if m.getCacheFn != nil {
		return m.getCacheFn(ctx, name)
	}
	return &providers.Cache{Name: name, Provider: m.Name()}, nil
}

func (m *mockCacheProvider) ListCaches(ctx context.Context, req *providers.ListCachesRequest) (*providers.ListCachesResponse, error) {
	m.listCalls.Add(1)
	if m.listCachesFn != nil {
		return m.listCachesFn(ctx, req)
	}
	return &providers.ListCachesResponse{
		Items: []providers.Cache{{Name: "cachedContents/demo", Provider: m.Name()}},
	}, nil
}

func (m *mockCacheProvider) UpdateCache(ctx context.Context, req *providers.UpdateCacheRequest) (*providers.Cache, error) {
	m.updateCalls.Add(1)
	if m.updateCacheFn != nil {
		return m.updateCacheFn(ctx, req)
	}
	return &providers.Cache{Name: req.Name, Provider: m.Name()}, nil
}

func (m *mockCacheProvider) DeleteCache(ctx context.Context, name string) error {
	m.deleteCalls.Add(1)
	if m.deleteCacheFn != nil {
		return m.deleteCacheFn(ctx, name)
	}
	return nil
}

type noopLogger struct{}

func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}

// providerErr builds a ProviderError for use in tests.
func providerErr(status int, retryable, fallback bool) *providers.ProviderError {
	et := providers.ErrTypeProvider
	switch status {
	case http.StatusBadRequest:
		et = providers.ErrTypeInvalidReq
	case http.StatusUnauthorized, http.StatusForbidden:
		et = providers.ErrTypeAuth
	case http.StatusTooManyRequests:
		et = providers.ErrTypeRateLimit
	}
	return &providers.ProviderError{
		Type:       et,
		Provider:   "mock",
		StatusCode: status,
		Message:    http.StatusText(status),
		Retryable:  retryable,
		Fallback:   fallback,
	}
}

func TestRetrySuccessOnFirstAttempt(t *testing.T) {
	mock := &mockProvider{name: "mock"}
	r := NewRetry(mock, 3)

	resp, err := r.Complete(context.Background(), &providers.Request{})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("unexpected content: %q", resp.Content)
	}
	if got := mock.callCount.Load(); got != 1 {
		t.Errorf("expected 1 call, got %d", got)
	}
}

func TestRetryExhaustedRetryableError(t *testing.T) {
	retryableErr := providerErr(http.StatusServiceUnavailable, true, true)
	mock := &mockProvider{
		name: "mock",
		completeFn: func(context.Context, *providers.Request) (*providers.Response, error) {
			return nil, retryableErr
		},
	}
	// maxRetries=2 -> 3 total attempts
	r := NewRetry(mock, 2)
	r.baseDelay = time.Millisecond // speed up test

	_, err := r.Complete(context.Background(), &providers.Request{})
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}
	if got := mock.callCount.Load(); got != 3 {
		t.Errorf("expected 3 total calls (1 + 2 retries), got %d", got)
	}
}

func TestRetryFastFailOnNonRetryable(t *testing.T) {
	badReqErr := providerErr(http.StatusBadRequest, false, false)
	mock := &mockProvider{
		name: "mock",
		completeFn: func(context.Context, *providers.Request) (*providers.Response, error) {
			return nil, badReqErr
		},
	}
	r := NewRetry(mock, 5)

	_, err := r.Complete(context.Background(), &providers.Request{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := mock.callCount.Load(); got != 1 {
		t.Errorf("non-retryable error must not retry; expected 1 call, got %d", got)
	}
}

func TestRetryFastFailOnAuthError(t *testing.T) {
	authErr := providerErr(http.StatusUnauthorized, false, true)
	mock := &mockProvider{
		name: "mock",
		completeFn: func(context.Context, *providers.Request) (*providers.Response, error) {
			return nil, authErr
		},
	}
	r := NewRetry(mock, 3)

	_, err := r.Complete(context.Background(), &providers.Request{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := mock.callCount.Load(); got != 1 {
		t.Errorf("auth error must not retry; expected 1 call, got %d", got)
	}
}

func TestRetryHonoursRetryAfterHint(t *testing.T) {
	hint := 50 * time.Millisecond
	retryAfterErr := &providers.ProviderError{
		Type:       providers.ErrTypeRateLimit,
		Provider:   "mock",
		StatusCode: http.StatusTooManyRequests,
		Message:    "rate limited",
		Retryable:  true,
		Fallback:   true,
		RetryAfter: hint,
	}
	calls := 0
	mock := &mockProvider{
		name: "mock",
		completeFn: func(context.Context, *providers.Request) (*providers.Response, error) {
			calls++
			if calls < 2 {
				return nil, retryAfterErr
			}
			return &providers.Response{Content: "ok"}, nil
		},
	}
	r := NewRetry(mock, 2)

	start := time.Now()
	_, err := r.Complete(context.Background(), &providers.Request{})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if elapsed < hint {
		t.Errorf("expected wait >= %v (Retry-After), got %v", hint, elapsed)
	}
}

func TestRetryContextCancellation(t *testing.T) {
	retryableErr := providerErr(http.StatusServiceUnavailable, true, true)
	mock := &mockProvider{
		name: "mock",
		completeFn: func(context.Context, *providers.Request) (*providers.Response, error) {
			return nil, retryableErr
		},
	}
	r := NewRetry(mock, 10)
	r.baseDelay = 500 * time.Millisecond // ensure sleep outlasts ctx

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := r.Complete(ctx, &providers.Request{})
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %T: %v", err, err)
	}
}

func TestRetrySuccessAfterTransientFailure(t *testing.T) {
	attempts := 0
	mock := &mockProvider{
		name: "mock",
		completeFn: func(context.Context, *providers.Request) (*providers.Response, error) {
			attempts++
			if attempts < 3 {
				return nil, providerErr(http.StatusInternalServerError, true, true)
			}
			return &providers.Response{Content: "recovered"}, nil
		},
	}
	r := NewRetry(mock, 3)
	r.baseDelay = time.Millisecond

	resp, err := r.Complete(context.Background(), &providers.Request{})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("unexpected content: %q", resp.Content)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryCreateCacheSuccessAfterTransientFailure(t *testing.T) {
	attempts := 0
	mock := newMockCacheProvider()
	mock.createCacheFn = func(context.Context, *providers.CreateCacheRequest) (*providers.Cache, error) {
		attempts++
		if attempts < 3 {
			return nil, providerErr(http.StatusServiceUnavailable, true, true)
		}
		return &providers.Cache{Name: "cachedContents/recovered", Provider: "mock"}, nil
	}

	r := NewRetry(mock, 3)
	r.baseDelay = time.Millisecond

	cache, err := r.CreateCache(context.Background(), &providers.CreateCacheRequest{Model: "gemini-2.5-flash"})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if cache.Name != "cachedContents/recovered" {
		t.Fatalf("cache.Name = %q, want %q", cache.Name, "cachedContents/recovered")
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRateLimitDisabled(t *testing.T) {
	mock := &mockProvider{name: "mock"}
	r := NewRateLimit(mock, 0) // disabled

	const n = 20
	for i := 0; i < n; i++ {
		_, err := r.Complete(context.Background(), &providers.Request{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if got := mock.callCount.Load(); got != n {
		t.Errorf("expected %d calls, got %d", n, got)
	}
}

func TestRateLimitThrottles(t *testing.T) {
	mock := &mockProvider{name: "mock"}
	// 10 req/s -> bucket capacity = 10 tokens, 1 token per 100 ms.
	const rps = 10
	r := NewRateLimit(mock, rps)

	// Drain the entire initial bucket with capacity requests.
	for i := 0; i < rps; i++ {
		if _, err := r.Complete(context.Background(), &providers.Request{}); err != nil {
			t.Fatalf("unexpected error draining bucket on request %d: %v", i, err)
		}
	}

	// The next request must wait for token refill (~100 ms).
	start := time.Now()
	if _, err := r.Complete(context.Background(), &providers.Request{}); err != nil {
		t.Fatalf("unexpected error after refill: %v", err)
	}
	elapsed := time.Since(start)

	// At 10 req/s, one refill takes 100 ms. Allow generous lower bound.
	const minWait = 80 * time.Millisecond
	if elapsed < minWait {
		t.Errorf("rate limiter too permissive after bucket drained: waited only %v, expected >= %v", elapsed, minWait)
	}
}

func TestRateLimitContextCancellation(t *testing.T) {
	mock := &mockProvider{name: "mock"}
	// 1 req/s - subsequent requests must wait 1s
	r := NewRateLimit(mock, 1)

	// Drain the initial token.
	if _, err := r.Complete(context.Background(), &providers.Request{}); err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	// Cancel after 50ms - well before the 1s refill.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := r.Complete(ctx, &providers.Request{})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %T: %v", err, err)
	}
}

func TestRateLimitCreateCacheThrottles(t *testing.T) {
	mock := newMockCacheProvider()
	const rps = 10
	r := NewRateLimit(mock, rps)

	req := &providers.CreateCacheRequest{Model: "gemini-2.5-flash"}
	for i := 0; i < rps; i++ {
		if _, err := r.CreateCache(context.Background(), req); err != nil {
			t.Fatalf("unexpected error draining bucket on request %d: %v", i, err)
		}
	}

	start := time.Now()
	if _, err := r.CreateCache(context.Background(), req); err != nil {
		t.Fatalf("unexpected error after refill: %v", err)
	}
	elapsed := time.Since(start)

	const minWait = 80 * time.Millisecond
	if elapsed < minWait {
		t.Errorf("rate limiter too permissive after bucket drained: waited only %v, expected >= %v", elapsed, minWait)
	}
}

func TestCircuitBreakerOpensAfterMaxFailures(t *testing.T) {
	serverErr := providerErr(http.StatusInternalServerError, true, true)
	mock := &mockProvider{
		name: "mock",
		completeFn: func(context.Context, *providers.Request) (*providers.Response, error) {
			return nil, serverErr
		},
	}

	cb := NewCircuitBreaker(mock, circuit.Config{
		MaxFailures:  3,
		ResetTimeout: 60 * time.Second,
	})

	req := &providers.Request{}

	// Drive 3 failures to open the circuit.
	for i := 0; i < 3; i++ {
		if _, err := cb.Complete(context.Background(), req); err == nil {
			t.Fatalf("expected error on attempt %d", i+1)
		}
	}

	// Next call should be rejected by the open circuit, not forwarded.
	countBefore := mock.callCount.Load()
	_, err := cb.Complete(context.Background(), req)
	if err == nil {
		t.Fatal("expected open-circuit error")
	}
	if mock.callCount.Load() != countBefore {
		t.Error("open circuit must not forward call to the underlying provider")
	}

	// The error must signal fallback so the client tries the next provider.
	var pe *providers.ProviderError
	if !errors.As(err, &pe) || !pe.Fallback {
		t.Errorf("open-circuit error must have Fallback=true, got %v", err)
	}
}

func TestCircuitBreakerAuthErrorDoesNotTrip(t *testing.T) {
	authErr := providerErr(http.StatusUnauthorized, false, true)
	mock := &mockProvider{
		name: "mock",
		completeFn: func(context.Context, *providers.Request) (*providers.Response, error) {
			return nil, authErr
		},
	}

	cb := NewCircuitBreaker(mock, circuit.Config{
		MaxFailures:  2,
		ResetTimeout: 60 * time.Second,
	})

	req := &providers.Request{}

	// Fire many auth errors - the circuit must stay closed.
	for i := 0; i < 10; i++ {
		cb.Complete(context.Background(), req) //nolint:errcheck
	}

	countBefore := mock.callCount.Load()
	cb.Complete(context.Background(), req) //nolint:errcheck
	if mock.callCount.Load() != countBefore+1 {
		t.Error("circuit must remain closed for auth errors; call was not forwarded")
	}
}

func TestCircuitBreakerBadRequestDoesNotTrip(t *testing.T) {
	badReqErr := providerErr(http.StatusBadRequest, false, false)
	mock := &mockProvider{
		name: "mock",
		completeFn: func(context.Context, *providers.Request) (*providers.Response, error) {
			return nil, badReqErr
		},
	}

	cb := NewCircuitBreaker(mock, circuit.Config{
		MaxFailures:  2,
		ResetTimeout: 60 * time.Second,
	})

	req := &providers.Request{}

	for i := 0; i < 5; i++ {
		cb.Complete(context.Background(), req) //nolint:errcheck
	}

	countBefore := mock.callCount.Load()
	cb.Complete(context.Background(), req) //nolint:errcheck
	if mock.callCount.Load() != countBefore+1 {
		t.Error("circuit must remain closed for bad-request errors; call was not forwarded")
	}
}

func TestCircuitBreakerHalfOpenAfterTimeout(t *testing.T) {
	serverErr := providerErr(http.StatusInternalServerError, true, true)
	calls := 0
	mock := &mockProvider{
		name: "mock",
		completeFn: func(context.Context, *providers.Request) (*providers.Response, error) {
			calls++
			if calls <= 3 {
				return nil, serverErr
			}
			return &providers.Response{Content: "ok"}, nil
		},
	}

	cb := NewCircuitBreaker(mock, circuit.Config{
		MaxFailures:      3,
		ResetTimeout:     20 * time.Millisecond, // short timeout for test
		HalfOpenRequests: 1,
	})

	req := &providers.Request{}

	// Trip the circuit.
	for i := 0; i < 3; i++ {
		cb.Complete(context.Background(), req) //nolint:errcheck
	}

	// Wait for reset timeout.
	time.Sleep(30 * time.Millisecond)

	// Probe request in half-open state must succeed and close the circuit.
	resp, err := cb.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("half-open probe should succeed, got %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("unexpected content: %q", resp.Content)
	}

	// Circuit should be closed again; further requests pass through.
	if _, err := cb.Complete(context.Background(), req); err != nil {
		t.Errorf("circuit should be closed after successful probe, got %v", err)
	}
}

func TestCircuitBreakerReopensOnHalfOpenFailure(t *testing.T) {
	serverErr := providerErr(http.StatusInternalServerError, true, true)
	mock := &mockProvider{
		name: "mock",
		completeFn: func(context.Context, *providers.Request) (*providers.Response, error) {
			return nil, serverErr
		},
	}

	cb := NewCircuitBreaker(mock, circuit.Config{
		MaxFailures:      3,
		ResetTimeout:     20 * time.Millisecond,
		HalfOpenRequests: 1,
	})

	req := &providers.Request{}

	// Trip the circuit.
	for i := 0; i < 3; i++ {
		cb.Complete(context.Background(), req) //nolint:errcheck
	}

	// Wait for reset, then probe - this will also fail.
	time.Sleep(30 * time.Millisecond)
	cb.Complete(context.Background(), req) //nolint:errcheck

	// Circuit must be open again: next call rejected without forwarding.
	countBefore := mock.callCount.Load()
	_, err := cb.Complete(context.Background(), req)
	if err == nil {
		t.Fatal("expected circuit to be open again after failed probe")
	}
	if mock.callCount.Load() != countBefore {
		t.Error("open circuit must not forward call to underlying provider")
	}
}

func TestCircuitBreakerCreateCacheOpensAfterMaxFailures(t *testing.T) {
	serverErr := providerErr(http.StatusInternalServerError, true, true)
	mock := newMockCacheProvider()
	mock.createCacheFn = func(context.Context, *providers.CreateCacheRequest) (*providers.Cache, error) {
		return nil, serverErr
	}

	cb := NewCircuitBreaker(mock, circuit.Config{
		MaxFailures:      2,
		ResetTimeout:     time.Minute,
		HalfOpenRequests: 1,
	})

	req := &providers.CreateCacheRequest{Model: "gemini-2.5-flash"}
	for i := 0; i < 2; i++ {
		if _, err := cb.CreateCache(context.Background(), req); err == nil {
			t.Fatalf("call %d: expected provider failure", i+1)
		}
	}

	_, err := cb.CreateCache(context.Background(), req)
	if err == nil {
		t.Fatal("expected circuit breaker error on third call")
	}
	pe := unwrap(err)
	if pe == nil || pe.Message != "circuit breaker open" {
		t.Fatalf("expected circuit breaker open error, got %v", err)
	}
	if got := mock.createCalls.Load(); got != 2 {
		t.Fatalf("expected provider create calls = 2, got %d", got)
	}
}

func TestCacheMiddlewarePassThrough(t *testing.T) {
	tests := []struct {
		name string
		wrap func(*mockCacheProvider) providers.CacheProvider
	}{
		{
			name: "logging",
			wrap: func(p *mockCacheProvider) providers.CacheProvider {
				return NewLogging(p, noopLogger{})
			},
		},
		{
			name: "ratelimit",
			wrap: func(p *mockCacheProvider) providers.CacheProvider {
				return NewRateLimit(p, 1000)
			},
		},
		{
			name: "retry",
			wrap: func(p *mockCacheProvider) providers.CacheProvider {
				return NewRetry(p, 1)
			},
		},
		{
			name: "circuit",
			wrap: func(p *mockCacheProvider) providers.CacheProvider {
				return NewCircuitBreaker(p, circuit.Config{
					MaxFailures:      2,
					ResetTimeout:     time.Minute,
					HalfOpenRequests: 1,
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockCacheProvider()
			cp := tt.wrap(mock)

			created, err := cp.CreateCache(context.Background(), &providers.CreateCacheRequest{Model: "gemini-2.5-flash"})
			if err != nil {
				t.Fatalf("CreateCache: %v", err)
			}
			if created.Name != "cachedContents/demo" {
				t.Fatalf("CreateCache.Name = %q, want %q", created.Name, "cachedContents/demo")
			}

			got, err := cp.GetCache(context.Background(), "cachedContents/demo")
			if err != nil {
				t.Fatalf("GetCache: %v", err)
			}
			if got.Name != "cachedContents/demo" {
				t.Fatalf("GetCache.Name = %q, want %q", got.Name, "cachedContents/demo")
			}

			list, err := cp.ListCaches(context.Background(), &providers.ListCachesRequest{PageSize: 10})
			if err != nil {
				t.Fatalf("ListCaches: %v", err)
			}
			if len(list.Items) != 1 || list.Items[0].Name != "cachedContents/demo" {
				t.Fatalf("ListCaches = %+v", list)
			}

			updated, err := cp.UpdateCache(context.Background(), &providers.UpdateCacheRequest{Name: "cachedContents/demo"})
			if err != nil {
				t.Fatalf("UpdateCache: %v", err)
			}
			if updated.Name != "cachedContents/demo" {
				t.Fatalf("UpdateCache.Name = %q, want %q", updated.Name, "cachedContents/demo")
			}

			if err := cp.DeleteCache(context.Background(), "cachedContents/demo"); err != nil {
				t.Fatalf("DeleteCache: %v", err)
			}

			if mock.createCalls.Load() != 1 ||
				mock.getCalls.Load() != 1 ||
				mock.listCalls.Load() != 1 ||
				mock.updateCalls.Load() != 1 ||
				mock.deleteCalls.Load() != 1 {
				t.Fatalf("unexpected cache call counts: create=%d get=%d list=%d update=%d delete=%d",
					mock.createCalls.Load(),
					mock.getCalls.Load(),
					mock.listCalls.Load(),
					mock.updateCalls.Load(),
					mock.deleteCalls.Load(),
				)
			}
		})
	}
}

func TestCacheMiddlewareUnsupportedProvider(t *testing.T) {
	tests := []struct {
		name string
		wrap func(*mockProvider) providers.Provider
	}{
		{
			name: "logging",
			wrap: func(p *mockProvider) providers.Provider {
				return NewLogging(p, noopLogger{})
			},
		},
		{
			name: "ratelimit",
			wrap: func(p *mockProvider) providers.Provider {
				return NewRateLimit(p, 1000)
			},
		},
		{
			name: "retry",
			wrap: func(p *mockProvider) providers.Provider {
				return NewRetry(p, 1)
			},
		},
		{
			name: "circuit",
			wrap: func(p *mockProvider) providers.Provider {
				return NewCircuitBreaker(p, circuit.Config{
					MaxFailures:      2,
					ResetTimeout:     time.Minute,
					HalfOpenRequests: 1,
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.wrap(&mockProvider{name: "mock"})
			cp, ok := p.(providers.CacheProvider)
			if !ok {
				t.Fatalf("%s middleware does not implement CacheProvider", tt.name)
			}
			_, err := cp.CreateCache(context.Background(), &providers.CreateCacheRequest{Model: "gemini-2.5-flash"})
			if err == nil {
				t.Fatal("expected unsupported cache management error")
			}
			if got := err.Error(); got != `provider "mock" does not support cache management` {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// backoff unit test
func TestBackoffWithHint(t *testing.T) {
	r := NewRetry(&mockProvider{name: "mock"}, 3)
	hint := 5 * time.Second
	got := r.backoff(0, hint)
	if got != hint {
		t.Errorf("backoff with hint: got %v, want %v", got, hint)
	}
}

func TestBackoffIncreasesWithAttempt(t *testing.T) {
	r := NewRetry(&mockProvider{name: "mock"}, 5)
	r.baseDelay = time.Millisecond
	r.maxDelay = time.Second

	var prev time.Duration
	for attempt := 0; attempt < 5; attempt++ {
		// Sample multiple times to account for jitter.
		sum := time.Duration(0)
		const samples = 100
		for i := 0; i < samples; i++ {
			sum += r.backoff(attempt, 0)
		}
		avg := sum / samples
		if attempt > 0 && avg <= prev {
			t.Errorf("attempt %d: average backoff %v not greater than previous %v", attempt, avg, prev)
		}
		prev = avg
	}
}

func TestBackoffCapEnforced(t *testing.T) {
	r := NewRetry(&mockProvider{name: "mock"}, 100)
	r.maxDelay = 1 * time.Second

	for attempt := 0; attempt < 50; attempt++ {
		got := r.backoff(attempt, 0)
		if got > r.maxDelay {
			t.Errorf("attempt %d: backoff %v exceeds maxDelay %v", attempt, got, r.maxDelay)
		}
	}
}
