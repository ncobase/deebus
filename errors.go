package deebus

import "github.com/ncobase/deebus/providers"

// ProviderError is the structured error type returned by all providers.
// Re-exported from the providers package for convenient access.
type ProviderError = providers.ProviderError

// IsRetryable reports whether err is a retryable error — i.e. the same
// provider should be tried again (after a backoff).
func IsRetryable(err error) bool { return providers.IsRetryable(err) }

// IsFallback reports whether err should cause the client to try the next
// provider in the fallback chain.
func IsFallback(err error) bool { return providers.IsFallback(err) }
