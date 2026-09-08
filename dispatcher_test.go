package webhook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmb-lib/go-platform-kit/propagation"
)

// fakeClock is a settable time source shared by the dispatcher and the worker.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// receiver is a test endpoint that answers a scripted sequence of statuses and
// records what it was sent.
type receiver struct {
	mu       sync.Mutex
	statuses []int
	calls    []receivedCall
	served   atomic.Int32
}

type receivedCall struct {
	headers http.Header
	body    []byte
}

func (r *receiver) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		i := int(r.served.Add(1)) - 1
		status := http.StatusOK
		if i < len(r.statuses) {
			status = r.statuses[i]
		}
		r.calls = append(r.calls, receivedCall{headers: req.Header.Clone(), body: body})
		r.mu.Unlock()
		if req.Method != http.MethodPost {
			t.Errorf("method %s", req.Method)
		}
		w.WriteHeader(status)
	}
}

func TestPublishFansOutToMatchingSubscriptionsOnly(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fakeClock{t: time.Unix(1757170123, 0)}
	n := 0
	disp := &InProcess{Store: store, Clock: clock.Now, NewID: func() string { n++; return "id" + string(rune('0'+n)) }}

	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(disp.Subscribe(ctx, Subscription{ID: "all", ClientID: "acme", EndpointURL: "https://a/all", Enabled: true}))
	must(disp.Subscribe(ctx, Subscription{ID: "onlyx", ClientID: "acme", EndpointURL: "https://a/x", Enabled: true, EventTypes: []string{"x"}}))
	must(disp.Subscribe(ctx, Subscription{ID: "off", ClientID: "acme", EndpointURL: "https://a/off", Enabled: false}))
	must(disp.Subscribe(ctx, Subscription{ID: "other", ClientID: "someone-else", EndpointURL: "https://o/all", Enabled: true}))
	if err := disp.Subscribe(ctx, Subscription{ID: "bad", ClientID: "acme"}); err == nil {
		t.Fatal("a subscription without an endpoint must be refused")
	}

	ds, err := disp.Publish(ctx, Event{ClientID: "acme", Type: "y", Payload: []byte(`{}`)})
	must(err)
	if len(ds) != 1 || ds[0].SubscriptionID != "all" || ds[0].Status != StatusPending || !ds[0].NextAttemptAt.Equal(clock.Now()) {
		t.Fatalf("type y: %+v", ds)
	}
	if ds[0].EventID == "" {
		t.Fatal("the event must have been given an id")
	}

	ds, err = disp.Publish(ctx, Event{ID: "ev-x", ClientID: "acme", Type: "x", Payload: []byte(`{}`)})
	must(err)
	if len(ds) != 2 {
		t.Fatalf("type x reaches 'all' and 'onlyx': %+v", ds)
	}

	ds, err = disp.Publish(ctx, Event{ID: "ev-none", ClientID: "nobody", Type: "x", Payload: []byte(`{}`)})
	must(err)
	if len(ds) != 0 {
		t.Fatalf("no subscription, no delivery, no error: %+v", ds)
	}
	if _, err := store.Event(ctx, "ev-none"); err != nil {
		t.Fatal("the event is still stored for a subscription added later")
	}
}

