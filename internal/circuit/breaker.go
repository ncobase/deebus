// Package circuit implements the circuit breaker pattern for provider protection.
package circuit

import (
	"errors"
	"sync"
	"time"
)

// ErrOpen is returned when a request is rejected because the circuit is open.
var ErrOpen = errors.New("circuit breaker open")

// State represents the circuit breaker state machine.
type State int

const (
	StateClosed   State = iota // Normal operation; requests pass through.
	StateOpen                  // Provider deemed unhealthy; requests rejected.
	StateHalfOpen              // Trial period; limited requests allowed.
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Config holds circuit breaker parameters.
type Config struct {
	// MaxFailures is the number of consecutive failures before the circuit opens.
	// Default: 5.
	MaxFailures int

	// ResetTimeout is how long the circuit stays open before moving to half-open.
	// Default: 60 seconds.
	ResetTimeout time.Duration

	// HalfOpenRequests is the maximum number of requests allowed through in
	// half-open state to probe recovery. Default: 1.
	HalfOpenRequests int
}

func (c *Config) withDefaults() Config {
	out := *c
	if out.MaxFailures <= 0 {
		out.MaxFailures = 5
	}
	if out.ResetTimeout <= 0 {
		out.ResetTimeout = 60 * time.Second
	}
	if out.HalfOpenRequests <= 0 {
		out.HalfOpenRequests = 1
	}
	return out
}

// Breaker is a thread-safe circuit breaker.
type Breaker struct {
	cfg          Config
	mu           sync.Mutex
	state        State
	failures     int
	halfOpenSent int
	lastFailTime time.Time
}

// New creates a Breaker with the given configuration.
func New(cfg Config) *Breaker {
	return &Breaker{cfg: cfg.withDefaults()}
}

// Allow returns nil if a request may proceed, or ErrOpen if the circuit is open.
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return nil

	case StateOpen:
		if time.Since(b.lastFailTime) >= b.cfg.ResetTimeout {
			b.state = StateHalfOpen
			b.halfOpenSent = 0
			// fall through to half-open logic
		} else {
			return ErrOpen
		}
		fallthrough

	case StateHalfOpen:
		if b.halfOpenSent >= b.cfg.HalfOpenRequests {
			return ErrOpen
		}
		b.halfOpenSent++
		return nil
	}

	return nil
}

// RecordSuccess records that the last request succeeded.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	if b.state == StateHalfOpen {
		b.state = StateClosed
	}
}

// RecordFailure records that the last request failed.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.lastFailTime = time.Now()
	if b.state == StateHalfOpen || b.failures >= b.cfg.MaxFailures {
		b.state = StateOpen
		b.failures = 0
	}
}

// State returns the current state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
