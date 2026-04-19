package providers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// parseError converts an HTTP error response into a *ProviderError with
// correctly classified Retryable and Fallback flags.
//
// Retry and fallback strategy:
//
//	400 Bad Request   -> not retryable, not fallback  (malformed request)
//	401/403           -> not retryable, fallback       (try a differently-configured provider)
//	408/504 Timeout   -> retryable,     fallback
//	429 Rate Limit    -> retryable,     fallback       (honour Retry-After if present)
//	5xx Server Error  -> retryable,     fallback
//	other             -> retryable if >=500, always fallback
func parseError(statusCode int, body []byte, header http.Header, provider string) *ProviderError {
	msg := string(body)
	if len(msg) > 300 {
		msg = msg[:300] + "..."
	}

	pe := &ProviderError{
		Provider:   provider,
		StatusCode: statusCode,
	}

	switch {
	case statusCode == http.StatusBadRequest: // 400
		pe.Type = ErrTypeInvalidReq
		pe.Message = msg
		pe.Retryable = false
		pe.Fallback = false // malformed request; no point trying another provider

	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden: // 401, 403
		pe.Type = ErrTypeAuth
		pe.Message = "authentication failed"
		pe.Retryable = false
		pe.Fallback = true // another provider may have valid credentials

	case statusCode == http.StatusRequestTimeout: // 408
		pe.Type = ErrTypeTimeout
		pe.Message = "request timeout"
		pe.Retryable = true
		pe.Fallback = true

	case statusCode == http.StatusTooManyRequests: // 429
		pe.Type = ErrTypeRateLimit
		pe.Message = "rate limit exceeded"
		pe.Retryable = true
		pe.Fallback = true
		pe.RetryAfter = parseRetryAfter(header)

	case statusCode == http.StatusGatewayTimeout: // 504
		pe.Type = ErrTypeTimeout
		pe.Message = "gateway timeout"
		pe.Retryable = true
		pe.Fallback = true

	case statusCode >= 500: // 500, 502, 503, ...
		pe.Type = ErrTypeProvider
		pe.Message = fmt.Sprintf("server error (%d)", statusCode)
		pe.Retryable = true
		pe.Fallback = true

	default:
		pe.Type = ErrTypeUnknown
		pe.Message = fmt.Sprintf("HTTP %d: %s", statusCode, msg)
		pe.Retryable = statusCode >= 500
		pe.Fallback = statusCode != http.StatusBadRequest
	}

	return pe
}

// parseRetryAfter extracts the wait duration from a Retry-After header.
// The header value may be an integer number of seconds or an HTTP date.
func parseRetryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// networkError wraps a raw network/transport error as a retryable ProviderError.
func networkError(provider string, err error) *ProviderError {
	return &ProviderError{
		Type:      ErrTypeNetwork,
		Provider:  provider,
		Message:   err.Error(),
		Retryable: true,
		Fallback:  true,
		Err:       err,
	}
}
