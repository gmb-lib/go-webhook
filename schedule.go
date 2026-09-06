package webhook

import (
	"errors"
	"math/rand/v2"
	"net"
	"time"
)

// Schedule is the delay before each retry, indexed by how many attempts have already
// failed: after the first failure wait Schedule[0], after the second Schedule[1], and
// so on. When failures outnumber the entries the delivery is given up (dead-lettered).
type Schedule []time.Duration

// DefaultSchedule retries six times over roughly thirty-five hours — quickly at first,
// for a receiver that blinked, then rarely, for one that is down for the night — and
// then stops. A receiver that is unreachable for that long is told the rest when it
// asks; it is not hammered.
var DefaultSchedule = Schedule{
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	8 * time.Hour,
	24 * time.Hour,
}

// Delay returns how long to wait after the given number of failed attempts (1-based),
// and false when the schedule is exhausted.
func (s Schedule) Delay(failedAttempts int) (time.Duration, bool) {
	if failedAttempts < 1 || failedAttempts > len(s) {
		return 0, false
	}

	return s[failedAttempts-1], true
}

// Jitter perturbs a delay so many deliveries that failed together do not retry
// together. Nil means [DefaultJitter].
type Jitter func(time.Duration) time.Duration

// DefaultJitter spreads a delay uniformly within ±10 % of itself.
func DefaultJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	spread := float64(d) * 0.10
	// Non-cryptographic randomness is the right tool here: the jitter only de-synchronises
	// retries, it protects nothing.
	offset := (rand.Float64()*2 - 1) * spread //nolint:gosec // see the line above

	return d + time.Duration(offset)
}

// NoJitter returns the delay unchanged — for tests and for hosts that schedule
// deterministically.
func NoJitter(d time.Duration) time.Duration { return d }

func (j Jitter) apply(d time.Duration) time.Duration {
	if j == nil {
		return DefaultJitter(d)
	}

	return j(d)
}

// Outcome is what one delivery attempt established.
type Outcome int

const (
	// OutcomeDelivered — the receiver answered 2xx: done.
	OutcomeDelivered Outcome = iota
	// OutcomeRetry — the receiver was unreachable, timed out, asked for a pause (429) or
	// failed on its side (5xx): try again on the schedule.
	OutcomeRetry
	// OutcomeDrop — the receiver refused the delivery as such (any other 4xx): retrying
	// the same bytes would get the same answer, so it is not tried again.
	OutcomeDrop
)

// String names the outcome.
func (o Outcome) String() string {
	switch o {
	case OutcomeDelivered:
		return "delivered"
	case OutcomeRetry:
		return "retry"
	case OutcomeDrop:
		return "drop"
	default:
		return "unknown"
	}
}

// Classify decides an attempt's outcome from the HTTP status and the transport error.
// A transport error (no response at all) always means retry; a response is judged by
// its status alone.
func Classify(status int, err error) Outcome {
	if err != nil {
		return OutcomeRetry
	}
	switch {
	case status >= 200 && status < 300:
		return OutcomeDelivered
	case status == 429, status >= 500:
		return OutcomeRetry
	default:
		return OutcomeDrop
	}
}

// isTimeout reports whether a transport error was a timeout, for the attempt record.
func isTimeout(err error) bool {
	var ne net.Error

	return errors.As(err, &ne) && ne.Timeout()
}
