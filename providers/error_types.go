package providers

import (
	"fmt"
	"time"
)

// ErrorType classifies a provider error by its cause.
type ErrorType string

const (
	ErrTypeNetwork    ErrorType = "network"
	ErrTypeAuth       ErrorType = "auth"
	ErrTypeRateLimit  ErrorType = "rate_limit"
	ErrTypeInvalidReq ErrorType = "invalid_request"
	ErrTypeProvider   ErrorType = "provider"
	ErrTypeTimeout    ErrorType = "timeout"
	ErrTypeUnknown    ErrorType = "unknown"
)

// ProviderError is a structured, typed error returned by a provider.
type ProviderError struct {
	Type       ErrorType
	Provider   string
	StatusCode int
	Message    string

	// Retryable indicates the same provider should be retried (e.g. 429, 5xx).
	Retryable bool

	// Fallback indicates the next provider in the chain should be tried.
	// False only for 400 Bad Request - the request itself is malformed.
	Fallback bool

	// RetryAfter is the server-requested wait duration parsed from the
	// Retry-After response header on 429 responses.
	RetryAfter time.Duration

	// Err wraps the underlying error, if any.
	Err error
}

func (e *ProviderError) Error() string {
	if e.Provider != "" {
		return fmt.Sprintf("[%s/%s] %s", e.Provider, e.Type, e.Message)
	}
	return fmt.Sprintf("[%s] %s", e.Type, e.Message)
}

func (e *ProviderError) Unwrap() error { return e.Err }

// IsRetryable reports whether err is a retryable ProviderError.
func IsRetryable(err error) bool {
	pe := unwrapProviderError(err)
	return pe != nil && pe.Retryable
}

// IsFallback reports whether err should trigger trying the next provider.
func IsFallback(err error) bool {
	pe := unwrapProviderError(err)
	if pe == nil {
		return true // network/unknown errors -> try next provider
	}
	return pe.Fallback
}

// unwrapProviderError walks the error chain and returns the first ProviderError.
func unwrapProviderError(err error) *ProviderError {
	for err != nil {
		if pe, ok := err.(*ProviderError); ok {
			return pe
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return nil
}
