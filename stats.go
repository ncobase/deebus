package deebus

import "sync/atomic"

// Stats tracks aggregate usage statistics across all requests.
type Stats struct {
	TotalRequests      atomic.Int64
	InputTokens        atomic.Int64
	OutputTokens       atomic.Int64
	TotalTokens        atomic.Int64
	SuccessRequests    atomic.Int64
	FailedRequests     atomic.Int64
	CacheCreatedTokens atomic.Int64 // tokens written to cache (Anthropic cache_creation_input_tokens)
	CacheReadTokens    atomic.Int64 // tokens served from cache (Anthropic cache_read_input_tokens; OpenAI cached_tokens; Gemini cachedContentTokenCount)
}

// RecordRequest records a completed request with its token and cache breakdown.
func (s *Stats) RecordRequest(success bool, input, output, cacheCreated, cacheRead int) {
	s.TotalRequests.Add(1)
	s.InputTokens.Add(int64(input))
	s.OutputTokens.Add(int64(output))
	s.TotalTokens.Add(int64(input + output))
	s.CacheCreatedTokens.Add(int64(cacheCreated))
	s.CacheReadTokens.Add(int64(cacheRead))
	if success {
		s.SuccessRequests.Add(1)
	} else {
		s.FailedRequests.Add(1)
	}
}

// Get returns current aggregate statistics.
func (s *Stats) Get() (requests, inputTokens, outputTokens, success, failed int64) {
	return s.TotalRequests.Load(),
		s.InputTokens.Load(),
		s.OutputTokens.Load(),
		s.SuccessRequests.Load(),
		s.FailedRequests.Load()
}
