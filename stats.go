package deebus

import "sync/atomic"

// Stats tracks usage statistics
type Stats struct {
	TotalRequests   atomic.Int64
	TotalTokens     atomic.Int64
	SuccessRequests atomic.Int64
	FailedRequests  atomic.Int64
}

// RecordRequest records a request
func (s *Stats) RecordRequest(success bool, tokens int) {
	s.TotalRequests.Add(1)
	s.TotalTokens.Add(int64(tokens))
	if success {
		s.SuccessRequests.Add(1)
	} else {
		s.FailedRequests.Add(1)
	}
}

// Get returns current stats
func (s *Stats) Get() (total, tokens, success, failed int64) {
	return s.TotalRequests.Load(),
		s.TotalTokens.Load(),
		s.SuccessRequests.Load(),
		s.FailedRequests.Load()
}
