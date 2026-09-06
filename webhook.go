// Package webhook delivers events to HTTP endpoints that other systems register, the
// way a payment provider or a source-code host tells an integrator that something
// happened: one signed POST per event, retried on a fixed schedule when the receiver
// does not answer, and given up after a bounded time so a dead endpoint never keeps a
// queue alive forever.
//
// The package fixes everything a receiver can observe — the headers, the signature
// scheme, what counts as delivered, the retry schedule, the at-least-once promise — and
// leaves to the host what only the host knows: the event body, where subscriptions and
// deliveries are stored, and when the worker runs. A host wires three things:
//
//   - a [Store], where subscriptions, events and delivery attempts live (a [MemoryStore]
//     is included for tests and small deployments; sql/ carries the table shape for a
//     relational one);
//   - a [Dispatcher], through which the host publishes events and registers
//     subscriptions — [InProcess] is the included implementation;
//   - a [Worker], which sends what is due and records what happened.
//
// Because the receiver-facing contract lives here and nowhere else, a host can move
// delivery to another process later — the same package, another host — and no
// receiver sees a different call.
package webhook

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Version is the library version, sent as the User-Agent when a host does not set one.
const Version = "0.1.0"

// Event is one thing that happened. ID must be stable across delivery attempts: a
// receiver de-duplicates on it. Payload is the exact body sent — this package neither
// reads nor reshapes it, so the host owns the wire schema of its events.
type Event struct {
	ID         string
	Type       string
	OccurredAt time.Time
	// ClientID names the receiver-side account the event belongs to; subscriptions
	// are matched within it, so one host can serve many receivers without one seeing
	// another's events.
	ClientID string
	Payload  json.RawMessage
}

// Subscription is one registered endpoint. EventTypes empty means every type.
type Subscription struct {
	ID          string
	ClientID    string
	EndpointURL string
	// Secrets are the keys the receiver verifies with, current first. During a
	// rotation two are active and every delivery is signed with both, so the receiver
	// can switch at its own pace; an expired secret is no longer used.
	Secrets    []Secret
	EventTypes []string
	Enabled    bool
}

// Secret is one signing key with an optional expiry (zero = does not expire).
type Secret struct {
	Value     []byte
	ExpiresAt time.Time
}

// Active reports whether the secret may still sign at the given instant.
func (s Secret) Active(now time.Time) bool {
	return len(s.Value) > 0 && (s.ExpiresAt.IsZero() || now.Before(s.ExpiresAt))
}

// Matches reports whether the subscription wants events of the given type.
func (s Subscription) Matches(eventType string) bool {
	if !s.Enabled {
		return false
	}
	if len(s.EventTypes) == 0 {
		return true
	}
	for _, t := range s.EventTypes {
		if t == eventType {
			return true
		}
	}

	return false
}

// ActiveSecrets returns the secrets that may sign at the given instant, current first.
func (s Subscription) ActiveSecrets(now time.Time) [][]byte {
	out := make([][]byte, 0, len(s.Secrets))
	for _, sec := range s.Secrets {
		if sec.Active(now) {
			out = append(out, sec.Value)
		}
	}

	return out
}

// Headers names the HTTP headers a delivery carries. A host may rename them to fit
// its product; the receiver-side documentation must then say so.
type Headers struct {
	// Signature carries `t=<unix seconds>,v1=<hex HMAC-SHA256>` — see [SignatureHeader].
	Signature string
	// Event carries the event type, so a receiver can route before parsing the body.
	Event string
	// Delivery carries the delivery attempt id — unique per attempt, while the event id
	// inside the body is the same across attempts.
	Delivery string
}

// DefaultHeaders are the header names used when a host sets none.
var DefaultHeaders = Headers{
	Signature: "Webhook-Signature",
	Event:     "Webhook-Event",
	Delivery:  "Webhook-Delivery",
}

func (h Headers) withDefaults() Headers {
	if h.Signature == "" {
		h.Signature = DefaultHeaders.Signature
	}
	if h.Event == "" {
		h.Event = DefaultHeaders.Event
	}
	if h.Delivery == "" {
		h.Delivery = DefaultHeaders.Delivery
	}

	return h
}

// Clock returns the current instant. Tests and hosts with their own time source
// inject one; nil means [time.Now].
type Clock func() time.Time

func (c Clock) now() time.Time {
	if c == nil {
		return time.Now()
	}

	return c()
}

// NewID returns a time-ordered, collision-resistant identifier: 8 bytes of Unix
// nanoseconds followed by 8 random bytes, hex-encoded (32 characters). Hosts with
// their own identifier scheme inject theirs instead.
func NewID() string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixNano())) //nolint:gosec // a timestamp, not a conversion of user input
	if _, err := rand.Read(b[8:]); err != nil {
		panic("webhook: crypto/rand unavailable: " + err.Error())
	}

	return hex.EncodeToString(b[:])
}
