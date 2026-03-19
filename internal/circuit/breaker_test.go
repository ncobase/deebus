package circuit

import (
	"testing"
	"time"
)

func TestBreakerInitialState(t *testing.T) {
	b := New(Config{MaxFailures: 3, ResetTimeout: 100 * time.Millisecond})
	if b.State() != StateClosed {
		t.Fatalf("want closed, got %s", b.State())
	}
	if err := b.Allow(); err != nil {
		t.Fatalf("unexpected error in closed state: %v", err)
	}
}

func TestBreakerOpensAfterMaxFailures(t *testing.T) {
	b := New(Config{MaxFailures: 3, ResetTimeout: time.Second})
	for i := 0; i < 3; i++ {
		b.Allow()
		b.RecordFailure()
	}
	if b.State() != StateOpen {
		t.Fatalf("want open, got %s", b.State())
	}
	if err := b.Allow(); err != ErrOpen {
		t.Fatalf("want ErrOpen, got %v", err)
	}
}

func TestBreakerHalfOpenAfterTimeout(t *testing.T) {
	b := New(Config{MaxFailures: 1, ResetTimeout: 50 * time.Millisecond})
	b.Allow()
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatal("expected open state")
	}

	time.Sleep(60 * time.Millisecond)

	// First request should be allowed (half-open probe)
	if err := b.Allow(); err != nil {
		t.Fatalf("expected probe request to be allowed: %v", err)
	}
	// Second request should be rejected (HalfOpenRequests=1 by default)
	if err := b.Allow(); err != ErrOpen {
		t.Fatalf("expected ErrOpen for second half-open request, got %v", err)
	}
}

func TestBreakerClosesAfterHalfOpenSuccess(t *testing.T) {
	b := New(Config{MaxFailures: 1, ResetTimeout: 50 * time.Millisecond})
	b.Allow()
	b.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	b.Allow()
	b.RecordSuccess()

	if b.State() != StateClosed {
		t.Fatalf("want closed after successful half-open probe, got %s", b.State())
	}
}

func TestBreakerReopensAfterHalfOpenFailure(t *testing.T) {
	b := New(Config{MaxFailures: 1, ResetTimeout: 50 * time.Millisecond})
	b.Allow()
	b.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	b.Allow()
	b.RecordFailure()

	if b.State() != StateOpen {
		t.Fatalf("want open after failed half-open probe, got %s", b.State())
	}
}

func TestBreakerSuccessResetsFailures(t *testing.T) {
	b := New(Config{MaxFailures: 3, ResetTimeout: time.Second})
	b.Allow()
	b.RecordFailure()
	b.Allow()
	b.RecordFailure()
	b.Allow()
	b.RecordSuccess() // success resets the failure counter

	if b.State() != StateClosed {
		t.Fatalf("want closed, got %s", b.State())
	}
	// Two more failures should not open the circuit (counter was reset)
	b.Allow()
	b.RecordFailure()
	b.Allow()
	b.RecordFailure()
	if b.State() != StateClosed {
		t.Fatalf("want closed, got %s", b.State())
	}
}
