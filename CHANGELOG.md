# Changelog

Notable changes to this library, newest first, per release. Written for whoever hosts the library or
receives its deliveries.

## v0.2.1

Dependency maintenance with one thing to act on: **this library now needs Go 1.27**. No source
changed here and nothing it does behaves differently — a delivery's shape, headers and retry
behaviour are exactly what v0.2.0 shipped.

### Changed

- **The module declares `go 1.27.0`** (was `1.26.6`), so your own module has to be on Go 1.27
  before it can build against this one. A dependency's `go` line does **not** make the go command
  fetch a newer toolchain for you — measured both ways: a consumer whose own `go` directive is
  lower stops with a `requires go >= 1.27.0 (running go 1.26.6)` error, and it stops there with
  `GOTOOLCHAIN` on its `auto` default just as it does under `local`. Raise your own `go` directive
  to `1.27.0` first; from there the go command downloads and uses the 1.27 toolchain by itself, so
  nobody has to install Go by hand. CI that reads `go-version-file: go.mod` follows the bump with
  no workflow edit — a workflow naming a Go version in the YAML needs that line changed.

### Notes

- **`github.com/gmb-lib/go-platform-kit` → v1.11.3** (was v1.11.1) — this library's only
  dependency, taken for its `propagation` package, the one home of the correlation header's name.
  Nothing it takes from there moved; the two releases in between are dependency maintenance and the
  same Go 1.27 requirement.

- The gate is green on the new set: `go mod verify`, `go mod tidy -diff`, build, vet, `gofmt`, and
  `go test -race` with **0 races**; `govulncheck` finds nothing.

## v0.2.0

### Added — the causing act's correlation id travels with the delivery

`Event.CorrelationID` is the correlation id of the request that caused the event, when the host knew
one. It is stored with the event (`MemoryStore` keeps it; a relational `Store` gets the column from
`sql/V2__webhook_event_correlation_id.sql`) and the worker sends it as the platform's `X-Correlation-ID`
header on **every attempt** of every delivery of the event — a retry continues the same thread. An event
with no correlation id (background work) is sent without the header, never with an empty one. The header
name comes from `go-platform-kit`'s `propagation` package, which is now a dependency: the library carries
the platform kit like the platform's other libraries rather than re-declaring a concern the kit owns.

For receivers: a delivery may now carry `X-Correlation-ID`; quote it when asking the host about a
delivery. Nothing else on the wire changes.

### Fixed — the delivery header's documentation

`Headers.Delivery` was documented as "unique per attempt". The worker has always sent the delivery's own
id — one per event per endpoint, the same value on every attempt — and the README said so; the doc
comment now says the same.

## v0.1.0

Initial code.

The receiver-facing contract in one place: one signed `POST` per event with a `t=…,v1=…` HMAC-SHA256
signature header (two `v1` values during a secret rotation), an event-type header and a delivery-id
header; `2xx` is delivered, `429` and `5xx` and a transport failure are retried on the schedule
1 min · 5 min · 30 min · 2 h · 8 h · 24 h (jittered) and then dead-lettered, any other `4xx` is dropped;
at-least-once, de-duplicate on the event id. For hosts: a `Store` interface with an in-memory
implementation, an in-process `Dispatcher`, a polling `Worker`, and the reference table shape under
`sql/`. For receivers: `Verify`, with a tolerance window and constant-time comparison.
