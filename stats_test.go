package deebus

import "testing"

func TestStats(t *testing.T) {
	s := &Stats{}
	
	// Record success
	s.RecordRequest(true, 100)
	s.RecordRequest(true, 200)
	
	// Record failure
	s.RecordRequest(false, 0)
	
	total, tokens, success, failed := s.Get()
	
	if total != 3 {
		t.Errorf("expected 3 total requests, got %d", total)
	}
	
	if tokens != 300 {
		t.Errorf("expected 300 tokens, got %d", tokens)
	}
	
	if success != 2 {
		t.Errorf("expected 2 success, got %d", success)
	}
	
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}
}
