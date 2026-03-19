package deebus

import "testing"

func TestStats(t *testing.T) {
	s := &Stats{}

	s.RecordRequest(true, 80, 20, 0, 50)   // 100 total, 50 cache read
	s.RecordRequest(true, 150, 50, 200, 0) // 200 total, 200 cache created
	s.RecordRequest(false, 0, 0, 0, 0)

	total, input, output, success, failed := s.Get()

	if total != 3 {
		t.Errorf("total: got %d, want 3", total)
	}
	if input != 230 {
		t.Errorf("inputTokens: got %d, want 230", input)
	}
	if output != 70 {
		t.Errorf("outputTokens: got %d, want 70", output)
	}
	if input+output != 300 {
		t.Errorf("total tokens: got %d, want 300", input+output)
	}
	if success != 2 {
		t.Errorf("success: got %d, want 2", success)
	}
	if failed != 1 {
		t.Errorf("failed: got %d, want 1", failed)
	}
	if s.CacheCreatedTokens.Load() != 200 {
		t.Errorf("cacheCreated: got %d, want 200", s.CacheCreatedTokens.Load())
	}
	if s.CacheReadTokens.Load() != 50 {
		t.Errorf("cacheRead: got %d, want 50", s.CacheReadTokens.Load())
	}
}
