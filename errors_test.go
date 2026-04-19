package deebus

import (
	"errors"
	"testing"

	"github.com/ncobase/deebus/providers"
)

func TestIsRetryable(t *testing.T) {
	retryable := &providers.ProviderError{
		Type:      providers.ErrTypeRateLimit,
		Provider:  "openai",
		Message:   "rate limit exceeded",
		Retryable: true,
	}
	if !IsRetryable(retryable) {
		t.Error("expected retryable=true for rate limit error")
	}

	authErr := &providers.ProviderError{
		Type:      providers.ErrTypeAuth,
		Provider:  "openai",
		Message:   "auth failed",
		Retryable: false,
	}
	if IsRetryable(authErr) {
		t.Error("expected retryable=false for auth error")
	}

	if IsRetryable(errors.New("generic error")) {
		t.Error("generic errors should not be retryable")
	}
}

func TestIsFallback(t *testing.T) {
	// Bad request - should NOT fallback
	badReq := &providers.ProviderError{
		Type:       providers.ErrTypeInvalidReq,
		Provider:   "openai",
		StatusCode: 400,
		Message:    "bad request",
		Fallback:   false,
	}
	if IsFallback(badReq) {
		t.Error("400 bad request should not trigger fallback")
	}

	// Auth error - should fallback (try another provider)
	authErr := &providers.ProviderError{
		Type:       providers.ErrTypeAuth,
		Provider:   "openai",
		StatusCode: 401,
		Fallback:   true,
	}
	if !IsFallback(authErr) {
		t.Error("auth error should trigger fallback")
	}

	// Generic Go error - should always fallback
	if !IsFallback(errors.New("connection refused")) {
		t.Error("network error should trigger fallback")
	}
}

func TestProviderErrorWrapping(t *testing.T) {
	inner := errors.New("original")
	pe := &providers.ProviderError{
		Type:      providers.ErrTypeNetwork,
		Provider:  "anthropic",
		Message:   "connection refused",
		Retryable: true,
		Err:       inner,
	}

	if !errors.Is(pe, inner) {
		t.Error("ProviderError should unwrap to inner error via errors.Is")
	}
	if !IsRetryable(pe) {
		t.Error("network error should be retryable")
	}
}
