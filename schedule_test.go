package webhook

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestDefaultScheduleInOrderThenExhausted(t *testing.T) {
	want := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 8 * time.Hour, 24 * time.Hour}
	for i, w := range want {
		d, ok := DefaultSchedule.Delay(i + 1)
		if !ok || d != w {
			t.Fatalf("attempt %d: got %v ok=%v, want %v", i+1, d, ok, w)
		}
	}
	if _, ok := DefaultSchedule.Delay(len(want) + 1); ok {
		t.Fatal("a seventh failure must exhaust the schedule")
	}
	if _, ok := DefaultSchedule.Delay(0); ok {
		t.Fatal("zero failures is not a retry")
	}
}

func TestDefaultJitterStaysWithinTenPercent(t *testing.T) {
	base := 10 * time.Minute
	lo, hi := time.Duration(float64(base)*0.9), time.Duration(float64(base)*1.1)
	for range 1000 {
		got := DefaultJitter(base)
		if got < lo || got > hi {
			t.Fatalf("jitter %v outside [%v, %v]", got, lo, hi)
		}
	}
	if DefaultJitter(0) != 0 {
		t.Fatal("zero stays zero")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

func TestClassify(t *testing.T) {
	cases := []struct {
		status int
		err    error
		want   Outcome
	}{
		{200, nil, OutcomeDelivered},
		{204, nil, OutcomeDelivered},
		{299, nil, OutcomeDelivered},
		{429, nil, OutcomeRetry},
		{500, nil, OutcomeRetry},
		{503, nil, OutcomeRetry},
		{0, errors.New("connection refused"), OutcomeRetry},
		{0, timeoutErr{}, OutcomeRetry},
		{400, nil, OutcomeDrop},
		{401, nil, OutcomeDrop},
		{404, nil, OutcomeDrop},
		{410, nil, OutcomeDrop},
		{301, nil, OutcomeDrop},
	}
	for _, c := range cases {
		if got := Classify(c.status, c.err); got != c.want {
			t.Errorf("Classify(%d, %v) = %v, want %v", c.status, c.err, got, c.want)
		}
	}
	if !isTimeout(timeoutErr{}) || isTimeout(errors.New("x")) {
		t.Fatal("isTimeout misclassifies")
	}
}
