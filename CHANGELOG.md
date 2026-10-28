# Changelog

Notable changes to this library, newest first, per release. Written for whoever hosts the library or
receives its deliveries.

## v0.2.3

This release requires **Go 1.27.3** (was 1.27.2). Raise your own module's `go` directive before upgrading.
No library source or public API changed.

### Changed

- `go-platform-kit` → v1.11.5 (was v1.11.4), used for correlation helpers.

## v0.2.2

A security release with one thing to act on: **this library now needs Go 1.27.2**. No source changed
here and nothing it does behaves differently: a delivery's shape, headers and retry behaviour are
exactly what v0.2.1 shipped.

### Changed

- **The module declares `go 1.27.2`** (was `1.27.0`). Go 1.27.2 fixes standard-library
  vulnerabilities that this library's code reaches under 1.27.0. `govulncheck` found eight it calls
  before the move and none after it: GO-2026-6603, -6605, -6607, -6608, -6610, -6611, -6613 and
  -6617, in `net/http`, `crypto/tls`, `net/textproto` and `mime/multipart`. Raise your own module's
  `go` directive to `1.27.2`; from there the go command downloads and uses that toolchain by itself.

- **`go-platform-kit` → v1.11.4** (was v1.11.3). This library uses only its correlation helpers:
  the header's name and the context key. The kit release requires azugo v0.40.0, so a host that also builds on azugo moves to it;
  the kit's v1.11.4 notes list what that changes for a service.

### Notes

- **CI's linter moved to golangci-lint v2.14.0** (was v2.13.1). The earlier release cannot read Go
  1.27.2's compiled standard library and stops before linting anything. CI's pinned GitHub Actions
  also moved to their current commits.

- The gate is green on Go 1.27.2: `go mod verify`, `go mod tidy -diff`, build, vet, `gofmt`,
  golangci-lint v2.14.0, `go test -race` with **0 races**, and the fuzz target for 30 seconds with no
  failing input; `govulncheck` finds nothing.

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
