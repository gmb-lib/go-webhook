package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gmb-lib/go-platform-kit/propagation"
)

// Dispatcher is the seam a host publishes through. Publish records an event and
// creates one delivery per matching subscription of the event's client; the deliveries
// are sent by a [Worker], not by Publish, so publishing never waits on a receiver.
// Subscribe registers or replaces an endpoint.
//
// [InProcess] implements it over a Store in the same process. A host that later moves
// delivery elsewhere replaces this one implementation with a client of that service;
// its own calls, and every receiver's contract, stay the same.
type Dispatcher interface {
	Publish(ctx context.Context, ev Event) ([]Delivery, error)
	Subscribe(ctx context.Context, sub Subscription) error
}

// InProcess is the Dispatcher that writes to a Store directly.
type InProcess struct {
	Store Store
	// Clock and NewID are injectable for tests and for hosts with their own schemes;
	// nil means the package defaults.
	Clock Clock
	NewID func() string
}

func (p *InProcess) id() string {
	if p.NewID == nil {
		return NewID()
	}

	return p.NewID()
}

// Publish implements Dispatcher. An event with no matching subscription is still
// stored (a host may add an endpoint later and ask what it missed) and returns an
// empty delivery list, not an error. An event without an ID gets one.
func (p *InProcess) Publish(ctx context.Context, ev Event) ([]Delivery, error) {
	if p.Store == nil {
		return nil, errors.New("webhook: InProcess.Store is nil")
	}
	if ev.ID == "" {
		ev.ID = p.id()
	}
	now := p.Clock.now()
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = now
	}
	if err := p.Store.SaveEvent(ctx, ev); err != nil {
		return nil, fmt.Errorf("webhook: save event: %w", err)
	}
	subs, err := p.Store.Subscriptions(ctx, ev.ClientID)
	if err != nil {
		return nil, fmt.Errorf("webhook: list subscriptions: %w", err)
	}
	var out []Delivery
	for _, s := range subs {
		if !s.Matches(ev.Type) {
			continue
		}
		out = append(out, Delivery{
			ID:             p.id(),
			EventID:        ev.ID,
			SubscriptionID: s.ID,
			Status:         StatusPending,
			NextAttemptAt:  now,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}
	if len(out) == 0 {
		return nil, nil
	}
	if err := p.Store.Enqueue(ctx, out); err != nil {
		return nil, fmt.Errorf("webhook: enqueue: %w", err)
	}

	return out, nil
}

// Subscribe implements Dispatcher.
func (p *InProcess) Subscribe(ctx context.Context, sub Subscription) error {
	if p.Store == nil {
		return errors.New("webhook: InProcess.Store is nil")
	}
	if sub.ID == "" {
		sub.ID = p.id()
	}
	if sub.EndpointURL == "" {
		return errors.New("webhook: subscription needs an endpoint URL")
	}

	return p.Store.SaveSubscription(ctx, sub)
}

// Worker sends due deliveries and records what happened to each. Run it in one
// goroutine per process, or in several processes against a Store that claims rows.
type Worker struct {
	Store Store
	// Client sends the requests. Nil means an http.Client with Timeout; a host that
	// needs a proxy, custom TLS or a stricter dialer supplies its own. The Timeout below
	// still applies per attempt through the request context.
	Client *http.Client
	// Timeout bounds one attempt; a receiver that has not answered by then is retried.
	// Zero means 10 seconds.
	Timeout time.Duration
	// Schedule and Jitter shape the retries; nil means the package defaults.
	Schedule Schedule
	Jitter   Jitter
	// Headers names the headers sent; zero fields take [DefaultHeaders].
	Headers Headers
	// UserAgent identifies the sender; empty means "go-webhook/<Version>".
	UserAgent string
	// Clock is the worker's time source; nil means the wall clock.
	Clock Clock
	// MaxBodyDrain caps how much of a receiver's response body is read before the
	// connection is reused; zero means 64 KiB. The body is never interpreted.
	MaxBodyDrain int64
}

func (w *Worker) client() *http.Client {
	if w.Client != nil {
		return w.Client
	}

	return &http.Client{Timeout: w.timeout()}
}

func (w *Worker) timeout() time.Duration {
	if w.Timeout > 0 {
		return w.Timeout
	}

	return 10 * time.Second
}

func (w *Worker) schedule() Schedule {
	if w.Schedule != nil {
		return w.Schedule
	}

	return DefaultSchedule
}

func (w *Worker) userAgent() string {
	if w.UserAgent != "" {
		return w.UserAgent
	}

	return "go-webhook/" + Version
}

func (w *Worker) drain() int64 {
	if w.MaxBodyDrain > 0 {
		return w.MaxBodyDrain
	}

	return 64 << 10
}

// RunOnce sends up to limit due deliveries and returns how many it attempted. A
// failure to read or write the Store is returned; a receiver's refusal is not — that is
// recorded on the delivery. Zero limit means 100.
func (w *Worker) RunOnce(ctx context.Context, limit int) (int, error) {
	if w.Store == nil {
		return 0, errors.New("webhook: Worker.Store is nil")
	}
	if limit <= 0 {
		limit = 100
	}
	now := w.Clock.now()
	due, err := w.Store.Due(ctx, now, limit)
	if err != nil {
		return 0, fmt.Errorf("webhook: due: %w", err)
	}
	for i := range due {
		if err := w.attempt(ctx, &due[i]); err != nil {
			return i, err
		}
	}

	return len(due), nil
}

// Run calls RunOnce every interval until ctx ends. Store errors are returned to the
// caller through the errs channel when non-nil, and never stop the loop.
func (w *Worker) Run(ctx context.Context, interval time.Duration, limit int, errs chan<- error) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := w.RunOnce(ctx, limit); err != nil && errs != nil {
			select {
			case errs <- err:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// attempt sends one delivery and records the result. Only Store failures are errors.
func (w *Worker) attempt(ctx context.Context, d *Delivery) error {
	sub, err := w.Store.Subscription(ctx, d.SubscriptionID)
	if err != nil {
		return fmt.Errorf("webhook: subscription %s: %w", d.SubscriptionID, err)
	}
	ev, err := w.Store.Event(ctx, d.EventID)
	if err != nil {
		return fmt.Errorf("webhook: event %s: %w", d.EventID, err)
	}
	now := w.Clock.now()

	// A subscription disabled after the event was queued is not sent to: the receiver
	// asked to stop. Recorded as dropped so the host can see it was not lost silently.
	if !sub.Enabled {
		d.Attempt(OutcomeDrop, 0, "subscription disabled", now, w.schedule(), w.Jitter)

		return w.Store.Update(ctx, *d)
	}

	status, errText := w.send(ctx, sub, ev, *d, now)
	var transportErr error
	if status == 0 {
		transportErr = errors.New(errText)
	}
	d.Attempt(Classify(status, transportErr), status, errText, now, w.schedule(), w.Jitter)

	return w.Store.Update(ctx, *d)
}

// send performs one HTTP POST. It returns the response status (0 when no response was
// received) and a short description of what went wrong, empty on success.
func (w *Worker) send(ctx context.Context, sub Subscription, ev Event, d Delivery, now time.Time) (int, string) {
	secrets := sub.ActiveSecrets(now)
	if len(secrets) == 0 {
		// Sending unsigned would teach receivers to accept unsigned; refusing is safer.
		return 0, "no active signing secret on the subscription"
	}
	body := []byte(ev.Payload)
	ctx, cancel := context.WithTimeout(ctx, w.timeout())
	defer cancel()
	// The causing act's correlation id rides on the context as well as on the header, so
	// a host-supplied Client whose transport reads the platform's context sees the same
	// thread (an empty id leaves the context as it is).
	ctx = propagation.WithCorrelationID(ctx, ev.CorrelationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.EndpointURL, bytes.NewReader(body))
	if err != nil {
		return 0, "build request: " + err.Error()
	}
	h := w.Headers.withDefaults()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", w.userAgent())
	req.Header.Set(h.Signature, SignatureHeader(now, body, secrets...))
	req.Header.Set(h.Event, ev.Type)
	req.Header.Set(h.Delivery, d.ID)
	// Every attempt of a delivery carries the same correlation id: a retry continues the
	// thread the causing act started. An event that no request caused carries none.
	if ev.CorrelationID != "" {
		req.Header.Set(propagation.HeaderCorrelationID, ev.CorrelationID)
	}

	resp, err := w.client().Do(req)
	if err != nil {
		if isTimeout(err) || errors.Is(err, context.DeadlineExceeded) {
			return 0, "timeout after " + w.timeout().String()
		}

		return 0, "send: " + err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, w.drain()))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, ""
	}

	return resp.StatusCode, "receiver answered " + resp.Status
}
