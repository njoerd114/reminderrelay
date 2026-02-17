package homeassistant

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetry_SucceedsFirstAttempt(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), 3, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("called %d times, want 1", calls)
	}
}

func TestRetry_SucceedsSecondAttempt(t *testing.T) {
	sentinel := errors.New("transient")
	calls := 0
	err := Retry(context.Background(), 3, func() error {
		calls++
		if calls < 2 {
			return sentinel
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("called %d times, want 2", calls)
	}
}

func TestRetry_AllAttemptsFail(t *testing.T) {
	sentinel := errors.New("persistent failure")
	calls := 0
	err := Retry(context.Background(), 3, func() error {
		calls++
		return sentinel
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 3 {
		t.Errorf("called %d times, want 3", calls)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error chain does not contain sentinel: %v", err)
	}
}

func TestRetry_ContextCancelledBeforeAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	calls := 0
	err := Retry(ctx, 3, func() error {
		calls++
		return nil
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 0 {
		t.Errorf("called %d times, want 0 (context already cancelled)", calls)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled in chain, got: %v", err)
	}
}

func TestRetry_ContextCancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	sentinel := errors.New("fail")
	calls := 0
	err := Retry(ctx, 10, func() error {
		calls++
		return sentinel
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should have made at least 1 call but fewer than 10 due to timeout.
	if calls < 1 || calls >= 10 {
		t.Errorf("calls = %d, expected between 1 and 9", calls)
	}
}

func TestRetry_SingleAttempt(t *testing.T) {
	sentinel := errors.New("fail once")
	err := Retry(context.Background(), 1, func() error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel in chain, got: %v", err)
	}
}

func TestBackoffDelay_Increases(t *testing.T) {
	d0 := backoffDelay(0)
	d1 := backoffDelay(1)
	d2 := backoffDelay(2)

	// With jitter, individual values are random, but we can check that the
	// max possible value increases (delay/2 + jitter where jitter < delay/2).
	// d0 ∈ [250ms, 500ms), d1 ∈ [500ms, 1s), d2 ∈ [1s, 2s)
	if d0 < 250*time.Millisecond || d0 >= 500*time.Millisecond {
		t.Errorf("d0 = %v, expected [250ms, 500ms)", d0)
	}
	if d1 < 500*time.Millisecond || d1 >= 1*time.Second {
		t.Errorf("d1 = %v, expected [500ms, 1s)", d1)
	}
	if d2 < 1*time.Second || d2 >= 2*time.Second {
		t.Errorf("d2 = %v, expected [1s, 2s)", d2)
	}
}

func TestBackoffDelay_Capped(t *testing.T) {
	// At attempt 10, raw delay would be 500ms * 2^10 = 512s, but should be capped.
	d := backoffDelay(10)
	if d >= maxDelay {
		t.Errorf("delay = %v, expected < maxDelay (%v) due to jitter", d, maxDelay)
	}
	if d < maxDelay/2 {
		t.Errorf("delay = %v, expected >= maxDelay/2 (%v)", d, maxDelay/2)
	}
}
