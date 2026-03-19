package deebus

import "sync/atomic"

// Stats tracks aggregate usage statistics across all requests.
type Stats struct {
	TotalRequests   atomic.Int64
	InputTokens     atomic.Int64
	OutputTokens    atomic.Int64
	TotalTokens     atomic.Int64
	SuccessRequests atomic.Int64
	FailedRequests  atomic.Int64
}

// RecordRequest records a completed request with its token breakdown.
func (s *Stats) RecordRequest(success bool, input, output int) {
	s.TotalRequests.Add(1)
	s.InputTokens.Add(int64(input))
	s.OutputTokens.Add(int64(output))
	s.TotalTokens.Add(int64(input + output))
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
