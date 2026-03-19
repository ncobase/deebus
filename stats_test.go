package deebus

import "testing"

func TestStats(t *testing.T) {
	s := &Stats{}

	s.RecordRequest(true, 80, 20)  // 100 total
	s.RecordRequest(true, 150, 50) // 200 total
	s.RecordRequest(false, 0, 0)

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
}
