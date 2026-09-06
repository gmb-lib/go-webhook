package webhook

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStoreDueOrderingLimitAndTerminals(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Unix(1757170123, 0)

	if err := m.Enqueue(ctx, []Delivery{
		{ID: "late", Status: StatusRetrying, NextAttemptAt: now.Add(time.Hour)},
		{ID: "b", Status: StatusPending, NextAttemptAt: now},
		{ID: "a", Status: StatusPending, NextAttemptAt: now},
		{ID: "older", Status: StatusRetrying, NextAttemptAt: now.Add(-time.Minute)},
		{ID: "done", Status: StatusDelivered, NextAttemptAt: now.Add(-time.Hour)},
		{ID: "dead", Status: StatusDeadLetter},
		{ID: "dropped", Status: StatusDropped},
	}); err != nil {
		t.Fatal(err)
	}

	due, err := m.Due(ctx, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(due))
	for i, d := range due {
		ids[i] = d.ID
	}
	want := []string{"older", "a", "b"}
	if len(ids) != len(want) {
		t.Fatalf("due %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("due %v, want %v", ids, want)
		}
	}

	two, _ := m.Due(ctx, now, 2)
	if len(two) != 2 || two[0].ID != "older" || two[1].ID != "a" {
		t.Fatalf("limit: %v", two)
	}

	later, _ := m.Due(ctx, now.Add(2*time.Hour), 0)
	if len(later) != 4 || later[3].ID != "late" {
		t.Fatalf("an hour later the late one is due last: %v", later)
	}
}

func TestMemoryStoreSubscriptionsEventsAndNotFound(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()

	if _, err := m.Subscription(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown subscription: %v", err)
	}
	if _, err := m.Event(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown event: %v", err)
	}
	if err := m.Update(ctx, Delivery{ID: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of an unknown delivery: %v", err)
	}

	_ = m.SaveSubscription(ctx, Subscription{ID: "s2", ClientID: "acme", EndpointURL: "https://a/2"})
	_ = m.SaveSubscription(ctx, Subscription{ID: "s1", ClientID: "acme", EndpointURL: "https://a/1"})
	_ = m.SaveSubscription(ctx, Subscription{ID: "s3", ClientID: "other", EndpointURL: "https://o/3"})
	subs, _ := m.Subscriptions(ctx, "acme")
	if len(subs) != 2 || subs[0].ID != "s1" || subs[1].ID != "s2" {
		t.Fatalf("acme's subscriptions, sorted: %+v", subs)
	}

	_ = m.SaveEvent(ctx, Event{ID: "e1", ClientID: "acme", Type: "x", Payload: []byte(`{"a":1}`)})
	ev, err := m.Event(ctx, "e1")
	if err != nil || string(ev.Payload) != `{"a":1}` {
		t.Fatalf("event round-trip: %v %s", err, ev.Payload)
	}

	_ = m.Enqueue(ctx, []Delivery{{ID: "d2", EventID: "e1"}, {ID: "d1", EventID: "e1"}, {ID: "d9", EventID: "e9"}})
	byEvent, _ := m.ByEvent(ctx, "e1")
	if len(byEvent) != 2 || byEvent[0].ID != "d1" {
		t.Fatalf("by event: %+v", byEvent)
	}
}