func TestWorkerRetriesThenDeliversWithVerifiableSignature(t *testing.T) {
	ctx := context.Background()
	rcv := &receiver{statuses: []int{503, 500, 200}}
	srv := httptest.NewServer(rcv.handler(t))
	defer srv.Close()

	store := NewMemoryStore()
	clock := &fakeClock{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	disp := &InProcess{Store: store, Clock: clock.Now}
	current, previous := testSecret("cur"), testSecret("prev")
	if err := disp.Subscribe(ctx, Subscription{
		ID: "s", ClientID: "acme", EndpointURL: srv.URL, Enabled: true,
		Secrets: []Secret{{Value: current}, {Value: previous, ExpiresAt: clock.Now().Add(time.Hour)}},
	}); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"eventId":"e1","type":"signing-request.completed","sequence":3}`)
	const correlation = "01K5VB0E-the-causing-request"
	ds, err := disp.Publish(ctx, Event{ID: "e1", ClientID: "acme", Type: "signing-request.completed", Payload: payload, CorrelationID: correlation})
	if err != nil || len(ds) != 1 {
		t.Fatalf("publish: %v %+v", err, ds)
	}

	w := &Worker{Store: store, Clock: clock.Now, Jitter: NoJitter, Headers: Headers{Signature: "Acme-Signature"}, UserAgent: "acme-api/1.0"}

	// Attempt 1: 503 → retrying, next in 1 minute.
	if n, err := w.RunOnce(ctx, 0); err != nil || n != 1 {
		t.Fatalf("run 1: n=%d err=%v", n, err)
	}
	d := mustDelivery(t, store, ds[0].ID)
	if d.Status != StatusRetrying || d.Attempts != 1 || d.LastHTTPStatus != 503 || !d.NextAttemptAt.Equal(clock.Now().Add(time.Minute)) {
		t.Fatalf("after 503: %+v", d)
	}
	// Not due yet: nothing is sent.
	if n, _ := w.RunOnce(ctx, 0); n != 0 {
		t.Fatalf("sent before its time: %d", n)
	}

	// Attempt 2 at +1m: 500 → retrying, next in 5 minutes.
	clock.Advance(time.Minute)
	if n, _ := w.RunOnce(ctx, 0); n != 1 {
		t.Fatal("second attempt expected")
	}
	d = mustDelivery(t, store, ds[0].ID)
	if d.Status != StatusRetrying || d.Attempts != 2 || !d.NextAttemptAt.Equal(clock.Now().Add(5*time.Minute)) {
		t.Fatalf("after 500: %+v", d)
	}

	// Attempt 3 at +6m: 200 → delivered.
	clock.Advance(5 * time.Minute)
	if n, _ := w.RunOnce(ctx, 0); n != 1 {
		t.Fatal("third attempt expected")
	}
	d = mustDelivery(t, store, ds[0].ID)
	if d.Status != StatusDelivered || d.Attempts != 3 || d.LastHTTPStatus != 200 || d.LastError != "" {
		t.Fatalf("after 200: %+v", d)
	}
	if n, _ := w.RunOnce(ctx, 0); n != 0 {
		t.Fatal("a delivered delivery is never sent again")
	}

	// What the receiver saw on the successful call: the body byte-identical, the
	// headers named by the host, a signature it can verify with EITHER secret, the
	// same event id every time and a different delivery id? — the delivery id is the
	// same row here (one delivery, three attempts), so it is stable too.
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	if len(rcv.calls) != 3 {
		t.Fatalf("receiver saw %d calls", len(rcv.calls))
	}
	last := rcv.calls[2]
	if string(last.body) != string(payload) {
		t.Fatalf("body altered: %s", last.body)
	}
	if last.headers.Get("Content-Type") != "application/json" || last.headers.Get("User-Agent") != "acme-api/1.0" {
		t.Fatalf("headers: %v", last.headers)
	}
	if last.headers.Get(DefaultHeaders.Event) != "signing-request.completed" || last.headers.Get(DefaultHeaders.Delivery) != ds[0].ID {
		t.Fatalf("routing headers: %v", last.headers)
	}
	sig := last.headers.Get("Acme-Signature")
	if sig == "" || last.headers.Get(DefaultHeaders.Signature) != "" {
		t.Fatalf("the host's signature header name must be used: %v", last.headers)
	}
	sentAt := clock.Now()
	for name, held := range map[string][][]byte{"current": {current}, "previous": {previous}} {
		if err := Verify(sig, last.body, held, sentAt, 5*time.Minute); err != nil {
			t.Errorf("verify with %s secret: %v", name, err)
		}
	}
	if err := Verify(sig, last.body, [][]byte{testSecret("stranger")}, sentAt, 5*time.Minute); err == nil {
		t.Fatal("a stranger's secret must not verify")
	}
	// The correlation id of the act that caused the event travels on EVERY attempt, the
	// same value each time — a retry is the same thread, not a new one.
	for i, call := range rcv.calls {
		if got := call.headers.Get(propagation.HeaderCorrelationID); got != correlation {
			t.Fatalf("attempt %d: %s = %q, want %q", i+1, propagation.HeaderCorrelationID, got, correlation)
		}
	}
}

// An event that no request caused — background work — has no correlation id, and the
// delivery then carries no header at all rather than an empty one.
func TestWorkerSendsNoCorrelationHeaderWhenTheEventHasNone(t *testing.T) {
	ctx := context.Background()
	rcv := &receiver{}
	srv := httptest.NewServer(rcv.handler(t))
	defer srv.Close()
	store := NewMemoryStore()
	clock := &fakeClock{t: time.Unix(1757170123, 0)}
	disp := &InProcess{Store: store, Clock: clock.Now}
	_ = disp.Subscribe(ctx, Subscription{ID: "s", ClientID: "c", EndpointURL: srv.URL, Enabled: true, Secrets: []Secret{{Value: testSecret("k")}}})
	ds, _ := disp.Publish(ctx, Event{ID: "e", ClientID: "c", Type: "t", Payload: []byte(`{}`)})
	w := &Worker{Store: store, Clock: clock.Now, Jitter: NoJitter}
	if n, err := w.RunOnce(ctx, 0); err != nil || n != 1 {
		t.Fatalf("run: n=%d err=%v", n, err)
	}
	if d := mustDelivery(t, store, ds[0].ID); d.Status != StatusDelivered {
		t.Fatalf("delivered expected: %+v", d)
	}
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	if len(rcv.calls) != 1 {
		t.Fatalf("receiver saw %d calls", len(rcv.calls))
	}
	if vals := rcv.calls[0].headers.Values(propagation.HeaderCorrelationID); len(vals) != 0 {
		t.Fatalf("no correlation id on the event, yet the header was sent: %q", vals)
	}
}

func TestWorkerDropsOnClientErrorAndDeadLettersWhenExhausted(t *testing.T) {
	ctx := context.Background()
	rcv := &receiver{statuses: []int{404}}
	srv := httptest.NewServer(rcv.handler(t))
	defer srv.Close()
	store := NewMemoryStore()
	clock := &fakeClock{t: time.Unix(1757170123, 0)}
	disp := &InProcess{Store: store, Clock: clock.Now}
	_ = disp.Subscribe(ctx, Subscription{ID: "s", ClientID: "c", EndpointURL: srv.URL, Enabled: true, Secrets: []Secret{{Value: testSecret("k")}}})
	ds, _ := disp.Publish(ctx, Event{ID: "e", ClientID: "c", Type: "t", Payload: []byte(`{}`)})
	w := &Worker{Store: store, Clock: clock.Now, Jitter: NoJitter, Schedule: Schedule{time.Second, time.Second}}

	_, _ = w.RunOnce(ctx, 0)
	if d := mustDelivery(t, store, ds[0].ID); d.Status != StatusDropped || d.Attempts != 1 {
		t.Fatalf("404 must drop once: %+v", d)
	}

	// A receiver that is down: connection refused every time, two-entry schedule → dead-letter on the third.
	srv.Close()
	ds2, _ := disp.Publish(ctx, Event{ID: "e2", ClientID: "c", Type: "t", Payload: []byte(`{}`)})
	for range 3 {
		_, _ = w.RunOnce(ctx, 0)
		clock.Advance(time.Second)
	}
	d := mustDelivery(t, store, ds2[0].ID)
	if d.Status != StatusDeadLetter || d.Attempts != 3 || d.LastHTTPStatus != 0 || d.LastError == "" {
		t.Fatalf("unreachable receiver must dead-letter after the schedule: %+v", d)
	}
}

func TestWorkerRefusesToSendUnsignedAndHonoursDisable(t *testing.T) {
	ctx := context.Background()
	rcv := &receiver{}
	srv := httptest.NewServer(rcv.handler(t))
	defer srv.Close()
	store := NewMemoryStore()
	clock := &fakeClock{t: time.Unix(1757170123, 0)}
	disp := &InProcess{Store: store, Clock: clock.Now}

	// No active secret: never sent, retried on the schedule (the host will fix the subscription).
	_ = disp.Subscribe(ctx, Subscription{ID: "nosecret", ClientID: "c", EndpointURL: srv.URL, Enabled: true,
		Secrets: []Secret{{Value: testSecret("x"), ExpiresAt: clock.Now().Add(-time.Second)}}})
	ds, _ := disp.Publish(ctx, Event{ID: "e", ClientID: "c", Type: "t", Payload: []byte(`{}`)})
	w := &Worker{Store: store, Clock: clock.Now, Jitter: NoJitter}
	_, _ = w.RunOnce(ctx, 0)
	if d := mustDelivery(t, store, ds[0].ID); d.Status != StatusRetrying || rcv.served.Load() != 0 {
		t.Fatalf("an unsigned delivery must not leave: %+v served=%d", d, rcv.served.Load())
	}

	// Disabled after queueing: dropped, not sent.
	_ = disp.Subscribe(ctx, Subscription{ID: "s", ClientID: "d", EndpointURL: srv.URL, Enabled: true, Secrets: []Secret{{Value: testSecret("k")}}})
	ds2, _ := disp.Publish(ctx, Event{ID: "e2", ClientID: "d", Type: "t", Payload: []byte(`{}`)})
	_ = disp.Subscribe(ctx, Subscription{ID: "s", ClientID: "d", EndpointURL: srv.URL, Enabled: false, Secrets: []Secret{{Value: testSecret("k")}}})
	_, _ = w.RunOnce(ctx, 0)
	if d := mustDelivery(t, store, ds2[0].ID); d.Status != StatusDropped || rcv.served.Load() != 0 {
		t.Fatalf("a disabled subscription is not sent to: %+v served=%d", d, rcv.served.Load())
	}
}

// mustDelivery reads one delivery straight out of the MemoryStore's map — the Store
// interface has no by-id read on purpose (a host never needs one), but a test does.
func mustDelivery(t *testing.T, s Store, id string) Delivery {
	t.Helper()
	m, ok := s.(*MemoryStore)
	if !ok {
		t.Fatal("test store must be a MemoryStore")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, found := m.deliveries[id]
	if !found {
		t.Fatalf("delivery %s not in store", id)
	}

	return d
}
