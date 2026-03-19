package middleware

import (
	"context"
	"net/http"

	"github.com/ncobase/deebus/internal/circuit"
	"github.com/ncobase/deebus/providers"
)

// CircuitBreakerMiddleware wraps a provider with circuit breaker protection.
// When the provider fails consistently, the circuit opens and requests are
// rejected immediately (with Fallback=true) until the reset timeout elapses.
//
// Note: auth errors (401/403) do not trip the circuit because they are
// configuration issues, not provider health indicators.
type CircuitBreakerMiddleware struct {
	provider providers.Provider
	breaker  *circuit.Breaker
}

// NewCircuitBreaker wraps p with a circuit breaker configured by cfg.
func NewCircuitBreaker(p providers.Provider, cfg circuit.Config) *CircuitBreakerMiddleware {
	return &CircuitBreakerMiddleware{
		provider: p,
		breaker:  circuit.New(cfg),
	}
}

func (m *CircuitBreakerMiddleware) Name() string { return m.provider.Name() }

func (m *CircuitBreakerMiddleware) Complete(ctx context.Context, req *providers.Request) (*providers.Response, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	resp, err := m.provider.Complete(ctx, req)
	m.record(err)
	return resp, err
}

func (m *CircuitBreakerMiddleware) Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	ch, err := m.provider.Stream(ctx, req)
	m.record(err)
	return ch, err
}

func (m *CircuitBreakerMiddleware) Embed(ctx context.Context, req *providers.EmbedRequest) (*providers.EmbedResponse, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	resp, err := m.provider.Embed(ctx, req)
	m.record(err)
	return resp, err
}

func (m *CircuitBreakerMiddleware) Health(ctx context.Context) error {
	return m.provider.Health(ctx)
}

// allow checks the circuit state and returns an error if the circuit is open.
func (m *CircuitBreakerMiddleware) allow() error {
	if err := m.breaker.Allow(); err != nil {
		return &providers.ProviderError{
			Type:      providers.ErrTypeProvider,
			Provider:  m.provider.Name(),
			Message:   "circuit breaker open",
			Retryable: false,
			Fallback:  true,
			Err:       err,
		}
	}
	return nil
}

// record updates the circuit breaker based on the outcome of a call.
func (m *CircuitBreakerMiddleware) record(err error) {
	if err == nil {
		m.breaker.RecordSuccess()
		return
	}
	if tripsBreaker(err) {
		m.breaker.RecordFailure()
	} else {
		// Auth errors / bad requests do not indicate provider health problems.
		m.breaker.RecordSuccess()
	}
}

// tripsBreaker returns false for errors that should not count as provider
// failures (auth errors, bad request errors).
func tripsBreaker(err error) bool {
	pe := unwrap(err)
	if pe == nil {
		return true
	}
	switch pe.StatusCode {
	case http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden:
		return false
	}
	return true
}
