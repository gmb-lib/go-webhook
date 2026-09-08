# go-webhook

Signed, retried HTTP notifications from your system to endpoints that other systems register — the way
a payment provider or a source-code host tells an integrator that something happened. One `POST` per
event, a signature the receiver can check, a fixed retry schedule when the receiver does not answer,
and a bounded give-up so a dead endpoint never keeps a queue alive forever.

```
go get github.com/gmb-lib/go-webhook
```

The library fixes everything a **receiver** can observe and leaves to the **host** what only the host
knows: the event body, where things are stored, when the worker runs. It builds on
[go-platform-kit](https://github.com/gmb-lib/go-platform-kit) for the one cross-cutting concern it
shares with every other service and library of the platform — the correlation id and its header name —
and on the standard library for everything else.
Because the receiver-facing contract lives here and nowhere else, a host can later move delivery to
another process — same package, another host — and no receiver sees a different call.

## What a receiver gets

Every delivery is one HTTP `POST` to the registered endpoint URL:

| Header | Value |
|---|---|
| `Content-Type` | `application/json` |
| `Webhook-Signature` | `t=<unix seconds>,v1=<hex HMAC-SHA256>` — two `v1` values while a secret rotation is in progress |
| `Webhook-Event` | the event type, so you can route before parsing the body |
| `Webhook-Delivery` | the delivery id — one per event per endpoint, the same for every attempt of one delivery |
| `X-Correlation-ID` | the correlation id of the act that caused the event (a request a person or another system made), when the host knew one — the same on every attempt; quote it when you ask the host about a delivery. Absent for an event no request caused |
| `User-Agent` | the host's name, or `go-webhook/<version>` |

The body is the host's event, byte for byte; the event id inside it is stable across attempts, so
**de-duplicate on it**. A host may rename the three `Webhook-*` headers; its documentation then says so.
`X-Correlation-ID` is the platform's header and is never renamed.

**The signature** is the lowercase hex HMAC-SHA256, keyed with a secret the host gave you when you
registered, over the bytes `<t> "." <raw body>`. `t` is bound into the signed bytes so a replayed
delivery can be refused once `t` is outside your tolerance window.

**How your endpoint's answer is read:**

| You answer | It means | The library does |
|---|---|---|
| any `2xx` | delivered | nothing more |
| `429`, any `5xx`, or no answer (timeout, connection refused) | try again later | retries after 1 min · 5 min · 30 min · 2 h · 8 h · 24 h (each ±10 % jitter), then gives up (dead-letter) |
| any other `4xx` | you refuse this delivery as such | does not retry (dropped) — the same bytes would get the same answer |

Delivery is **at least once** and **unordered**: a retry can arrive after a later event. Order on
whatever your host puts in the body for that (a sequence number, the occurrence time), never on
arrival.

### Verifying a delivery

```go
import webhook "github.com/gmb-lib/go-webhook"

func handle(w http.ResponseWriter, r *http.Request) {
    body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
    if err != nil { http.Error(w, "read", http.StatusBadRequest); return }

    // secrets: the current one, and the previous one while a rotation is in progress.
    if err := webhook.Verify(r.Header.Get("Webhook-Signature"), body, secrets, time.Now(), 5*time.Minute); err != nil {
        http.Error(w, "signature", http.StatusUnauthorized) // a 4xx: the sender will not retry this one
        return
    }
    // de-duplicate on the event id in the body, then act; answer 2xx only once you have durably recorded it
    w.WriteHeader(http.StatusOK)
}
```

`Verify` parses the header strictly (the one place untrusted bytes are read — it is fuzzed), refuses a
`t` further than the tolerance from now, and compares in constant time against every secret you pass.
`errors.Is` against `ErrSignatureMalformed`, `ErrSignatureStale`, `ErrSignatureMismatch` tells you which.

## Hosting it

Three pieces, each a small interface or struct:

```go
store := webhook.NewMemoryStore()                 // or your own Store over the tables in sql/
disp  := &webhook.InProcess{Store: store}        // the Dispatcher your code publishes through
worker := &webhook.Worker{                       // sends what is due, records what happened
    Store:     store,
    Headers:   webhook.Headers{Signature: "Acme-Signature", Event: "Acme-Event", Delivery: "Acme-Delivery"},
    UserAgent: "acme-api/1.4.0",
    Timeout:   10 * time.Second,                 // per attempt; zero = 10 s
    Schedule:  webhook.DefaultSchedule,          // or your own
}

// registration — once per receiver endpoint
_ = disp.Subscribe(ctx, webhook.Subscription{
    ID: "sub_01", ClientID: "acme", EndpointURL: "https://dms.example/events", Enabled: true,
    Secrets:    []webhook.Secret{{Value: currentSecret}, {Value: previousSecret, ExpiresAt: rotationEnds}},
    EventTypes: []string{"order.completed"},     // empty = every type
})

// publishing — never waits on a receiver; it only writes deliveries. CorrelationID is the
// causing request's (propagation.CorrelationID(ctx) in a handler); leave it empty for
// background work and no header is sent.
_, _ = disp.Publish(ctx, webhook.Event{ID: eventID, ClientID: "acme", Type: "order.completed", Payload: body, CorrelationID: correlationID})

// delivering — one goroutine per process, or several processes against a claiming Store
go worker.Run(ctx, 5*time.Second, 100, errs)
```

**What the host decides:** the event body and its schema; the secrets and their rotation (the library
signs with every active secret it is given, and refuses to send with none); the endpoint URL policy
(https, allowlists — enforce them before `Subscribe`); how long events and deliveries are kept; and
whether the header names carry the host's own brand.

**What the library decides:** the header format, the signature scheme, the outcome rules, the retry
schedule and jitter, the delivery states (`pending · retrying · delivered · dead-letter · dropped`), and
that a subscription disabled after an event was queued is not sent to.

### Storing it

`Store` is five kinds of read and write over subscriptions, events and deliveries; `MemoryStore` is the
complete reference implementation. For a database, [`sql/`](sql/) is the table shape — copy its
migrations into yours and map them, column for column, onto the Go types (`V1` the three tables, `V2` the
event's `correlation_id`).
Secrets are stored as **references** into your secret store, never as values; your `Store` resolves
them when the worker asks. With several workers, claim rows as you read them
(`FOR UPDATE SKIP LOCKED`) so no delivery is sent twice.

### Showing it

`Store.ByEvent` lists every delivery of an event with its attempt record — status, attempts, last HTTP
status, next attempt — so a host can show a receiver what happened to each event on each endpoint,
including the ones it never received (dead-letter) and the ones it refused (dropped).

## Scope / non-goals

- No inbound webhooks: this library sends and gives receivers `Verify`; it does not run a server.
- No transport beyond HTTP `POST` with a JSON body; no compression, no batching.
- No secret management: the host owns generation, storage and rotation; the library only signs.
- No ordering guarantee across events. Sequence numbers belong in the host's body.
- No persistence of its own beyond `MemoryStore`; a durable `Store` is the host's.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security reports go through [SECURITY.md](SECURITY.md), never a
public issue.

## License

MIT — see [LICENSE](LICENSE).
