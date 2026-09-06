package webhook

import (
	"testing"
	"time"
)

func TestDeliveryStateMachine(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	d := Delivery{ID: "d1", Status: StatusPending, NextAttemptAt: now}
	if !d.Due(now) {
		t.Fatal("a pending delivery whose time has come is due")
	}

	// Six retryable failures walk the whole schedule, then dead-letter.
	for i, delay := range DefaultSchedule {
		d.Attempt(OutcomeRetry, 503, "receiver answered 503", now, DefaultSchedule, NoJitter)
		if d.Status != StatusRetrying {
			t.Fatalf("after failure %d: status %s", i+1, d.Status)
		}
		if d.Attempts != i+1 {
			t.Fatalf("attempts %d, want %d", d.Attempts, i+1)
		}
		if !d.NextAttemptAt.Equal(now.Add(delay)) {
			t.Fatalf("after failure %d: next %v, want %v", i+1, d.NextAttemptAt, now.Add(delay))
		}
		if d.Due(now) {
			t.Fatalf("after failure %d: must not be due before its delay", i+1)
		}
		if !d.Due(now.Add(delay)) {
			t.Fatalf("after failure %d: must be due at its delay", i+1)
		}
		now = now.Add(delay)
	}
	d.Attempt(OutcomeRetry, 0, "connection refused", now, DefaultSchedule, NoJitter)
	if d.Status != StatusDeadLetter || !d.Status.Terminal() || d.Due(now.Add(48*time.Hour)) {
		t.Fatalf("seventh failure must dead-letter: %+v", d)
	}
	if d.Attempts != len(DefaultSchedule)+1 || d.LastError != "connection refused" || d.LastHTTPStatus != 0 {
		t.Fatalf("attempt record: %+v", d)
	}
}

func TestDeliveryDeliveredAndDroppedAreTerminal(t *testing.T) {
	now := time.Unix(1757170123, 0)

	ok := Delivery{ID: "ok", Status: StatusRetrying, Attempts: 2, NextAttemptAt: now}
	ok.Attempt(OutcomeDelivered, 200, "", now, DefaultSchedule, NoJitter)
	if ok.Status != StatusDelivered || ok.Attempts != 3 || !ok.NextAttemptAt.IsZero() || ok.Due(now) {
		t.Fatalf("delivered: %+v", ok)
	}

	no := Delivery{ID: "no", Status: StatusPending, NextAttemptAt: now}
	no.Attempt(OutcomeDrop, 404, "receiver answered 404 Not Found", now, DefaultSchedule, NoJitter)
	if no.Status != StatusDropped || no.Attempts != 1 || no.Due(now) {
		t.Fatalf("dropped: %+v", no)
	}
	for _, s := range []Status{StatusDelivered, StatusDeadLetter, StatusDropped} {
		if !s.Terminal() {
			t.Errorf("%s must be terminal", s)
		}
	}
	for _, s := range []Status{StatusPending, StatusRetrying} {
		if s.Terminal() {
			t.Errorf("%s must not be terminal", s)
		}
	}
}

func TestDeliveryRetryUsesJitter(t *testing.T) {
	now := time.Unix(1757170123, 0)
	d := Delivery{ID: "j", Status: StatusPending, NextAttemptAt: now}
	doubled := func(x time.Duration) time.Duration { return 2 * x }
	d.Attempt(OutcomeRetry, 500, "", now, Schedule{time.Minute}, doubled)
	if !d.NextAttemptAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("jitter not applied: next %v", d.NextAttemptAt)
	}
}
