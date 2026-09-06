package webhook

import "time"

// Status is where a delivery stands. The receiver-visible vocabulary: a host that
// exposes delivery state to its integrators shows these words.
type Status string

const (
	// StatusPending — created, not yet attempted.
	StatusPending Status = "pending"
	// StatusRetrying — attempted and failed in a retryable way; NextAttemptAt says when.
	StatusRetrying Status = "retrying"
	// StatusDelivered — the receiver answered 2xx. Terminal.
	StatusDelivered Status = "delivered"
	// StatusDeadLetter — every scheduled retry failed; given up. Terminal. Visible to
	// the host so it can show the integrator what it missed.
	StatusDeadLetter Status = "dead-letter"
	// StatusDropped — the receiver refused the delivery (a non-retryable 4xx). Terminal.
	StatusDropped Status = "dropped"
)

// Terminal reports whether no further attempt will be made.
func (s Status) Terminal() bool {
	return s == StatusDelivered || s == StatusDeadLetter || s == StatusDropped
}

// Delivery is one event bound for one subscription, and the record of every attempt
// to get it there. One event fans out into one Delivery per matching subscription.
type Delivery struct {
	ID             string
	EventID        string
	SubscriptionID string
	Status         Status
	// Attempts is how many times a send was tried, successful or not.
	Attempts int
	// NextAttemptAt is when the worker may try again; meaningful while Status is
	// pending or retrying.
	NextAttemptAt  time.Time
	LastHTTPStatus int
	LastError      string
	LastAttemptAt  time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Attempt records the result of one send and moves the delivery to its next state:
// delivered, retrying (with the next instant from the schedule), dead-letter when the
// schedule is exhausted, or dropped. It is the only place a delivery's status changes
// after creation, so the state machine is readable in one screen.
func (d *Delivery) Attempt(outcome Outcome, httpStatus int, errText string, now time.Time, s Schedule, j Jitter) {
	d.Attempts++
	d.LastHTTPStatus = httpStatus
	d.LastError = errText
	d.LastAttemptAt = now
	d.UpdatedAt = now

	switch outcome {
	case OutcomeDelivered:
		d.Status = StatusDelivered
		d.NextAttemptAt = time.Time{}
	case OutcomeDrop:
		d.Status = StatusDropped
		d.NextAttemptAt = time.Time{}
	case OutcomeRetry:
		delay, ok := s.Delay(d.Attempts)
		if !ok {
			d.Status = StatusDeadLetter
			d.NextAttemptAt = time.Time{}

			return
		}
		d.Status = StatusRetrying
		d.NextAttemptAt = now.Add(j.apply(delay))
	}
}

// Due reports whether the worker should attempt this delivery at the given instant.
func (d Delivery) Due(now time.Time) bool {
	if d.Status.Terminal() {
		return false
	}

	return !d.NextAttemptAt.After(now)
}
