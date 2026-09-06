package webhook

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrNotFound is returned by a Store for an unknown subscription, event or delivery.
var ErrNotFound = errors.New("webhook: not found")

// Store is where subscriptions, events and deliveries live. A host implements it over
// its own database (sql/ carries the table shape); [MemoryStore] is the reference
// implementation and the one tests use. Every method must be safe for concurrent use.
type Store interface {
	// SaveSubscription creates or replaces a subscription by ID.
	SaveSubscription(ctx context.Context, sub Subscription) error
	// Subscription returns one subscription, or ErrNotFound.
	Subscription(ctx context.Context, id string) (Subscription, error)
	// Subscriptions returns every subscription of a client, enabled or not.
	Subscriptions(ctx context.Context, clientID string) ([]Subscription, error)

	// SaveEvent stores an event so its payload can be sent later, and re-sent.
	SaveEvent(ctx context.Context, ev Event) error
	// Event returns one event, or ErrNotFound.
	Event(ctx context.Context, id string) (Event, error)

	// Enqueue stores new deliveries.
	Enqueue(ctx context.Context, deliveries []Delivery) error
	// Due returns up to limit deliveries whose next attempt is at or before now, oldest
	// first, excluding terminal ones. A relational implementation should claim them
	// (`FOR UPDATE SKIP LOCKED` or an equivalent) so two workers do not send the same
	// delivery twice.
	Due(ctx context.Context, now time.Time, limit int) ([]Delivery, error)
	// Update replaces a delivery by ID.
	Update(ctx context.Context, d Delivery) error
	// ByEvent returns every delivery of an event, so a host can show an integrator
	// what happened to it on each endpoint.
	ByEvent(ctx context.Context, eventID string) ([]Delivery, error)
}

// MemoryStore is a Store in process memory: complete, concurrency-safe, and forgotten
// on restart. For tests, and for hosts whose deliveries may be lost with the process.
type MemoryStore struct {
	mu         sync.Mutex
	subs       map[string]Subscription
	events     map[string]Event
	deliveries map[string]Delivery
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		subs:       map[string]Subscription{},
		events:     map[string]Event{},
		deliveries: map[string]Delivery{},
	}
}

// SaveSubscription implements Store.
func (m *MemoryStore) SaveSubscription(_ context.Context, sub Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[sub.ID] = sub

	return nil
}

// Subscription implements Store.
func (m *MemoryStore) Subscription(_ context.Context, id string) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok {
		return Subscription{}, ErrNotFound
	}

	return s, nil
}

// Subscriptions implements Store.
func (m *MemoryStore) Subscriptions(_ context.Context, clientID string) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Subscription
	for _, s := range m.subs {
		if s.ClientID == clientID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}

// SaveEvent implements Store.
func (m *MemoryStore) SaveEvent(_ context.Context, ev Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events[ev.ID] = ev

	return nil
}

// Event implements Store.
func (m *MemoryStore) Event(_ context.Context, id string) (Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[id]
	if !ok {
		return Event{}, ErrNotFound
	}

	return e, nil
}

// Enqueue implements Store.
func (m *MemoryStore) Enqueue(_ context.Context, deliveries []Delivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range deliveries {
		m.deliveries[d.ID] = d
	}

	return nil
}

// Due implements Store.
func (m *MemoryStore) Due(_ context.Context, now time.Time, limit int) ([]Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Delivery
	for _, d := range m.deliveries {
		if d.Due(now) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].NextAttemptAt.Equal(out[j].NextAttemptAt) {
			return out[i].NextAttemptAt.Before(out[j].NextAttemptAt)
		}

		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return out, nil
}

// Update implements Store.
func (m *MemoryStore) Update(_ context.Context, d Delivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deliveries[d.ID]; !ok {
		return ErrNotFound
	}
	m.deliveries[d.ID] = d

	return nil
}

// ByEvent implements Store.
func (m *MemoryStore) ByEvent(_ context.Context, eventID string) ([]Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Delivery
	for _, d := range m.deliveries {
		if d.EventID == eventID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}
